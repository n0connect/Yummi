package agent

import "testing"

func TestEvaluateTaskQualityRejectsCosmeticCompletion(t *testing.T) {
	task := Task{
		Status: StatusCompleted, Summary: "done", Findings: []string{"unsupported claim"},
		Plan:       []PlanStep{{ID: "s1", Status: StepFailed}},
		Checkpoint: Checkpoint{ExecutedCommands: []string{"nmap 127.0.0.1", "nmap 127.0.0.1"}},
	}
	report := EvaluateTaskQuality(task, QualitySpec{
		RequireCompleted: true, RequireSummary: true, MinSuccessfulSteps: 1,
		MaxFailedSteps: 0, MaxDuplicateCommands: 0, MaxUnverifiedFindings: 0,
	})
	if report.Passed || report.DuplicateCommands != 1 || report.UnverifiedFindings != 1 {
		t.Fatalf("cosmetic completion passed quality gate: %+v", report)
	}
}

func TestEvaluateTaskQualityAcceptsGroundedCompletion(t *testing.T) {
	task := Task{
		Status: StatusCompleted, Summary: "verified", Findings: []string{"port open"},
		Plan:           []PlanStep{{ID: "s1", Status: StepDone, Command: "nmap 127.0.0.1"}},
		FindingRecords: []FindingRecord{{Title: "port open", Verdict: VerdictVerified}},
	}
	report := EvaluateTaskQuality(task, QualitySpec{
		RequireCompleted: true, RequireSummary: true, MinSuccessfulSteps: 1,
		MaxFailedSteps: 0, MaxDuplicateCommands: 0, MaxUnverifiedFindings: 0,
	})
	if !report.Passed {
		t.Fatalf("grounded completion failed quality gate: %+v", report)
	}
}

func TestEvaluateTaskQualityEnforcesScenarioSemantics(t *testing.T) {
	task := Task{
		Status:     StatusCompleted,
		Summary:    "Unable to complete the file search.\n\nEVIDENCE DIGEST (server-recorded):",
		Plan:       []PlanStep{{ID: "s1", Status: StepDone, Command: "du -s /var"}},
		Checkpoint: Checkpoint{ExecutedCommands: []string{"du -s /var"}},
	}
	report := EvaluateTaskQuality(task, QualitySpec{
		RequireCompleted: true, RequireSummary: true, MinObservedSteps: 1,
		RequireEvidenceDigest: true,
		RequiredCommandGroups: [][]string{{"find "}},
		ForbiddenSummaryTerms: []string{"unable to complete"},
	})
	if report.Passed || len(report.Failures) != 2 {
		t.Fatalf("scenario semantics were not enforced: %+v", report)
	}
}
