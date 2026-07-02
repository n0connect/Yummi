package skillcurator

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
	"gopkg.in/yaml.v3"
)

// Curator drafts a candidate skill from a task trajectory and quarantines it.
type Curator struct {
	store *Store
	chat  ChatFunc
}

// NewCurator wires the curator to a store and an LLM chat function.
func NewCurator(store *Store, chat ChatFunc) *Curator {
	return &Curator{store: store, chat: chat}
}

// ProposeFromTask drafts a skill from traj, scans it with the exact same
// content-layer check skill.LoadDir already applies to hand-authored skill
// YAML, and stores the result — "pending" if clean, "rejected" (with the
// scan result, for audit) if flagged. A flagged draft NEVER reaches the
// pending queue an operator reviews.
//
// Never returns an error: this is called fire-and-forget after a task
// finishes (agent.Config.SkillCurator), so every failure mode here is
// logged and swallowed rather than propagated to a caller that can't act on it.
func (c *Curator) ProposeFromTask(ctx context.Context, traj Trajectory) {
	if c == nil || c.store == nil || c.chat == nil {
		return
	}

	drafted, err := DraftSkill(ctx, c.chat, traj)
	if err != nil {
		slog.Warn("skillcurator: draft failed", "error", err, "task_id", traj.TaskID)
		return
	}

	yamlBytes, err := yaml.Marshal(drafted)
	if err != nil {
		slog.Warn("skillcurator: marshal draft failed", "error", err, "task_id", traj.TaskID)
		return
	}
	trajJSON, err := json.Marshal(traj)
	if err != nil {
		slog.Warn("skillcurator: marshal trajectory failed", "error", err, "task_id", traj.TaskID)
		return
	}

	ps := PendingSkill{
		Origin:     "auto",
		TaskID:     traj.TaskID,
		SessionID:  traj.SessionID,
		Name:       drafted.Name,
		DraftYAML:  string(yamlBytes),
		Trajectory: string(trajJSON),
	}
	if msg := threatscan.FirstThreatMessage(drafted.Description+"\n"+drafted.Knowledge, threatscan.ScopeStrict); msg != "" {
		ps.Status = "rejected"
		ps.ThreatScanResult = msg
		ps.RejectReason = "threat scan: " + msg
		slog.Warn("skillcurator: draft rejected by threat scan", "task_id", traj.TaskID, "reason", msg)
	} else {
		ps.Status = "pending"
	}

	if err := c.store.Put(ctx, ps); err != nil {
		slog.Warn("skillcurator: put pending skill failed", "error", err, "task_id", traj.TaskID)
	}
}
