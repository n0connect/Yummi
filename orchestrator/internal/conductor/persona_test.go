package conductor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPersona(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "core_identity.yaml"), "version: \"0.1\"\nidentity: \"Yummi\"\nvalues:\n  - privacy_first\n")
	mustWrite(t, filepath.Join(dir, "safety_boundaries.yaml"), "boundaries:\n  - \"safe\"\n")
	mustWrite(t, filepath.Join(dir, "methodology.yaml"), "steps:\n  - \"Recon\"\n")
	mustWrite(t, filepath.Join(dir, "output_schema.json"), "{}")

	persona, err := LoadPersona(dir)
	if err != nil {
		t.Fatalf("LoadPersona failed: %v", err)
	}
	if persona.Version != "0.1" {
		t.Fatalf("unexpected version: %s", persona.Version)
	}
	if persona.Identity != "Yummi" {
		t.Fatalf("unexpected identity: %s", persona.Identity)
	}
	if len(persona.Values) == 0 {
		t.Fatal("expected at least one value")
	}
	if len(persona.Boundaries) == 0 {
		t.Fatal("expected at least one boundary")
	}
	if len(persona.Methodology) == 0 {
		t.Fatal("expected at least one methodology step")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
