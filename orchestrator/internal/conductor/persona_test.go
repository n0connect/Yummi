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

// Regression: core_identity.yaml's character.role/disposition block used to
// have no matching struct field, so yaml.Unmarshal silently dropped it —
// the persona file declared a "language_parity: ... no exceptions" rule that
// never reached the model, found live when an English message got a Turkish
// reply. LoadPersona must actually populate these fields.
func TestLoadPersonaCapturesCharacterBlock(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "core_identity.yaml"), `version: "0.1"
identity: "Yummi"
values:
  - privacy_first
character:
  role: "You are a precise assistant."
  disposition:
    operator_relationship: "Operator decides."
    honesty: "State uncertainty."
    economy: "Be concise."
    language_parity: "Match the operator's language. No exceptions."
`)
	mustWrite(t, filepath.Join(dir, "safety_boundaries.yaml"), "boundaries:\n  - \"safe\"\n")
	mustWrite(t, filepath.Join(dir, "methodology.yaml"), "steps:\n  - \"Recon\"\n")
	mustWrite(t, filepath.Join(dir, "output_schema.json"), "{}")

	persona, err := LoadPersona(dir)
	if err != nil {
		t.Fatalf("LoadPersona failed: %v", err)
	}
	if persona.Role != "You are a precise assistant." {
		t.Fatalf("character.role not loaded: %q", persona.Role)
	}
	if persona.Disposition.LanguageParity != "Match the operator's language. No exceptions." {
		t.Fatalf("character.disposition.language_parity not loaded: %q", persona.Disposition.LanguageParity)
	}
	if persona.Disposition.OperatorRelationship == "" || persona.Disposition.Honesty == "" || persona.Disposition.Economy == "" {
		t.Fatalf("disposition fields not fully loaded: %+v", persona.Disposition)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
