// Package cli implements the yummi command-line interface.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/n0connect/yummi/internal/config"
	"github.com/n0connect/yummi/internal/version"
)

const containerName = "yummi-sandbox"

type commandOptions struct {
	Help bool
}

type topLevelOptions struct {
	Action string
	Opts   commandOptions
}

const (
	actionUI      = "ui"
	actionStart   = "start"
	actionStop    = "stop"
	actionStatus  = "status"
	actionDoctor  = "doctor"
	actionVersion = "version"
)

// Run executes the yummi CLI and returns a process-style exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	top, err := parseTopLevelOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		printUsage(stderr)
		return 2
	}
	if top.Opts.Help {
		printUsage(stdout)
		return 0
	}

	switch top.Action {
	case actionUI:
		return runUI(stdout, stderr)
	case actionVersion:
		return runVersion(stdout, stderr)
	case actionStart:
		return runStart(stdout, stderr)
	case actionStop:
		return runStop(stdout, stderr)
	case actionStatus:
		return runStatus(stdout, stderr)
	case actionDoctor:
		return runDoctor(stdout, stderr)
	default:
		fmt.Fprintf(stderr, "error: unknown action %q\n", top.Action)
		return 2
	}
}

func parseTopLevelOptions(args []string) (topLevelOptions, error) {
	top := topLevelOptions{}
	if len(args) == 0 {
		top.Action = actionUI
		return top, nil
	}

	setAction := func(action string) error {
		if top.Action != "" {
			return fmt.Errorf("multiple operation flags are not allowed: %s and %s", top.Action, action)
		}
		top.Action = action
		return nil
	}

	for _, arg := range args {
		switch arg {
		case "--json":
			return topLevelOptions{}, fmt.Errorf("--json is no longer supported; use plain output")
		case "--help", "-h":
			top.Opts.Help = true
		case "--start":
			if err := setAction(actionStart); err != nil {
				return topLevelOptions{}, err
			}
		case "--stop":
			if err := setAction(actionStop); err != nil {
				return topLevelOptions{}, err
			}
		case "--status":
			if err := setAction(actionStatus); err != nil {
				return topLevelOptions{}, err
			}
		case "--doctor":
			if err := setAction(actionDoctor); err != nil {
				return topLevelOptions{}, err
			}
		case "--version":
			if err := setAction(actionVersion); err != nil {
				return topLevelOptions{}, err
			}
		case "":
			continue
		default:
			if strings.HasPrefix(arg, "-") {
				return topLevelOptions{}, fmt.Errorf("unknown flag %q", arg)
			}
			return topLevelOptions{}, fmt.Errorf("subcommands are removed; use long flags (for example: yummi --start)")
		}
	}

	if top.Action == "" {
		top.Action = actionUI
	}
	return top, nil
}

func runVersion(stdout, _ io.Writer) int {
	info := version.Current()
	fmt.Fprintf(stdout, "%s (git=%s, built=%s)\n", info.Semver, info.GitSHA, info.BuildDate)
	return 0
}

func runStart(stdout, stderr io.Writer) int {
	cfg, err := config.Load("")
	if err != nil {
		return reportErr(stderr, "load config", err)
	}
	args := []string{"compose", "-f", cfg.Runtime.ComposeFile, "up", "-d", cfg.Runtime.SandboxService}
	if err := runExternal("docker", args, stdout, stderr); err != nil {
		return reportErr(stderr, "start container", err)
	}
	fmt.Fprintf(stdout, "started service %s\n", cfg.Runtime.SandboxService)
	return 0
}

func runStop(stdout, stderr io.Writer) int {
	cfg, err := config.Load("")
	if err != nil {
		return reportErr(stderr, "load config", err)
	}
	args := []string{"compose", "-f", cfg.Runtime.ComposeFile, "stop", cfg.Runtime.SandboxService}
	if err := runExternal("docker", args, stdout, stderr); err != nil {
		return reportErr(stderr, "stop container", err)
	}
	fmt.Fprintf(stdout, "stopped service %s\n", cfg.Runtime.SandboxService)
	return 0
}

type statusInfo struct {
	ContainerState string `json:"container_state"`
	HomePath       string `json:"home_path"`
	HomeExists     bool   `json:"home_exists"`
	HomeUsageBytes int64  `json:"home_usage_bytes"`
	DiskFreeBytes  uint64 `json:"disk_free_bytes"`
}

func runStatus(stdout, _ io.Writer) int {
	home := config.HomeDir()
	st := statusInfo{
		ContainerState: detectContainerState(),
		HomePath:       home,
	}
	if _, err := os.Stat(home); err == nil {
		st.HomeExists = true
		usage, usageErr := dirSize(home)
		if usageErr == nil {
			st.HomeUsageBytes = usage
		}
	}
	diskRef := home
	if !st.HomeExists {
		diskRef = filepath.Dir(home)
	}
	if free, err := diskFree(diskRef); err == nil {
		st.DiskFreeBytes = free
	}
	fmt.Fprintf(stdout, "sandbox state: %s\n", st.ContainerState)
	fmt.Fprintf(stdout, "home: %s (exists=%t)\n", st.HomePath, st.HomeExists)
	fmt.Fprintf(stdout, "home usage: %s\n", humanBytes(st.HomeUsageBytes))
	fmt.Fprintf(stdout, "disk free: %s\n", humanBytes(int64(st.DiskFreeBytes)))
	return 0
}

