package agent

import (
	"fmt"
	"strings"
)

// FindingVerdict is the confidence state of one structured finding. The
// model can never assert Verified directly — only verifyFindingDetail
// computes it, by checking the finding's claimed Evidence against the
// actual, real output of the plan step it cites. Mirrors the industry
// pattern of a "verified" finding requiring a named, machine-checkable
// oracle rather than an LLM's own narrative (see docs/architecture.md's
// finding-verification section). Named FindingVerdict, not Verdict, because
// gate.go already uses Verdict for the unrelated operator-approval decision.
type FindingVerdict string

const (
	// VerdictCandidate is the honest default for anything not positively
	// confirmed: no evidence/step cited, a cited step the plan doesn't have,
	// OR a step that DID complete but whose recorded output does not contain
	// the claimed evidence verbatim. That last case is deliberately an
	// abstain, not a red flag — a substring-match miss only proves the
	// checker's simple oracle couldn't confirm the exact wording (the model
	// may have paraphrased, or synthesized a fact from more than one line of
	// output); it does not prove the claim is false. Collapsing it into a
	// stronger "wrong" verdict would be a confidence the check hasn't earned.
	VerdictCandidate FindingVerdict = "candidate"
	// VerdictVerified means the claimed Evidence is present, verbatim
	// (case-insensitive), in the real captured output of the plan step the
	// finding names, and that step actually completed successfully.
	VerdictVerified FindingVerdict = "verified"
	// VerdictUnsupported is reserved for the one case that is a genuine,
	// structural problem with the citation itself, not a soft matching miss:
	// the finding names a step that failed or never completed. If the tool
	// never produced output, nothing it "found" has a real source — that's
	// stronger than "couldn't confirm the wording."
	VerdictUnsupported FindingVerdict = "unsupported"
)

// OracleKind names the mechanism that earned a Verified verdict. A finding
// can only become Verified by naming one of these — never by LLM assertion.
type OracleKind string

const (
	// OracleToolEvidence: the claimed Evidence is a literal substring of the
	// real, stored output of the PlanStep the finding cites. This is
	// evidence-grounding, not exploit-reproduction — Yummi does not (yet)
	// re-run an exploit to prove code execution; it only proves the model's
	// claim is actually traceable to real tool output rather than invented.
	// Future oracle kinds (active replay, OAST callback, etc.) get their own
	// value here if Yummi grows that capability.
	OracleToolEvidence OracleKind = "tool_evidence"
)

// FindingRecord is the persisted, structured form of one finding: the
// model's claim plus the server-computed verdict. Task.Findings stays the
// flat, backward-compatible display list; FindingRecords is additive and may
// be shorter than Findings (only FindingsDetail entries produce a record —
// plain-text-only findings have nothing to verify against).
type FindingRecord struct {
	Title      string         `json:"title"`
	Verdict    FindingVerdict `json:"verdict"`
	OracleKind OracleKind     `json:"oracle_kind,omitempty"`
	Evidence   string         `json:"evidence,omitempty"`
	StepID     string         `json:"step_id,omitempty"`
	// Reason explains a Candidate/Unsupported verdict (empty when Verified —
	// the evidence match speaks for itself).
	Reason string `json:"reason,omitempty"`
}

// buildFindingRecords verifies every detail entry that carries a title or
// description (mirrors recordTaskFindings' own inclusion rule) and returns
// one FindingRecord each. currentStepID/liveContent describe the
// just-completed command, when there is one (empty at FINISH time, which
// isn't tied to a single step) — see verifyFindingDetail.
func buildFindingRecords(details []FindingDetail, currentStepID, liveContent string, plan []PlanStep) []FindingRecord {
	out := make([]FindingRecord, 0, len(details))
	for _, d := range details {
		if strings.TrimSpace(d.Title) == "" && strings.TrimSpace(d.Description) == "" {
			continue
		}
		out = append(out, verifyFindingDetail(d, currentStepID, liveContent, plan))
	}
	return out
}

// verifyFindingDetail computes a FindingRecord's verdict from real task data.
//
// Two verification paths, in preference order:
//  1. The finding's (possibly defaulted) StepID matches currentStepID and
//     liveContent is available — the untruncated output of the command that
//     JUST ran. Preferred because PlanStep.Result is capped for storage and
//     can clip evidence a model legitimately quotes from later in a long
//     output.
//  2. Otherwise, the cited step is looked up in plan and its (possibly
//     truncated) stored Result is used instead — the only option for a
//     finding attached to FINISH, which isn't tied to one just-run step.
//
// A substring miss against an otherwise-successful step's output is
// Candidate, not Unsupported — the checker couldn't confirm it, which is not
// the same as disproving it. Only a step that itself failed/never completed
// earns the stronger Unsupported verdict.
func verifyFindingDetail(d FindingDetail, currentStepID, liveContent string, plan []PlanStep) FindingRecord {
	title := sanitizeAgentText(d.Title)
	if title == "" {
		title = sanitizeAgentText(d.Description)
	}
	evidence := sanitizeAgentText(d.Evidence)
	stepID := strings.TrimSpace(d.StepID)
	if stepID == "" {
		stepID = currentStepID
	}
	rec := FindingRecord{Title: title, Evidence: evidence, StepID: stepID, Verdict: VerdictCandidate}

	if evidence == "" || stepID == "" {
		rec.Reason = "no evidence/step cited for this finding"
		return rec
	}

	if stepID == currentStepID && liveContent != "" {
		if containsFold(liveContent, evidence) {
			rec.Verdict = VerdictVerified
			rec.OracleKind = OracleToolEvidence
			return rec
		}
		rec.Reason = "could not confirm — claimed evidence not found verbatim in this step's actual output"
		return rec
	}

	step, ok := findPlanStep(plan, stepID)
	if !ok {
		rec.Reason = fmt.Sprintf("cited step %q is not in the plan", stepID)
		return rec
	}
	if step.Status != StepDone {
		rec.Verdict = VerdictUnsupported
		rec.Reason = fmt.Sprintf("cited step %q never completed successfully (status: %s)", stepID, step.Status)
		return rec
	}
	if !containsFold(step.Result, evidence) {
		rec.Reason = fmt.Sprintf("could not confirm — claimed evidence not found verbatim in step %q's recorded output", stepID)
		return rec
	}
	rec.Verdict = VerdictVerified
	rec.OracleKind = OracleToolEvidence
	return rec
}

func containsFold(haystack, needle string) bool {
	return needle != "" && strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func findPlanStep(plan []PlanStep, id string) (PlanStep, bool) {
	for _, s := range plan {
		if s.ID == id {
			return s, true
		}
	}
	return PlanStep{}, false
}
