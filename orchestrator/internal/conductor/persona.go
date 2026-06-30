package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type coreIdentityFile struct {
	Version   string   `yaml:"version"`
	Identity  string   `yaml:"identity"`
	Values    []string `yaml:"values"`
	Character struct {
		Role        string `yaml:"role"`
		Disposition struct {
			OperatorRelationship string `yaml:"operator_relationship"`
			Honesty              string `yaml:"honesty"`
			Economy              string `yaml:"economy"`
			LanguageParity       string `yaml:"language_parity"`
		} `yaml:"disposition"`
	} `yaml:"character"`
}

type safetyFile struct {
	Boundaries []string `yaml:"boundaries"`
}

type methodologyFile struct {
	Steps []string `yaml:"steps"`
}

// LoadPersona loads Yummi persona files from a directory.
func LoadPersona(dir string) (Persona, error) {
	if dir == "" {
		return Persona{}, fmt.Errorf("persona dir is empty")
	}

	core, err := readYAML[coreIdentityFile](filepath.Join(dir, "core_identity.yaml"))
	if err != nil {
		return Persona{}, err
	}
	safety, err := readYAML[safetyFile](filepath.Join(dir, "safety_boundaries.yaml"))
	if err != nil {
		return Persona{}, err
	}
	methodology, err := readYAML[methodologyFile](filepath.Join(dir, "methodology.yaml"))
	if err != nil {
		return Persona{}, err
	}

	schemaBytes, err := os.ReadFile(filepath.Join(dir, "output_schema.json"))
	if err != nil {
		return Persona{}, fmt.Errorf("read output schema: %w", err)
	}

	return Persona{
		Version:      core.Version,
		Identity:     core.Identity,
		Values:       core.Values,
		Boundaries:   safety.Boundaries,
		Methodology:  methodology.Steps,
		OutputSchema: string(schemaBytes),
		Role:         strings.TrimSpace(core.Character.Role),
		Disposition: PersonaDisposition{
			OperatorRelationship: strings.TrimSpace(core.Character.Disposition.OperatorRelationship),
			Honesty:              strings.TrimSpace(core.Character.Disposition.Honesty),
			Economy:              strings.TrimSpace(core.Character.Disposition.Economy),
			LanguageParity:       strings.TrimSpace(core.Character.Disposition.LanguageParity),
		},
	}, nil
}

func readYAML[T any](path string) (T, error) {
	var out T
	buf, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(buf, &out); err != nil {
		return out, fmt.Errorf("parse %s: %w", path, err)
	}
	return out, nil
}
