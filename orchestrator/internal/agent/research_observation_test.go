package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResearchObservationPreservesSourceWindow(t *testing.T) {
	input := `{"kind":"web_fetch","sources":[{"source_url":"https://www.openssh.org/security.html","excerpt":"` + strings.Repeat("source ", 300) + `Affected versions 8.5p1 to 9.7p1` + strings.Repeat(" evidence", 300) + `"}],"next_offset":6000}`
	got := CompressObservation(input)
	if !json.Valid([]byte(got)) || !strings.Contains(got, "Affected versions 8.5p1 to 9.7p1") {
		t.Fatal("research source window was fragmented or its middle evidence discarded")
	}
}

func TestResearchObservationLargeReportRemainsBoundedAndExplicit(t *testing.T) {
	input := `{"kind":"web_search","sources":[{"source_url":"https://www.openssh.org/security.html","excerpt":"` + strings.Repeat("evidence ", 10000) + `"}]}`
	got := CompressObservation(input)
	if !json.Valid([]byte(got)) || len(got) > 16384 || !strings.Contains(got, "EXCERPT_TRUNCATED") || !strings.Contains(got, "https://www.openssh.org/security.html") {
		t.Fatal("large research report must remain valid bounded JSON with source and explicit truncation notice")
	}
}
