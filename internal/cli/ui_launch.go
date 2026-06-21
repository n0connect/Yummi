package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/n0connect/yummi/internal/config"
)

const (
	// defaultUIURL must match the orchestrator's default bind address/port
	// (YUMMI_ORCHESTRATOR_HOST/PORT, see orchestrator/internal/config/config.go) —
	// the orchestrator binary serves both the API and the embedded UI assets.
	defaultUIURL          = "http://127.0.0.1:9080"
	readinessTimeout      = 20 * time.Second
	readinessPollInterval = 500 * time.Millisecond
)

type launchContext struct {
	UIURL               string
	SandboxStatus       string
	GatewayStatus       string
	OrchestratorStarted bool
	LogPath             string
}

func runUI(stdout, stderr io.Writer) int {
	ctx := &launchContext{UIURL: uiURL()}
	logger := newLaunchLogger(stderr)

	// ── Pre-flight checks ──────────────────────────────────────────────────
	sectionHeader(stdout, "Yummi Pre-flight")
	if !runPreflightChecks(stdout, stderr) {
		fmt.Fprintln(stderr)
		fmt.Fprintf(stderr, "%s\n", boldRed("  Pre-flight failed. Run 'yummi --doctor' for details."))
		return 1
	}

	// ── Start orchestrator if not already running ───────────────────────────────────
	sectionHeader(stdout, "Starting Services")
	stepInfo(stdout, "orchestrator", "checking…")
	startedCmd, startedLogPath, err := ensureOrchestratorRunning(ctx.UIURL, logger)
	if err != nil {
		stepFail(stdout, "orchestrator", err.Error())
		printStartupHints(stderr)
		return 1
	}
	if startedCmd != nil {
		ctx.OrchestratorStarted = true
		ctx.LogPath = startedLogPath
		stepOK(stdout, "orchestrator", "spawned → "+startedLogPath)
	} else {
		stepOK(stdout, "orchestrator", "already running")
	}

	// ── Wait for readiness ─────────────────────────────────────────────────
	sectionHeader(stdout, "Waiting for Readiness")
	stepInfo(stdout, "health probes", "sandbox + gateway (timeout 20s)…")
	readyErr := waitUntilReady(ctx.UIURL, readinessTimeout, func(sandbox, gateway string) {
		ctx.SandboxStatus = sandbox
		ctx.GatewayStatus = gateway
	})
	if readyErr != nil {
		if startedCmd != nil {
			stopSpawnedProcess(startedCmd, logger)
		}
		stepFail(stdout, "health probes", readyErr.Error())
		printReadinessDetails(stderr, ctx)
		printStartupHints(stderr)
		return 1
	}
	stepOK(stdout, "sandbox container", ctx.SandboxStatus)
	stepOK(stdout, "gateway trust boundary", ctx.GatewayStatus)

	// ── Open browser ───────────────────────────────────────────────────────
	sectionHeader(stdout, "Launching UI")
	stepInfo(stdout, "browser", ctx.UIURL)
	if err := openAppWindow(ctx.UIURL); err != nil {
		stepFail(stdout, "browser", err.Error())
		fmt.Fprintln(stderr, "hint: install Chromium/Chrome/Edge or set YUMMI_UI_BROWSER to an app-capable browser binary")
		return 1
	}

	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  %s  %s\n", boldGreen("YUMMI"), cyan(ctx.UIURL))
	if ctx.LogPath != "" {
		fmt.Fprintf(stdout, "  %s  %s\n", dim("orchestrator log"), ctx.LogPath)
	}
	fmt.Fprintln(stdout)
	return 0
}

// runPreflightChecks runs startup validations and prints colored step output.
// Returns true if all required checks pass.
func runPreflightChecks(stdout, stderr io.Writer) bool {
	_ = stderr
	pass := true

	// Docker daemon
	if err := checkDockerDaemonErr(); err != nil {
		stepFail(stdout, "Docker daemon", "not reachable – start Docker Desktop")
		pass = false
	} else {
		stepOK(stdout, "Docker daemon", "running")
	}

	// docker compose plugin
	if err := checkDockerComposeErr(); err != nil {
		stepFail(stdout, "docker compose", "plugin not found")
		pass = false
	} else {
		stepOK(stdout, "docker compose", "available")
	}

	// orchestrator binary (warn, not fatal — we can still try if already running)
	if _, err := findOrchestratorBinary(); err != nil {
		stepWarn(stdout, "yummi-orchestrator binary", "not found in PATH or next to binary")
	} else {
		stepOK(stdout, "yummi-orchestrator binary", "found")
	}

	// sandbox container state
	state := detectContainerState()
	switch state {
	case "running":
		stepOK(stdout, "sandbox container", "running")
	case "not_found":
		stepWarn(stdout, "sandbox container", "not found – run 'yummi --start'")
	default:
		stepWarn(stdout, "sandbox container", state)
	}

	return pass
}

func uiURL() string {
	if v := os.Getenv("YUMMI_UI_URL"); v != "" {
		return v
	}
	return defaultUIURL
}

func ensureOrchestratorRunning(baseURL string, logger *slog.Logger) (*exec.Cmd, string, error) {
	if _, _, err := probeInfra(baseURL); err == nil {
		return nil, "", nil
	}

	home := config.HomeDir()
	logDir := filepath.Join(home, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "orchestrator.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open orchestrator log file: %w", err)
	}

	orchestratorBin, err := findOrchestratorBinary()
	if err != nil {
		_ = logFile.Close()
		return nil, "", err
	}

	cmd := exec.Command(orchestratorBin)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, "", fmt.Errorf("start orchestrator (%s): %w", orchestratorBin, err)
	}
	_ = logFile.Close()

	logger.Info("spawned orchestrator", "binary", orchestratorBin, "pid", cmd.Process.Pid, "log_path", logPath)
	return cmd, logPath, nil
}

