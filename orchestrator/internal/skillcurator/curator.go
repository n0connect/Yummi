package skillcurator

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
	"gopkg.in/yaml.v3"
)

// Curator drafts a candidate skill from a task trajectory and quarantines it.
type Curator struct {
	store    *Store
	chat     ChatFunc
	registry *skill.Registry // may be nil: dedup check is then skipped
}

// NewCurator wires the curator to a store, an LLM chat function, and the live
// skill registry. registry may be nil (dedup against already-loaded skills is
// then skipped, but dedup against other pending drafts still runs via store).
func NewCurator(store *Store, chat ChatFunc, registry *skill.Registry) *Curator {
	return &Curator{store: store, chat: chat, registry: registry}
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

	traj = SanitizeTrajectory(traj)
	drafted, err := DraftSkill(ctx, c.chat, traj)
	if err != nil {
		slog.Warn("skillcurator: draft failed", "error", err, "task_id", traj.TaskID)
		return
	}

	// Avoid spamming the operator's review queue: a candidate that overlaps
	// enough with a skill that's already LIVE (registry) or already AWAITING
	// review (another pending draft) teaches nothing new — skip it rather than
	// queue a near-duplicate for the operator to triage again.
	if dup, reason := c.isDuplicate(ctx, drafted); dup {
		slog.Info("skillcurator: draft is a near-duplicate, skipping", "task_id", traj.TaskID, "name", drafted.Name, "reason", reason)
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
	if msg := threatscan.FirstThreatMessage(drafted.Name+"\n"+drafted.Description+"\n"+drafted.Knowledge, threatscan.ScopeStrict); msg != "" {
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

// isDuplicate reports whether candidate overlaps enough with an already-live
// registry skill or an already-pending draft to count as teaching nothing
// new, plus the name of the match (for logging). Deliberately a cheap
// keyword check — sharing at least one Tool AND at least one Trigger — the
// same substring-matching philosophy skill.Registry.Select already uses for
// retrieval, rather than standing up a parallel embedding pipeline just for
// this. A shared tool alone (e.g. two very different nmap skills) is not
// enough; both signals must agree.
func (c *Curator) isDuplicate(ctx context.Context, candidate skill.Skill) (bool, string) {
	if c.registry != nil {
		for _, existing := range c.registry.All() {
			if overlaps(candidate, existing) {
				return true, "registry:" + existing.Name
			}
		}
	}
	pending, err := c.store.List(ctx, "pending")
	if err != nil {
		slog.Warn("skillcurator: list pending for dedup check failed", "error", err)
		return false, ""
	}
	for _, ps := range pending {
		var existing skill.Skill
		if err := yaml.Unmarshal([]byte(ps.DraftYAML), &existing); err != nil {
			continue
		}
		if overlaps(candidate, existing) {
			return true, "pending:" + existing.Name
		}
	}
	return false, ""
}

// overlaps reports whether a and b share at least one Tool AND at least one
// Trigger (case-insensitive, trimmed).
func overlaps(a, b skill.Skill) bool {
	return shareAny(a.Tools, b.Tools) && shareAny(a.Triggers, b.Triggers)
}

func shareAny(a, b []string) bool {
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
			set[v] = struct{}{}
		}
	}
	for _, v := range b {
		if _, ok := set[strings.ToLower(strings.TrimSpace(v))]; ok {
			return true
		}
	}
	return false
}
