// Package conductor contains the Yummi assistant orchestration primitives.
package conductor

// Proposal is a command/tool proposal produced by the assistant.
type Proposal struct {
	ID               string         `json:"id"`
	Command          string         `json:"command"`
	Rationale        string         `json:"rationale"`
	RiskLevel        string         `json:"risk_level"`
	RequiresApproval bool           `json:"requires_approval"`
	TargetMCPServer  string         `json:"target_mcp_server,omitempty"`
	TargetTool       string         `json:"target_tool,omitempty"`
	ToolArgs         map[string]any `json:"tool_args,omitempty"`
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
}
