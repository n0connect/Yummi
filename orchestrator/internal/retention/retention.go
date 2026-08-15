// Package retention prunes old, low-stakes operational rows from the shared
// SQLite database so a long-running orchestrator (docker-compose's
// restart: unless-stopped) doesn't grow its DB file unbounded over months or
// years of use. Disabled by default (YUMMI_DATA_RETENTION_DAYS unset or 0)
// — a security tool should never silently delete an operator's data; this
// is opt-in, not a background cleanup an operator has to discover and turn
// off.
//
// Deliberately, permanently out of scope for pruning, no matter what the
// retention window is: sessions, findings, persistent_context,
// procedural_memory, audit_log, and agent_tasks. These are exactly the
// compliance/attribution-relevant records an operator engaged in a real
// pentest would want kept — a finding or a task's summary is the actual
// deliverable, not disposable operational noise. Only three tables are ever
// pruned: chat_messages (UI conversation history — reconstructable context,
// not evidence), agent_activity (the TRACE timeline — explicitly documented
// elsewhere, agent/activity.go, as "purely observational... carries no
// execution authority"), and executions (raw command transcripts — the
// value already distilled out of them lives in findings/tactics, which this
// package never touches).
package retention

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Result reports how many rows were deleted per table, for logging.
type Result struct {
	ChatMessages int64
	Activity     int64
	Executions   int64
}

// Total sums every pruned table's row count, for a single "anything happen"
// check before logging.
func (r Result) Total() int64 { return r.ChatMessages + r.Activity + r.Executions }

// PruneOlderThan deletes rows older than cutoff from the three prunable
// tables described in the package doc. A nil db is a no-op, matching this
// codebase's nil-safe-degradation pattern. Each table is pruned
// independently — one table's failure doesn't block the others, and is
// logged rather than returned as an error (this always runs from a
// background goroutine with no caller to report to).
func PruneOlderThan(ctx context.Context, db *sql.DB, cutoff time.Time) Result {
	var res Result
	if db == nil {
		return res
	}
	// chat_messages.created_at and executions.created_at are Unix seconds;
	// agent_activity.timestamp is Unix milliseconds (see agent.Store's
	// RecordActivity) — deliberately different units per table, matched here.
	if n, err := pruneTable(ctx, db, "chat_messages", "created_at", cutoff.Unix()); err != nil {
		slog.Warn("retention: prune chat_messages failed", "error", err)
	} else {
		res.ChatMessages = n
	}
	if n, err := pruneTable(ctx, db, "agent_activity", "timestamp", cutoff.UnixMilli()); err != nil {
		slog.Warn("retention: prune agent_activity failed", "error", err)
	} else {
		res.Activity = n
	}
	if n, err := pruneTable(ctx, db, "executions", "created_at", cutoff.Unix()); err != nil {
		slog.Warn("retention: prune executions failed", "error", err)
	} else {
		res.Executions = n
	}
	return res
}

func pruneTable(ctx context.Context, db *sql.DB, table, timeCol string, cutoff int64) (int64, error) {
	result, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+timeCol+" < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// StartPurger runs PruneOlderThan on a fixed interval until ctx is
// cancelled, mirroring conductor.ApprovalStore's own StartPurger pattern.
// retentionDays <= 0 disables pruning entirely (the safe default) — no
// goroutine is even started, so there is zero background cost when the
// operator hasn't opted in.
func StartPurger(ctx context.Context, db *sql.DB, retentionDays int, interval time.Duration) {
	if db == nil || retentionDays <= 0 || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := time.Now().AddDate(0, 0, -retentionDays)
				res := PruneOlderThan(ctx, db, cutoff)
				if res.Total() > 0 {
					slog.Info("retention: pruned old records",
						"chat_messages", res.ChatMessages, "agent_activity", res.Activity,
						"executions", res.Executions, "retention_days", retentionDays)
				}
			}
		}
	}()
}
