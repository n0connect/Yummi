// Package conductor contains the Yummi assistant orchestration primitives.
package conductor

// Proposal is a command/tool proposal produced by the assistant.
type Proposal struct {
	ID               string `json:"id"`
	Command          string `json:"command"`
	Rationale        string `json:"rationale"`
	RiskLevel        string `json:"risk_level"`
	RequiresApproval bool   `json:"requires_approval"`
	// ForceApprovalReason is set only when the command matches a known
	// irreversible/destructive pattern (destructiveFloor in validator.go) —
	// a semantic-danger floor independent of classifyRisk's shell-shape floor
	// and of the model's own risk_level. Unlike RequiresApproval (which the
	// agent loop's auto mode deliberately ignores — "auto" means don't gate
	// on ordinary risk), a non-empty ForceApprovalReason overrides even auto
	// mode: node_tool.go gates on it regardless of ApprovalMode. Empty means
	// no override; ordinary mode-driven gating applies as before.
	ForceApprovalReason string         `json:"force_approval_reason,omitempty"`
	TargetMCPServer     string         `json:"target_mcp_server,omitempty"`
	TargetTool          string         `json:"target_tool,omitempty"`
	ToolArgs            map[string]any `json:"tool_args,omitempty"`
}

// ChatResponse is the structured reply expected by the dashboard and API clients.
type ChatResponse struct {
	Intent     string     `json:"intent"`
	Message    string     `json:"message"`
	Proposals  []Proposal `json:"proposals,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
	Confidence float64    `json:"confidence"`
	References []string   `json:"references,omitempty"`

	// TaskGoal is set by the model with intent "start_task"; the API layer
	// creates an agent task from it. TaskID is filled by the API layer.
	TaskGoal string `json:"task_goal,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
}

// Persona contains the loaded prompt hierarchy fragments.
type Persona struct {
	Version      string
	Identity     string
	Values       []string
	Boundaries   []string
	Methodology  []string
	OutputSchema string
	Role         string
	Disposition  PersonaDisposition
}

// PersonaDisposition mirrors core_identity.yaml's character.disposition block —
// behavioral guidance (not safety rules) the model should fold into its
// system prompt: how it relates to the operator, how it handles uncertainty,
// how terse to be, and language matching.
type PersonaDisposition struct {
	OperatorRelationship string
	Honesty              string
	Economy              string
	LanguageParity       string
}
