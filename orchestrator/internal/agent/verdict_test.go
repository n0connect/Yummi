package agent

import "testing"

func TestVerifyFindingDetail(t *testing.T) {
	plan := []PlanStep{
		{ID: "s1", Status: StepDone, Result: "PORT   STATE SERVICE\n3306/tcp open mysql 5.5.47"},
		{ID: "s2", Status: StepFailed, Result: "", Error: "command aborted after running too long"},
	}

	cases := []struct {
		name           string
		detail         FindingDetail
		currentStepID  string
		liveContent    string
		wantVerdict    FindingVerdict
		wantOracleKind OracleKind
	}{
		{
			name:        "no evidence or step cited stays a candidate",
			detail:      FindingDetail{Title: "open port found"},
			wantVerdict: VerdictCandidate,
		},
		{
			name:        "evidence with no step id stays a candidate",
			detail:      FindingDetail{Title: "open port found", Evidence: "3306/tcp open"},
			wantVerdict: VerdictCandidate,
		},
		{
			name:           "live content match verifies against the just-run step",
			detail:         FindingDetail{Title: "MySQL exposed", Evidence: "3306/tcp open mysql 5.5.47", StepID: "s1"},
			currentStepID:  "s1",
			liveContent:    "PORT   STATE SERVICE\n3306/tcp open mysql 5.5.47\nNmap done.",
			wantVerdict:    VerdictVerified,
			wantOracleKind: OracleToolEvidence,
		},
		{
			// A substring miss only proves the checker couldn't confirm the
			// wording — it does not disprove the claim, so it stays a
			// candidate (abstain), not a red-flag "unsupported". Only a step
			// that itself failed earns Unsupported (see the case below).
			name:          "live content mismatch is a candidate, not unsupported",
			detail:        FindingDetail{Title: "MySQL exposed", Evidence: "8080/tcp open http", StepID: "s1"},
			currentStepID: "s1",
			liveContent:   "PORT   STATE SERVICE\n3306/tcp open mysql 5.5.47",
			wantVerdict:   VerdictCandidate,
		},
		{
			name:           "case-insensitive match against persisted step result",
			detail:         FindingDetail{Title: "MySQL exposed", Evidence: "3306/TCP OPEN MYSQL", StepID: "s1"},
			wantVerdict:    VerdictVerified,
			wantOracleKind: OracleToolEvidence,
		},
		{
			name:           "quoted and backticked evidence matches cleanly",
			detail:         FindingDetail{Title: "MySQL exposed", Evidence: "`3306/tcp open mysql 5.5.47`", StepID: "s1"},
			wantVerdict:    VerdictVerified,
			wantOracleKind: OracleToolEvidence,
		},
		{
			name:           "whitespace-normalized evidence matches multi-column output",
			detail:         FindingDetail{Title: "MySQL exposed", Evidence: "3306/tcp   open   mysql 5.5.47", StepID: "s1"},
			wantVerdict:    VerdictVerified,
			wantOracleKind: OracleToolEvidence,
		},
		{
			name:        "cited step that failed is unsupported even with a plausible quote",
			detail:      FindingDetail{Title: "Nikto found vulns", Evidence: "known vulnerabilities", StepID: "s2"},
			wantVerdict: VerdictUnsupported,
		},
		{
			name:        "cited step not in the plan stays a candidate",
			detail:      FindingDetail{Title: "phantom finding", Evidence: "anything", StepID: "s99"},
			wantVerdict: VerdictCandidate,
		},
		{
			name:        "paraphrased evidence that isn't a verbatim quote stays a candidate",
			detail:      FindingDetail{Title: "MySQL exposed", Evidence: "a database service was detected", StepID: "s1"},
			wantVerdict: VerdictCandidate,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := verifyFindingDetail(tc.detail, tc.currentStepID, tc.liveContent, plan)
			if rec.Verdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q (reason: %q)", rec.Verdict, tc.wantVerdict, rec.Reason)
			}
			if tc.wantOracleKind != "" && rec.OracleKind != tc.wantOracleKind {
				t.Fatalf("oracle_kind = %q, want %q", rec.OracleKind, tc.wantOracleKind)
			}
			if tc.wantVerdict == VerdictVerified && rec.Reason != "" {
				t.Fatalf("verified record should carry no Reason, got %q", rec.Reason)
			}
			if tc.wantVerdict != VerdictVerified && rec.Reason == "" {
				t.Fatalf("non-verified record should explain why, got empty Reason")
			}
		})
	}
}

func TestBuildFindingRecordsSkipsEmptyDetails(t *testing.T) {
	details := []FindingDetail{
		{}, // no title, no description — must be skipped entirely, like recordTaskFindings
		{Title: "real finding", Evidence: "x", StepID: "s1"},
	}
	plan := []PlanStep{{ID: "s1", Status: StepDone, Result: "x"}}

	records := buildFindingRecords(details, "", "", plan)
	if len(records) != 1 {
		t.Fatalf("expected 1 record (empty detail skipped), got %d: %+v", len(records), records)
	}
	if records[0].Title != "real finding" {
		t.Fatalf("unexpected record: %+v", records[0])
	}
}

func TestVerifyFindingDetailSanitizesTitleAndEvidence(t *testing.T) {
	plan := []PlanStep{{ID: "s1", Status: StepDone, Result: "port 22 open"}}
	rec := verifyFindingDetail(FindingDetail{
		Title:    "<think>hidden reasoning</think>SSH exposed",
		Evidence: "port 22 open",
		StepID:   "s1",
	}, "", "", plan)
	if rec.Title != "SSH exposed" {
		t.Fatalf("expected sanitized title, got %q", rec.Title)
	}
	if rec.Verdict != VerdictVerified {
		t.Fatalf("expected verified, got %q (reason: %q)", rec.Verdict, rec.Reason)
	}
}
