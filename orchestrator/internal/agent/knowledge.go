package agent

import (
	"context"
	"log/slog"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// recordTaskFindings writes a task's discoveries to the knowledge store using
// the graceful contract: structured FindingDetail when the brain supplied it,
// otherwise each freetext string wrapped as a low-severity "info" note. A nil
// store is a no-op (degraded mode), never a panic.
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
		if err := ks.RecordFinding(ctx, knowledge.Finding{
			SessionID: sessionID, TaskID: taskID, Severity: sev,
			Title: d.Title, Description: d.Description, Evidence: d.Evidence,
			Source: "agent",
		}); err != nil {
			slog.Warn("agent: record structured finding failed", "task", taskID, "error", err)
		}
	}
	for _, text := range freetext {
		if text == "" {
			continue
		}
		title := text
		if len(title) > 120 {
			title = title[:120]
		}
		if err := ks.RecordFinding(ctx, knowledge.Finding{
			SessionID: sessionID, TaskID: taskID, Severity: "info",
			Title: title, Description: text, Source: "agent",
		}); err != nil {
			slog.Warn("agent: record freetext finding failed", "task", taskID, "error", err)
		}
	}
}
