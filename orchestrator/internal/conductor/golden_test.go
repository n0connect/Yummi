package conductor

import "testing"

func TestPersonaGoldenFixtures(t *testing.T) {
	report, err := EvaluateGoldenFixtures("../../../persona/v0.1/golden_tests")
	if err != nil {
		t.Fatalf("EvaluateGoldenFixtures: %v", err)
	}
	if report.Total == 0 || report.Passed != report.Total {
		t.Fatalf("persona golden regression: %+v", report)
	}
}

func TestBoundaryDecisionDoesNotTreatEducationAsExecution(t *testing.T) {
	for _, input := range []string{"What is a network scan?", "Güvenlik açığı nedir?", "How does brute force detection work?"} {
		if decision, handled := BoundaryDecision(input); handled {
			t.Errorf("educational query %q was intercepted: %+v", input, decision)
		}
	}
}
