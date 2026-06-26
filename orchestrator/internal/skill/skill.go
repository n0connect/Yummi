// Package skill provides declarative, pluggable capability modules ("skills")
// that make the brain smarter about which sandbox commands to run for a class
// of target. A skill is KNOWLEDGE ONLY — it introduces no execution path; every
// command the agent runs still flows through the single fail-closed sandbox
// exec path. Skills are loaded from YAML so new ones are added without code
// changes. It depends only on stdlib, yaml, and the leaf security/threatscan
// package (skill content is rendered verbatim into the prompt, so it is scanned
// for injection at load) — no agent/conductor deps, so it stays import-safe.
package skill

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
	"gopkg.in/yaml.v3"
)

// Skill is one declarative playbook.
type Skill struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Triggers    []string `yaml:"triggers" json:"triggers"` // lowercase keywords matched against goal + tools
	Tools       []string `yaml:"tools" json:"tools"`       // recommended sandbox tools (informational)
	Knowledge   string   `yaml:"knowledge" json:"knowledge"`
}

// Registry holds the loaded skill set.
type Registry struct {
	skills []Skill
}

// New builds a registry from an explicit skill list (used by tests and by
// sub-agent scoping).
func New(skills []Skill) *Registry {
	return &Registry{skills: skills}
}

// LoadDir loads every *.yaml file in dir as one skill. A file that fails to
// parse or has no name is skipped with a warning (degraded, never fatal) —
// consistent with the persona-loading convention.
func LoadDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skill: read dir %s: %w", dir, err)
	}
	var skills []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			slog.Warn("skill: read failed, skipping", "file", path, "error", rerr)
			continue
		}
		var s Skill
		if uerr := yaml.Unmarshal(b, &s); uerr != nil {
			slog.Warn("skill: parse failed, skipping", "file", path, "error", uerr)
			continue
		}
		if strings.TrimSpace(s.Name) == "" {
			slog.Warn("skill: missing name, skipping", "file", path)
			continue
		}
		// Content-layer fail-closed: a skill's knowledge/description is rendered
		// verbatim into the step prompt (Render), so a skill file is an injection
		// vector if it ever comes from an untrusted source. Scan at strict scope
		// and skip (don't load) any skill whose text trips a pattern — a skill is
		// user-curated, so blocking with a warning is the safe, reviewable choice.
		if msg := threatscan.FirstThreatMessage(s.Description+"\n"+s.Knowledge, threatscan.ScopeStrict); msg != "" {
			slog.Warn("skill: threat pattern in skill content, skipping", "file", path, "reason", msg)
			continue
		}
		skills = append(skills, s)
	}
	return &Registry{skills: skills}, nil
}

// All returns a copy of every loaded skill (for the read API / TRACE view).
func (r *Registry) All() []Skill {
	if r == nil {
		return []Skill{}
	}
	return append([]Skill{}, r.skills...)
}

// Names returns the names of all loaded skills (stable order).
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.skills))
	for i, s := range r.skills {
		out[i] = s.Name
	}
	return out
}

// Subset returns a new registry containing only the named skills (used to scope
// a sub-agent to its role's skills). Unknown names are ignored.
func (r *Registry) Subset(names []string) *Registry {
	if r == nil {
		return New(nil)
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []Skill
	for _, s := range r.skills {
		if want[s.Name] {
			out = append(out, s)
		}
	}
	return New(out)
}

// Select returns up to max skills relevant to the goal and the given tools,
// scored by how many of each skill's triggers appear (case-insensitive
// substring) in "goal + tools". Deterministic: score desc, then name asc.
// max <= 0 defaults to 3.
func (r *Registry) Select(goal string, tools []string, max int) []Skill {
	if r == nil || len(r.skills) == 0 {
		return nil
	}
	if max <= 0 {
		max = 3
	}
	hay := strings.ToLower(goal + " " + strings.Join(tools, " "))

	type scored struct {
		s     Skill
		score int
	}
	var matches []scored
	for _, s := range r.skills {
		score := 0
		for _, tr := range s.Triggers {
			tr = strings.ToLower(strings.TrimSpace(tr))
			if tr != "" && strings.Contains(hay, tr) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, scored{s, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].s.Name < matches[j].s.Name
	})
	if len(matches) > max {
		matches = matches[:max]
	}
	out := make([]Skill, len(matches))
	for i, m := range matches {
		out[i] = m.s
	}
	return out
}

// Render formats selected skills as a compact prompt block. Empty input yields
// an empty string (no header).
func (r *Registry) Render(selected []Skill) string {
	if len(selected) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("SKILLS (relevant playbooks — apply the matching approach):\n")
	for _, s := range selected {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, strings.TrimSpace(s.Knowledge))
	}
	return b.String()
}
