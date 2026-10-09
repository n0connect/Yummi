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
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
	"gopkg.in/yaml.v3"
)

// Skill is one declarative playbook.
type Skill struct {
	SchemaVersion int        `yaml:"schema_version,omitempty" json:"schema_version"`
	Version       string     `yaml:"version,omitempty" json:"version"`
	Name          string     `yaml:"name" json:"name"`
	Description   string     `yaml:"description" json:"description"`
	Triggers      []string   `yaml:"triggers" json:"triggers"` // lowercase keywords matched against goal + tools
	Tools         []string   `yaml:"tools" json:"tools"`       // recommended sandbox tools (informational)
	Knowledge     string     `yaml:"knowledge" json:"knowledge"`
	References    []string   `yaml:"references,omitempty" json:"references,omitempty"`
	PlanStepLimit int        `yaml:"plan_step_limit,omitempty" json:"plan_step_limit,omitempty"`
	Provenance    Provenance `yaml:"provenance,omitempty" json:"provenance"`
	Digest        string     `yaml:"-" json:"digest"`
}

// Provenance identifies who supplied a skill and where the reviewed source
// lives. It is metadata only and never grants execution authority.
type Provenance struct {
	Origin string `yaml:"origin,omitempty" json:"origin"` // builtin | operator | auto
	Source string `yaml:"source,omitempty" json:"source,omitempty"`
}

var (
	skillNamePattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)
	skillVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)
)

// Normalize fills backward-compatible metadata for legacy v0.1 YAML files.
func Normalize(s Skill) Skill {
	s.Name = strings.TrimSpace(s.Name)
	if s.SchemaVersion == 0 {
		s.SchemaVersion = 1
	}
	if strings.TrimSpace(s.Version) == "" {
		s.Version = "1.0.0"
	}
	if strings.TrimSpace(s.Provenance.Origin) == "" {
		s.Provenance.Origin = "builtin"
	}
	s.Digest = digest(s)
	return s
}

// Validate enforces the stable declarative skill contract before a skill can
// enter the live registry. Skills remain knowledge-only: Tools are advisory
// and every actual command still uses the sandbox execution authority.
func Validate(s Skill) error {
	if err := ValidateSchema(s); err != nil {
		return err
	}
	s = Normalize(s)
	if msg := threatscan.FirstThreatMessage(s.Name+"\n"+s.Description+"\n"+s.Knowledge, threatscan.ScopeStrict); msg != "" {
		return fmt.Errorf("skill %s failed threat scan: %s", s.Name, msg)
	}
	return nil
}

// ValidateSchema checks shape/size/version separately from content security.
// The curator uses this split so a structurally-valid but hostile LLM draft
// can still be persisted as an audited rejected proposal.
func ValidateSchema(s Skill) error {
	s = Normalize(s)
	if s.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d", s.SchemaVersion)
	}
	if !skillNamePattern.MatchString(s.Name) {
		return fmt.Errorf("invalid skill name %q", s.Name)
	}
	if !skillVersionPattern.MatchString(s.Version) {
		return fmt.Errorf("invalid semantic version %q", s.Version)
	}
	if len(s.Triggers) == 0 {
		return fmt.Errorf("skill %s has no triggers", s.Name)
	}
	if len(s.Triggers) > 32 || len(s.Tools) > 32 || len(s.References) > 32 {
		return fmt.Errorf("skill %s exceeds list limits", s.Name)
	}
	if len(s.Description) > 1000 || len(s.Knowledge) > 16000 {
		return fmt.Errorf("skill %s exceeds content limits", s.Name)
	}
	if s.PlanStepLimit < 0 || s.PlanStepLimit > 20 {
		return fmt.Errorf("skill %s has invalid plan_step_limit %d", s.Name, s.PlanStepLimit)
	}
	return nil
}

func digest(s Skill) string {
	payload := strings.Join([]string{
		fmt.Sprint(s.SchemaVersion),
		s.Name,
		s.Version,
		s.Description,
		strings.Join(s.Triggers, "\x00"),
		strings.Join(s.Tools, "\x00"),
		s.Knowledge,
		strings.Join(s.References, "\x00"),
		fmt.Sprint(s.PlanStepLimit),
	}, "\x01")
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(payload)))
}