type checkResult struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Details string `json:"details"`
}

func runDoctor(stdout, _ io.Writer) int {
	home := config.HomeDir()

	sectionHeader(stdout, "Yummi Doctor")

	type namedCheck struct {
		label string
		fn    func() checkResult
	}

	checks := []namedCheck{
		{"docker", func() checkResult { return checkCommand("docker") }},
		{"docker compose", checkDockerCompose},
		{"docker daemon", checkDockerDaemon},
		{"architecture", checkArch},
		{home, func() checkResult { return checkDir(home) }},
		{filepath.Join(home, "wordlists"), func() checkResult { return checkDir(filepath.Join(home, "wordlists")) }},
		{filepath.Join(home, "workspace"), func() checkResult { return checkDir(filepath.Join(home, "workspace")) }},
		{filepath.Join(home, "payloads"), func() checkResult { return checkDir(filepath.Join(home, "payloads")) }},
		{filepath.Join(home, "logs"), func() checkResult { return checkDir(filepath.Join(home, "logs")) }},
	}

	failed := 0
	for _, nc := range checks {
		ch := nc.fn()
		if ch.OK {
			stepOK(stdout, ch.Name, ch.Details)
		} else {
			stepFail(stdout, ch.Name, ch.Details)
			failed++
		}
	}

	fmt.Fprintln(stdout)
	if failed == 0 {
		fmt.Fprintf(stdout, "  %s  %s\n", boldGreen("doctor:"), green("all checks passed"))
		return 0
	}
	fmt.Fprintf(stdout, "  %s  %s\n", boldRed("doctor:"), red(fmt.Sprintf("%d check(s) failed", failed)))
	return 1
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "yummi [--start|--stop|--status|--doctor|--version] [--help]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Default:")
	fmt.Fprintln(w, "  yummi      Start/check services and open Yummi UI in app-window mode")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Operation flags:")
	fmt.Fprintln(w, "  --version  Print version, git SHA, build date")
	fmt.Fprintln(w, "  --start    Start runtime services via docker compose")
	fmt.Fprintln(w, "  --stop     Stop runtime services via docker compose")
	fmt.Fprintln(w, "  --status   Show sandbox state and local storage stats")
	fmt.Fprintln(w, "  --doctor   Run environment diagnostics")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  --help     Show this help")
}

func runExternal(name string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("%s %s: exited with code %d", name, strings.Join(args, " "), ee.ExitCode())
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func detectContainerState() string {
	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", containerName)
	buf, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := string(ee.Stderr)
			if strings.Contains(strings.ToLower(msg), "no such object") {
				return "not_found"
			}
		}
		return "unknown"
	}
	return strings.TrimSpace(string(buf))
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// diskFree is implemented in platform-specific files:
// - cli_unix.go for Unix/Linux/macOS
// - cli_windows.go for Windows

func humanBytes(v int64) string {
	if v < 0 {
		return "unknown"
	}
	if v < 1024 {
		return fmt.Sprintf("%d B", v)
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(v)
	u := 0
	for f >= 1024 && u < len(units)-1 {
		f /= 1024
		u++
	}
	return fmt.Sprintf("%.1f %s", f, units[u])
}

func reportErr(stderr io.Writer, context string, err error) int {
	fmt.Fprintf(stderr, "error: %s: %v\n", context, err)
	return 1
}

func checkCommand(name string) checkResult {
	_, err := exec.LookPath(name)
	if err != nil {
		return checkResult{Name: name, OK: false, Details: "not found"}
	}
	return checkResult{Name: name, OK: true, Details: "found"}
}

func checkDockerCompose() checkResult {
	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		return checkResult{Name: "docker compose", OK: false, Details: "unavailable"}
	}
	return checkResult{Name: "docker compose", OK: true, Details: "available"}
}

func checkDockerDaemon() checkResult {
	cmd := exec.Command("docker", "info")
	if err := cmd.Run(); err != nil {
		return checkResult{Name: "docker daemon", OK: false, Details: "not reachable"}
	}
	return checkResult{Name: "docker daemon", OK: true, Details: "reachable"}
}

func checkArch() checkResult {
	arch := runtime.GOARCH
	if arch == "arm64" {
		return checkResult{Name: "architecture", OK: true, Details: arch}
	}
	return checkResult{Name: "architecture", OK: true, Details: arch + " (arm64 recommended)"}
}

func checkDir(path string) checkResult {
	if stat, err := os.Stat(path); err != nil {
		return checkResult{Name: path, OK: false, Details: "missing"}
	} else if !stat.IsDir() {
		return checkResult{Name: path, OK: false, Details: "not a directory"}
	}
	return checkResult{Name: path, OK: true, Details: "present"}
}

// checkDockerDaemonErr returns an error if the Docker daemon is unreachable.
func checkDockerDaemonErr() error {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// checkDockerComposeErr returns an error if the docker compose plugin is missing.
func checkDockerComposeErr() error {
	cmd := exec.Command("docker", "compose", "version")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
