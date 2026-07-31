package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

type GoldenFixture struct {
	Name           string `yaml:"name" json:"name"`
	Input          string `yaml:"input" json:"input"`
	ExpectedIntent string `yaml:"expected_intent" json:"expected_intent"`
}

type GoldenFailure struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type GoldenReport struct {
	Total    int             `json:"total"`
	Passed   int             `json:"passed"`
	Failures []GoldenFailure `json:"failures,omitempty"`
}

func LoadGoldenFixtures(dir string) ([]GoldenFixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read golden fixtures: %w", err)
	}
	var fixtures []GoldenFixture
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var fixture GoldenFixture
		if err := yaml.Unmarshal(body, &fixture); err != nil {
			return nil, fmt.Errorf("parse golden fixture %s: %w", entry.Name(), err)
		}
		if fixture.Name == "" || fixture.Input == "" || fixture.ExpectedIntent == "" {
			return nil, fmt.Errorf("golden fixture %s is incomplete", entry.Name())
		}
		fixtures = append(fixtures, fixture)
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Name < fixtures[j].Name })
	return fixtures, nil
}

// EvaluateGoldenFixtures executes deterministic boundary policy against the
// checked-in persona contract. A fixture not covered by deterministic policy
// is reported as llm_required rather than silently passing.
func EvaluateGoldenFixtures(dir string) (GoldenReport, error) {
	fixtures, err := LoadGoldenFixtures(dir)
	if err != nil {
		return GoldenReport{}, err
	}
	report := GoldenReport{Total: len(fixtures)}
	for _, fixture := range fixtures {
		decision, ok := BoundaryDecision(fixture.Input)
		actual := "llm_required"
		if ok {
			actual = decision.Intent
		}
		if actual == fixture.ExpectedIntent {
			report.Passed++
		} else {
			report.Failures = append(report.Failures, GoldenFailure{Name: fixture.Name, Expected: fixture.ExpectedIntent, Actual: actual})
		}
	}
	return report, nil
}
