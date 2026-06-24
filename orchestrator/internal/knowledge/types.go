// Package knowledge is the single owner of Yummi's world model: targets,
// findings, and target-scrubbed procedural memory. It is a leaf package —
// it depends only on database/sql and the scope leaf — so the agent,
// executor, and API all write structured knowledge to one place.
package knowledge

// Target is an in-scope host/IP discovered or declared for a session.
type Target struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Value     string `json:"value"`
	InScope   bool   `json:"in_scope"`
	Notes     string `json:"notes"`
	CreatedAt int64  `json:"created_at"`
}

// Finding is a structured security finding (factual memory layer).
// Source is one of "manual" | "agent" | "subagent"; TaskID is the agent
// task that produced it (empty for operator-entered findings).
type Finding struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	TargetID    string `json:"target_id,omitempty"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	Source      string `json:"source"`
	TaskID      string `json:"task_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// Tactic is a portable, target-scrubbed procedural-memory entry: "this
// command shape, run with this tool, had this outcome." It carries NO
// session or target value.
type Tactic struct {
	ID             string   `json:"id"`
	Tool           string   `json:"tool"`
	CommandPattern string   `json:"command_pattern"` // target replaced with <TARGET>
	Outcome        string   `json:"outcome"`         // worked | failed | inconclusive
	ContextTags    []string `json:"context_tags,omitempty"`
	Note           string   `json:"note,omitempty"`
	HitCount       int      `json:"hit_count"`
	LastUsedAt     int64    `json:"last_used_at"`
	CreatedAt      int64    `json:"created_at"`
}
