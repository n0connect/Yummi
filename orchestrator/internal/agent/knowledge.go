package agent

import (
	"context"
	"log/slog"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

// recordTaskFindings writes a task's discoveries to the knowledge store using
// the graceful contract: structured FindingDetail when the brain supplied it,
// otherwise each freetext string wrapped as a low-severity "info" note. A nil
// store is a no-op (degraded mode), never a panic.
//
// Findings are LLM-authored summaries of sandbox tool output, which is
// adversarial (the pentest target controls it). Like memory writes
// (rememberNode), a finding is persisted and re-injected into every future
// prompt for the session (knowledge/retrieval.go), so it is the same
// cross-task prompt-injection persistence vector — content-layer fail-closed
// applies here too. Findings are agent-authored, not user-mediated, so we use
// ScopeContext (detect, don't hard-block on borderline patterns) but still
// refuse to persist a finding that trips the scan, consistent with the
// REMEMBER gate.
func recordTaskFindings(ctx context.Context, ks *knowledge.Store, sessionID, taskID string, freetext []string, detail []FindingDetail) {
	if ks == nil {
		return
	}
	for _, d := range detail {
		if d.Title == "" && d.Description == "" {
			continue
		}
		sev := d.Severity
		if sev == "" {
			sev = "info"
		}
		// ANSI stripped before the threat scan — same ordering as the agent's
		// own observations (graph.go: StripANSI, then Banner) — so a target
		// can't use an escape sequence to split a banned phrase past the regex.
		// SanitizeAgentText (<think> blocks, echoed protocol-tag lines) on top
		// of the existing ANSI strip: these three fields are re-injected into
		// every future prompt for the session AND served wire-visible via the
		// Trace tab's findings list, so both defenses belong here.
		title := SanitizeAgentText(threatscan.StripANSI(d.Title))
		desc := SanitizeAgentText(threatscan.StripANSI(d.Description))
		evidence := SanitizeAgentText(threatscan.StripANSI(d.Evidence))
		if msg := threatscan.FirstThreatMessage(title+"\n"+desc+"\n"+evidence, threatscan.ScopeContext); msg != "" {
			slog.Warn("agent: structured finding blocked by threat scan", "task", taskID, "reason", msg)
			continue
		}
		if err := ks.RecordFinding(ctx, knowledge.Finding{
			SessionID: sessionID, TaskID: taskID, Severity: sev,
			Title: title, Description: desc, Evidence: evidence,
			Source: "agent",
		}); err != nil {
			slog.Warn("agent: record structured finding failed", "task", taskID, "error", err)
		}
	}
	for _, text := range freetext {
		if text == "" {
			continue
		}
		text = threatscan.StripANSI(text)
		if msg := threatscan.FirstThreatMessage(text, threatscan.ScopeContext); msg != "" {
			slog.Warn("agent: freetext finding blocked by threat scan", "task", taskID, "reason", msg)
			continue
		}
		title := text[:utf8SafeCut(text, 120)]
		if err := ks.RecordFinding(ctx, knowledge.Finding{
			SessionID: sessionID, TaskID: taskID, Severity: "info",
			Title: title, Description: text, Source: "agent",
		}); err != nil {
			slog.Warn("agent: record freetext finding failed", "task", taskID, "error", err)
		}
	}
}