// Registry holds the loaded skill set. Safe for concurrent use — Add can be
// called from a REST approval handler while agent steps concurrently read
// via Select/Names/All/Subset.
type Registry struct {
	mu     sync.RWMutex
	skills []Skill
}

// New builds a registry from an explicit skill list (used by tests and by
// sub-agent scoping).
func New(skills []Skill) *Registry {
	r := &Registry{}
	for _, s := range skills {
		s = Normalize(s)
		replaced := false
		for i := range r.skills {
			if r.skills[i].Name == s.Name {
				r.skills[i] = s
				replaced = true
				break
			}
		}
		if !replaced {
			r.skills = append(r.skills, s)
		}
	}
	return r
}

// LoadDir loads every *.yaml file in dir as one skill. A file that fails to
// parse or has no name is skipped with a warning (degraded, never fatal) —
// consistent with the persona-loading convention.
func LoadDir(dir string) (*Registry, error) {
	return LoadDirWithOrigin(dir, "builtin")
}

// LoadDirWithOrigin loads a directory while preserving provenance declared in
// each artifact. origin is only used for legacy YAML without provenance, which
// lets shipped and operator-approved directories retain distinct trust labels.
func LoadDirWithOrigin(dir, origin string) (*Registry, error) {
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
		if strings.TrimSpace(s.Provenance.Origin) == "" {
			s.Provenance.Origin = origin
		}
		if strings.TrimSpace(s.Provenance.Source) == "" {
			s.Provenance.Source = path
		}
		s = Normalize(s)
		if err := Validate(s); err != nil {
			slog.Warn("skill: validation failed, skipping", "file", path, "error", err)
			continue
		}
		// Content-layer fail-closed: a skill's knowledge/description is rendered
		// verbatim into the step prompt (Render), so a skill file is an injection
		// vector if it ever comes from an untrusted source. Scan at strict scope
		// and skip (don't load) any skill whose text trips a pattern — a skill is
		// user-curated, so blocking with a warning is the safe, reviewable choice.
		if msg := threatscan.FirstThreatMessage(s.Name+"\n"+s.Description+"\n"+s.Knowledge, threatscan.ScopeStrict); msg != "" {
			slog.Warn("skill: threat pattern in skill content, skipping", "file", path, "reason", msg)
			continue
		}
		duplicate := false
		for _, existing := range skills {
			if existing.Name == s.Name {
				duplicate = true
				break
			}
		}
		if duplicate {
			slog.Warn("skill: duplicate name, skipping", "file", path, "name", s.Name)
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Skill{}, r.skills...)
}

// Add appends a skill to the live registry, making it immediately selectable
// — used when an operator approves a curator-drafted skill (skillcurator),
// so the newly-approved skill doesn't require an orchestrator restart to
// take effect. Caller is responsible for any content-layer scanning before
// calling Add; this method does not re-scan.
func (r *Registry) Add(s Skill) *Registry {
	if r == nil {
		return r
	}
	if err := r.Upsert(s); err != nil {
		slog.Warn("skill: add rejected", "name", s.Name, "error", err)
	}
	return r
}

// Upsert validates and atomically activates one version of a named skill.
// Re-approval replaces the previous version instead of accumulating duplicate
// prompt blocks in a long-running process.
func (r *Registry) Upsert(s Skill) error {
	if r == nil {
		return fmt.Errorf("skill registry unavailable")
	}
	s = Normalize(s)
	if err := Validate(s); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.skills {
		if r.skills[i].Name == s.Name {
			r.skills[i] = s
			return nil
		}
	}
	r.skills = append(r.skills, s)
	return nil
}

