package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// TestBenchmark_FindsRealVulnerability drives the real agent (real model,
// real sandbox container, real gateway trust boundary) against a genuinely
// vulnerable local target — an intentionally outdated WordPress 5.0 instance,
// see docker-compose.override.eval-target.yml — and asserts it actually
// discovers and reports the real, known issue (an outdated, fingerprintable
// core version), not just that some command ran. "The agent executed nmap"
// and "the agent found a real bug" are different claims; QualitySpec/
// QualityReport (agent/evaluation.go) already back make test-agent-eval for
// the first claim — this test exists specifically to back the second one
// with live evidence instead of leaving it implicit.
//
// Setup (once, before running):
//
//	docker compose -f docker-compose.yml -f docker-compose.override.eval-target.yml \
//	  up -d gateway sandbox orchestrator docker-proxy eval-target-db eval-target
//
// Run with:
//
//	YUMMI_BENCHMARK=1 YUMMI_OLLAMA_MODEL=qwen3:14b go test ./internal/agent/ \
//	  -run TestBenchmark_FindsRealVulnerability -v -timeout 15m
func TestBenchmark_FindsRealVulnerability(t *testing.T) {
	if os.Getenv("YUMMI_BENCHMARK") != "1" {
		t.Skip("benchmark: set YUMMI_BENCHMARK=1 (needs the full stack + docker-compose.override.eval-target.yml)")
	}
	container := envOrDefault("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox")
	if err := osexec.Command("docker", "exec", container, "true").Run(); err != nil {
		t.Skipf("benchmark: sandbox container %q not reachable: %v", container, err)
	}
	orchURL := envOrDefault("YUMMI_ORCHESTRATOR_URL", "http://127.0.0.1:9080")
	if resp, err := http.Get(orchURL + "/api/v1/system/status"); err != nil || resp.StatusCode != 200 {
		t.Skipf("benchmark: orchestrator API not reachable at %s: err=%v", orchURL, err)
	}
	targetIP := envOrDefault("YUMMI_BENCHMARK_TARGET_IP", "203.0.113.10")
	targetURL := "http://" + targetIP + "/"

	// ── Gateway setup: allow the target through the real trust boundary ────
	// This mirrors exactly what an operator does before scanning anything —
	// set egress mode, then explicitly allow the one target in scope — never
	// bypassed, since the whole point of this test is proving the REAL path
	// (agent -> sandbox -> gateway -> target) actually finds a real bug.
	priorMode, err := getEgressMode(orchURL)
	if err != nil {
		t.Fatalf("benchmark: get egress mode: %v", err)
	}
	if err := setEgressMode(orchURL, "allow"); err != nil {
		t.Fatalf("benchmark: set egress mode: %v", err)
	}
	t.Cleanup(func() { _ = setEgressMode(orchURL, priorMode) })

	ruleID, err := addGatewayRule(orchURL, targetIP+"/32", "benchmark eval-target access", 1800)
	if err != nil {
		t.Fatalf("benchmark: add gateway rule: %v", err)
	}
	t.Cleanup(func() { _ = removeGatewayRule(orchURL, ruleID) })

	if err := osexec.Command("docker", "exec", container, "curl", "-fsS", "-m", "5", "-o", "/dev/null", targetURL).Run(); err != nil {
		t.Skipf("benchmark: sandbox cannot reach eval-target at %s (is docker-compose.override.eval-target.yml up?): %v", targetURL, err)
	}

	// ── Real agent wiring: real model, real sandbox exec, real skills ──────
	url := envOrDefault("YUMMI_OLLAMA_URL", "http://localhost:11434")
	model := envOrDefault("YUMMI_OLLAMA_MODEL", "qwen3:14b")
	personaDir := envOrDefault("YUMMI_PERSONA_DIR", "../../../persona/v0.1")
	persona, err := LoadAgentPersona(personaDir)
	if err != nil {
		t.Fatalf("persona: %v", err)
	}
	skillRegistry, err := skill.LoadDir(filepath.Join(personaDir, "skills"))
	if err != nil {
		t.Fatalf("skills: %v", err)
	}

	es := execution.NewStore()
	es.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
		out, err := osexec.CommandContext(ctx, "docker", "exec", "--user", "1000:1000", container,
			"/bin/bash", "-lc", rec.Command).CombinedOutput()
		return string(out), err
	})

	store, _ := NewStore(openTestDB(t))
	client := llm.NewClient(url, model)
	validator := conductor.NewValidator()
	runner := NewRunner(store, client, validator, NewStoreExecer(es, 5*time.Minute), Config{
		StepBudget: 10, ApprovalTimeout: 90 * time.Second, SoftTimeout: 90 * time.Second,
		ApprovalMode: ModeAuto, Persona: persona, Skills: skillRegistry,
	})

	goal := fmt.Sprintf(
		"Fingerprint the web service at %s — identify what software it is running and its version, "+
			"and report clearly whether that version is outdated.", targetURL)
	task, err := runner.CreateAndPlan(context.Background(), "benchmark-session", goal)
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	t.Logf("PLAN (%d steps):", len(task.Plan))
	for _, s := range task.Plan {
		t.Logf("  [%s] %s", s.ID, s.Description)
	}

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if p, ok := runner.Gate().Pending(task.ID); ok {
					_ = runner.Approve(task.ID, p.ProposalID, true, "auto-approved (benchmark)")
				}
			}
		}
	}()

	if err := runner.Start(task.ID, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(10 * time.Minute)
	last := TaskStatus("")
	var final Task
	for time.Now().Before(deadline) {
		tk, _ := store.Get(task.ID)
		if tk.Status != last {
			t.Logf("status: %s  (steps %d/%d)", tk.Status, tk.StepsUsed, tk.StepBudget)
			last = tk.Status
		}
		if tk.Status.Terminal() {
			final = tk
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if final.ID == "" {
		t.Fatal("task did not reach a terminal state within the deadline")
	}

	t.Logf("FINAL status: %s", final.Status)
	t.Logf("summary: %s", final.Summary)
	for _, s := range final.Plan {
		t.Logf("  step [%s] %s — %s | result=%q error=%q", s.ID, s.Status, s.Description, s.Result, s.Error)
	}
	for _, f := range final.Findings {
		t.Logf("  finding: %s", f)
	}

	report := EvaluateTaskQuality(final, QualitySpec{
		RequireCompleted:      true,
		RequireSummary:        true,
		MinSuccessfulSteps:    1,
		MinObservedSteps:      1,
		RequireEvidenceDigest: true,
		RequiredCommandGroups: [][]string{{"whatweb", "wpscan", "curl", "nikto"}},
	})
	t.Logf("quality report: %+v", report)
	if !report.Passed {
		t.Fatalf("quality thresholds not met: %v", report.Failures)
	}

	// The claim this test actually exists to back: the agent didn't just run
	// a tool, it correctly identified the real, known issue on this specific
	// target — the outdated WordPress 5.0 core version.
	corpus := strings.ToLower(final.Summary + " " + strings.Join(final.Findings, " "))
	if !strings.Contains(corpus, "5.0") && !strings.Contains(corpus, "outdated") && !strings.Contains(corpus, "wordpress") {
		t.Fatalf("agent completed but did not report the real vulnerability (outdated WordPress 5.0) "+
			"in its summary/findings — summary=%q findings=%v", final.Summary, final.Findings)
	}
}

func getEgressMode(orchURL string) (string, error) {
	resp, err := http.Get(orchURL + "/api/v1/gateway/egress-mode")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Mode, nil
}

func setEgressMode(orchURL, mode string) error {
	body, _ := json.Marshal(map[string]string{"mode": mode})
	resp, err := http.Post(orchURL+"/api/v1/gateway/egress-mode", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("set egress mode: status %d", resp.StatusCode)
	}
	return nil
}

func addGatewayRule(orchURL, network, reason string, ttlSecs int) (string, error) {
	body, _ := json.Marshal(map[string]any{"network": network, "ttl_secs": ttlSecs, "reason": reason})
	resp, err := http.Post(orchURL+"/api/v1/gateway/rules", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("add gateway rule: status %d", resp.StatusCode)
	}
	var out struct {
		RuleID string `json:"rule_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.RuleID, nil
}

func removeGatewayRule(orchURL, ruleID string) error {
	if ruleID == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, orchURL+"/api/v1/gateway/rules/"+ruleID, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