func stopSpawnedProcess(cmd *exec.Cmd, logger *slog.Logger) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil {
		logger.Warn("failed to kill spawned orchestrator", "error", err)
		return
	}
	_ = cmd.Wait()
	logger.Info("killed spawned orchestrator after readiness failure", "pid", cmd.Process.Pid)
}

func findOrchestratorBinary() (string, error) {
	if p := os.Getenv("YUMMI_ORCHESTRATOR_BINARY"); p != "" {
		if existsExecutable(p) {
			return p, nil
		}
		return "", fmt.Errorf("YUMMI_ORCHESTRATOR_BINARY does not exist or is not executable: %s", p)
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "yummi-orchestrator")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if existsExecutable(candidate) {
			return candidate, nil
		}
	}

	name := "yummi-orchestrator"
	if runtime.GOOS == "windows" {
		name = "yummi-orchestrator.exe"
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	return "", errors.New("yummi-orchestrator binary not found (expected next to yummi binary or in PATH)")
}

func existsExecutable(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func waitUntilReady(baseURL string, timeout time.Duration, sink func(sandbox, gateway string)) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		sandbox, gateway, err := probeInfra(baseURL)
		if sink != nil {
			sink(sandbox, gateway)
		}
		if err == nil {
			if sandbox == "healthy" && gateway == "connected" {
				return nil
			}
			lastErr = fmt.Errorf("sandbox=%s gateway=%s", sandbox, gateway)
		} else {
			lastErr = err
		}

		if time.Now().After(deadline) {
			if lastErr == nil {
				lastErr = errors.New("unknown readiness failure")
			}
			return lastErr
		}
		time.Sleep(readinessPollInterval)
	}
}

func probeInfra(baseURL string) (string, string, error) {
	sandboxURL, err := joinURL(baseURL, "/api/v1/sandbox/status")
	if err != nil {
		return "unknown", "unknown", err
	}
	gatewayURL, err := joinURL(baseURL, "/api/v1/gateway/status")
	if err != nil {
		return "unknown", "unknown", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sandbox, err := getStatusField(ctx, sandboxURL)
	if err != nil {
		return "unknown", "unknown", err
	}
	gateway, err := getStatusField(ctx, gatewayURL)
	if err != nil {
		return sandbox, "unknown", err
	}
	return sandbox, gateway, nil
}

func getStatusField(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "unknown", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "unknown", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "unknown", fmt.Errorf("%s returned status %d", targetURL, resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "unknown", err
	}
	status, _ := payload["status"].(string)
	if status == "" {
		return "unknown", errors.New("status field missing in readiness payload")
	}
	return status, nil
}

func joinURL(base, path string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse YUMMI_UI_URL: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func openAppWindow(targetURL string) error {
	browsers := browserCandidates()
	var errs []string
	for _, candidate := range browsers {
		execPath, err := resolveBrowserBinary(candidate)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}

		cmd := exec.Command(execPath, "--app="+targetURL, "--new-window")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", execPath, err))
			continue
		}
		return nil
	}
	return fmt.Errorf("no app-window capable browser found (%s)", strings.Join(errs, "; "))
}

func browserCandidates() []string {
	var out []string
	if explicit := strings.TrimSpace(os.Getenv("YUMMI_UI_BROWSER")); explicit != "" {
		out = append(out, explicit)
	}

	switch runtime.GOOS {
	case "darwin":
		out = append(out,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"google-chrome",
			"chromium",
			"microsoft-edge",
		)
	case "windows":
		out = append(out,
			"chrome.exe",
			"msedge.exe",
			"chromium.exe",
		)
	default:
		out = append(out,
			"google-chrome",
			"google-chrome-stable",
			"chromium",
			"chromium-browser",
			"microsoft-edge",
		)
	}

	return uniqStrings(out)
}

func resolveBrowserBinary(candidate string) (string, error) {
	if candidate == "" {
		return "", errors.New("empty candidate")
	}
	if filepath.IsAbs(candidate) {
		if existsExecutable(candidate) {
			return candidate, nil
		}
		return "", errors.New("not executable")
	}
	p, err := exec.LookPath(candidate)
	if err != nil {
		return "", err
	}
	return p, nil
}

func uniqStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func printStartupHints(w io.Writer) {
	fmt.Fprintln(w, "hints:")
	fmt.Fprintln(w, "  - run: yummi --doctor")
	fmt.Fprintln(w, "  - start services: yummi --start")
	fmt.Fprintln(w, "  - verify status: yummi --status")
}

func printReadinessDetails(w io.Writer, lc *launchContext) {
	fmt.Fprintln(w, "readiness diagnostics:")
	fmt.Fprintf(w, "  ui_url: %s\n", lc.UIURL)
	fmt.Fprintf(w, "  sandbox_status: %s\n", fallbackStatus(lc.SandboxStatus))
	fmt.Fprintf(w, "  gateway_status: %s\n", fallbackStatus(lc.GatewayStatus))
	if lc.OrchestratorStarted {
		fmt.Fprintf(w, "  orchestrator_started_by_cli: true\n")
	}
	if lc.LogPath != "" {
		fmt.Fprintf(w, "  orchestrator_log: %s\n", lc.LogPath)
	}
}

func fallbackStatus(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func newLaunchLogger(stderr io.Writer) *slog.Logger {
	h := slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h)
}
