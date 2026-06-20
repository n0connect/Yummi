package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentPersona is the document-driven, model-facing persona for the autonomous
// loop. All prose the model reads (identity, planning guidance) lives in the
// persona document — never hard-coded in Go. The code only assembles it with
// the runtime protocol contract.
type AgentPersona struct {
	Identity   string `yaml:"identity"`
	Principles string `yaml:"principles"`
	Planning   string `yaml:"planning"`
}

// LoadAgentPersona reads agent.yaml from the persona directory. The identity is
// required; an empty or missing document is an error so the agent never falls
// back to silently hard-coded behavior.
func LoadAgentPersona(dir string) (AgentPersona, error) {
	path := filepath.Join(dir, "agent.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		return AgentPersona{}, fmt.Errorf("load agent persona %s: %w", path, err)
	}
	var p AgentPersona
	if err := yaml.Unmarshal(b, &p); err != nil {
		return AgentPersona{}, fmt.Errorf("parse agent persona %s: %w", path, err)
	}
	if strings.TrimSpace(p.Identity) == "" {
		return AgentPersona{}, errors.New("agent persona: identity is empty")
	}
	return p, nil
}
