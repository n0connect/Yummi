package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}

func TestLoadDirAndSelect(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "web.yaml", `name: web
description: web service assessment
triggers: [web, http, nginx]
tools: [whatweb, gobuster]
knowledge: |
  Fingerprint first with whatweb, then gobuster for content discovery.`)
	writeSkill(t, dir, "recon.yaml", `name: recon
description: host enumeration
triggers: [recon, enumerate, ports, nmap]
tools: [nmap]
knowledge: |
  Start with nmap -sV for service/version detection.`)
	// A malformed file must be skipped, not fatal.
	writeSkill(t, dir, "broken.yaml", "name: [this is not valid: yaml: {")

	reg, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := len(reg.Names()); got != 2 {
		t.Fatalf("want 2 loaded skills (broken skipped), got %d: %v", got, reg.Names())
	}

	// Goal mentions a web target → web skill ranks; nmap tool in recent tools → recon too.
	sel := reg.Select("assess the nginx web server", []string{"nmap"}, 3)
	if len(sel) != 2 {
		t.Fatalf("want both skills selected, got %d", len(sel))
	}
	// web has more trigger hits (web + nginx) than recon (nmap) → web first.
	if sel[0].Name != "web" {
		t.Fatalf("want web ranked first by score, got %q", sel[0].Name)
	}

	rendered := reg.Render(sel)
	if !strings.Contains(rendered, "SKILLS") || !strings.Contains(rendered, "whatweb") {
		t.Fatalf("render missing content:\n%s", rendered)
	}
}

func TestSelectNoMatchAndNilSafety(t *testing.T) {
	reg := New([]Skill{{Name: "web", Triggers: []string{"http"}, Knowledge: "k"}})
	if got := reg.Select("reverse engineer a binary", nil, 3); len(got) != 0 {
		t.Fatalf("want no match, got %v", got)
	}
	if r := reg.Render(nil); r != "" {
		t.Fatalf("empty render should be blank, got %q", r)
	}
	// Nil registry must not panic.
	var nilReg *Registry
	_ = nilReg.Select("x", nil, 3)
	_ = nilReg.Names()
	_ = nilReg.Subset([]string{"web"})
}

func TestSubsetScopesSkills(t *testing.T) {
	reg := New([]Skill{
		{Name: "web", Triggers: []string{"http"}, Knowledge: "w"},
		{Name: "recon", Triggers: []string{"nmap"}, Knowledge: "r"},
		{Name: "source", Triggers: []string{"code"}, Knowledge: "s"},
	})
	sub := reg.Subset([]string{"web", "unknown"})
	if got := sub.Names(); len(got) != 1 || got[0] != "web" {
		t.Fatalf("subset scoping wrong: %v", got)
	}
}
