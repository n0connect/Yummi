package agent

import (
	"fmt"
	"strings"
)

// QualitySpec defines objective, model-independent acceptance thresholds for
// one agent scenario. It is used by both offline integration tests and the
// opt-in real-model evaluation suite.
type QualitySpec struct {
	RequireCompleted      bool
	RequireSummary        bool
	MinSuccessfulSteps    int
	MinObservedSteps      int
	MaxFailedSteps        int
	MaxDuplicateCommands  int
	MaxUnverifiedFindings int
	RequireEvidenceDigest bool
	RequiredCommandGroups [][]string
	ForbiddenSummaryTerms []string
}

type QualityReport struct {
	Passed             bool     `json:"passed"`
	SuccessfulSteps    int      `json:"successful_steps"`
	FailedSteps        int      `json:"failed_steps"`
	ObservedSteps      int      `json:"observed_steps"`
	DuplicateCommands  int      `json:"duplicate_commands"`
	UnverifiedFindings int      `json:"unverified_findings"`
	Failures           []string `json:"failures,omitempty"`
}

// EvaluateTaskQuality turns task state into assertion-ready metrics. It never
// asks the model to grade itself.
func EvaluateTaskQuality(task Task, spec QualitySpec) QualityReport {
	report := QualityReport{}
	for _, step := range task.Plan {
		switch step.Status {
		case StepDone:
			report.SuccessfulSteps++
		case StepFailed:
			report.FailedSteps++
		}
	}
	report.ObservedSteps = report.SuccessfulSteps + report.FailedSteps
	seen := make(map[string]struct{})
	commands := task.Checkpoint.ExecutedCommands
	if len(commands) == 0 {
		for _, step := range task.Plan {
			if step.Command != "" {
				commands = append(commands, step.Command)
			}
		}
	}
	for _, command := range commands {
		normalized := strings.Join(strings.Fields(command), " ")
		if _, ok := seen[normalized]; ok {
			report.DuplicateCommands++
		} else if normalized != "" {
			seen[normalized] = struct{}{}
		}
	}
	commandCorpus := strings.ToLower(strings.Join(commands, "\n"))
	for _, finding := range task.FindingRecords {
		if finding.Verdict != VerdictVerified {
			report.UnverifiedFindings++
		}
	}
	// Free-text findings have no evidence-bearing verdict record.
	if len(task.Findings) > len(task.FindingRecords) {
		report.UnverifiedFindings += len(task.Findings) - len(task.FindingRecords)
	}

	if spec.RequireCompleted && task.Status != StatusCompleted {
		report.Failures = append(report.Failures, fmt.Sprintf("status=%s, want completed", task.Status))
	}
	if spec.RequireSummary && strings.TrimSpace(task.Summary) == "" {
		report.Failures = append(report.Failures, "summary is empty")
	}
	if report.SuccessfulSteps < spec.MinSuccessfulSteps {
		report.Failures = append(report.Failures, fmt.Sprintf("successful_steps=%d, want >=%d", report.SuccessfulSteps, spec.MinSuccessfulSteps))
	}
	if report.ObservedSteps < spec.MinObservedSteps {
		report.Failures = append(report.Failures, fmt.Sprintf("observed_steps=%d, want >=%d", report.ObservedSteps, spec.MinObservedSteps))
	}
	if report.FailedSteps > spec.MaxFailedSteps {
		report.Failures = append(report.Failures, fmt.Sprintf("failed_steps=%d, want <=%d", report.FailedSteps, spec.MaxFailedSteps))
	}
	if report.DuplicateCommands > spec.MaxDuplicateCommands {
		report.Failures = append(report.Failures, fmt.Sprintf("duplicate_commands=%d, want <=%d", report.DuplicateCommands, spec.MaxDuplicateCommands))
	}
	if report.UnverifiedFindings > spec.MaxUnverifiedFindings {
		report.Failures = append(report.Failures, fmt.Sprintf("unverified_findings=%d, want <=%d", report.UnverifiedFindings, spec.MaxUnverifiedFindings))
	}
	if spec.RequireEvidenceDigest && !strings.Contains(task.Summary, "EVIDENCE DIGEST (server-recorded):") {
		report.Failures = append(report.Failures, "server-recorded evidence digest is missing")
	}
	for _, group := range spec.RequiredCommandGroups {
		matched := false
		for _, term := range group {
			if strings.Contains(commandCorpus, strings.ToLower(term)) {
				matched = true
				break
			}
		}
		if !matched {
			report.Failures = append(report.Failures, fmt.Sprintf("no executed command matched required alternatives %v", group))
		}
	}
	lowerSummary := strings.ToLower(task.Summary)
	for _, term := range spec.ForbiddenSummaryTerms {
		if strings.Contains(lowerSummary, strings.ToLower(term)) {
			report.Failures = append(report.Failures, fmt.Sprintf("summary contains failure marker %q", term))
		}
	}
	report.Passed = len(report.Failures) == 0
	return report
}