// Names returns the names of all loaded skills (stable order).
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	if r == nil {
		return nil
	}
	r.mu.RLock()
	skills := append([]Skill{}, r.skills...)
	r.mu.RUnlock()
	if len(skills) == 0 {
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
	for _, s := range skills {
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

// Lookup returns one skill by exact name. It backs the skill_view tool, so
// the model can load a full playbook on demand instead of carrying every
// playbook's body in every prompt.
func (r *Registry) Lookup(name string) (Skill, bool) {
	if r == nil {
		return Skill{}, false
	}
	name = strings.TrimSpace(name)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// indexSuggestedMax caps how many goal-matched skills get a one-line
// description in the index. Every other skill appears by name only.
const indexSuggestedMax = 5

// indexDescRunes bounds one suggested skill's description line.
const indexDescRunes = 160

// Index renders the SKILLS catalogue that every task prompt carries. It is the
// progressive-disclosure layer: the model always sees WHAT playbooks exist, and
// loads the full text of one with skill_view when its next step falls in that
// area. Goal-matched skills (Select) are listed first with a one-line
// description; every other skill is listed by name only, so the full catalogue
// stays cheap enough for a local model's context. budget bounds the whole block
// in bytes (<= 0 means DefaultIndexBudget); names that do not fit are reported
// as a count, never silently dropped. Empty when the registry has no skills.
func (r *Registry) Index(goal string, tools []string, budget int) string {
	if r == nil {
		return ""
	}
	all := r.All()
	if len(all) == 0 {
		return ""
	}
	if budget <= 0 {
		budget = DefaultIndexBudget
	}
	suggested := r.Select(goal, tools, indexSuggestedMax)
	isSuggested := make(map[string]bool, len(suggested))
	for _, s := range suggested {
		isSuggested[s.Name] = true
	}
	var b strings.Builder
	b.WriteString("SKILLS — playbooks you can load with skill_view(name). Before a technique that a playbook covers, load it and follow its method; do not improvise a method the playbook already gives.\n")
	if len(suggested) > 0 {
		b.WriteString("Suggested for this goal:\n")
		for _, s := range suggested {
			// A playbook without a description is listed by name alone. Falling
			// back to its knowledge text would leak the body into the index,
			// which must stay a catalogue, never a copy of the playbooks.
			if d := oneLine(s.Description, indexDescRunes); d != "" {
				fmt.Fprintf(&b, "  - %s: %s\n", s.Name, d)
			} else {
				fmt.Fprintf(&b, "  - %s\n", s.Name)
			}
		}
	}
	var others []string
	for _, s := range all {
		if !isSuggested[s.Name] {
			others = append(others, s.Name)
		}
	}
	sort.Strings(others)
	if len(others) > 0 {
		b.WriteString("Other playbooks: ")
		listed := 0
		for i, n := range others {
			piece := n
			if listed > 0 {
				piece = ", " + n
			}
			// Reserve room for an overflow note so the block stays within budget.
			remaining := len(others) - i - 1
			note := ""
			if remaining > 0 {
				note = fmt.Sprintf(", … (+%d more)", remaining)
			}
			if b.Len()+len(piece)+len(note) > budget {
				if listed > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "… (+%d more)", len(others)-listed)
				break
			}
			b.WriteString(piece)
			listed++
		}
		b.WriteString("\n")
	}
	return b.String()
}

// DefaultIndexBudget bounds the SKILLS index block in bytes. Roughly 900 tokens:
// enough for the full catalogue's names plus the goal's suggestions.
const DefaultIndexBudget = 3500

// RenderBody formats one full playbook for the skill_view tool result: its
// name, description, method text, and any reviewed plan constraint. The
// knowledge text is already bounded at load time (Validate's 16000-byte cap).
func RenderBody(s Skill) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SKILL %s — %s\n", s.Name, strings.TrimSpace(s.Description))
	b.WriteString(strings.TrimSpace(s.Knowledge))
	b.WriteString("\n")
	if s.PlanStepLimit > 0 {
		fmt.Fprintf(&b, "Reviewed plan constraint: at most %d step(s).\n", s.PlanStepLimit)
	}
	return b.String()
}

// oneLine collapses whitespace and cuts s to at most maxRunes runes, ending
// with an ellipsis when it had to cut. Rune-safe, so a multi-byte description
// is never split mid-character.
func oneLine(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes-1]) + "…"
}

// Render formats selected skills as a compact prompt block. Empty input yields
// an empty string (no header).
func (r *Registry) Render(selected []Skill) string {
	if len(selected) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("MATCHED PLAYBOOKS — apply their method to this goal:\n")
	for _, s := range selected {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, strings.TrimSpace(s.Knowledge))
		if s.PlanStepLimit > 0 {
			fmt.Fprintf(&b, "  Reviewed plan constraint: at most %d step(s).\n", s.PlanStepLimit)
		}
	}
	return b.String()
}
