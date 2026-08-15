package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SubAgentDef is a declarative specialist definition loaded from
// persona/<v>/subagents/*.yaml. Each becomes a sub-agent Runner: the same graph
// state machine, specialized by this persona and scoped to these skills, and
// (in wiring) sharing the orchestrator's Store + Knowledge.
type SubAgentDef struct {
	Role       string   `yaml:"role"`
	Identity   string   `yaml:"identity"`
	Principles string   `yaml:"principles"`
	Planning   string   `yaml:"planning"`
	Skills     []string `yaml:"skills"`
	// RoutingHint is a short, one-sentence description of when the top-level
	// agent should hand a subtask to this role. Shown to the model alongside
	// the role name in the DELEGATION prompt block (unlike Identity/Principles/
	// Planning, which shape the sub-agent's own behavior once it's running) —
	// so this is written from the delegator's point of view, not the
	// specialist's. Optional: a role without one still appears in the
	// delegation list, just as a bare name.
	RoutingHint string `yaml:"routing_hint"`
}

// Persona returns the model-facing persona for this sub-agent role.
func (d SubAgentDef) Persona() AgentPersona {
	return AgentPersona{Identity: d.Identity, Principles: d.Principles, Planning: d.Planning}
}

// LoadSubAgentDefs reads every *.yaml in dir as one sub-agent definition. A file
// that fails to parse or lacks role/identity is skipped with a warning
// (degraded, never fatal) — matching the persona/skill loading convention. A
// missing directory yields an empty set (delegation simply disabled).
func LoadSubAgentDefs(dir string) ([]SubAgentDef, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("subagents: read dir %s: %w", dir, err)
	}
	var defs []SubAgentDef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			slog.Warn("subagents: read failed, skipping", "file", path, "error", rerr)
			continue
		}
		var d SubAgentDef
		if uerr := yaml.Unmarshal(b, &d); uerr != nil {
			slog.Warn("subagents: parse failed, skipping", "file", path, "error", uerr)
			continue
		}
		if strings.TrimSpace(d.Role) == "" || strings.TrimSpace(d.Identity) == "" {
			slog.Warn("subagents: missing role or identity, skipping", "file", path)
			continue
		}
		defs = append(defs, d)
	}
	return defs, nil
}
