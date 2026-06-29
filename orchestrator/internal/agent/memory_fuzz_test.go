package agent

import (
	"testing"
	"unicode/utf8"
)

// FuzzUtf8SafeCut drives arbitrary strings and cut points through utf8SafeCut
// and checks the security property the helper exists for: slicing tool output
// (attacker-influenced — the pentest target controls it) at the returned index
// must never split a multi-byte UTF-8 rune, regardless of how the caller picked
// n. This is the bug fixed in capObservation/summarizeInput/CompressObservation,
// which previously sliced at a raw byte offset.
func FuzzUtf8SafeCut(f *testing.F) {
	seeds := []struct {
		s string
		n int
	}{
		{"hello world", 5},
		{"héllo wörld", 3}, // multi-byte runes straddling likely cut points
		{"日本語のテスト", 4},     // 3-byte runes throughout
		{"🔥🔥🔥", 2},         // 4-byte runes (emoji)
		{"", 0},
		{"x", -1},
		{"x", 100},
		{string([]byte{0xff, 0xfe, 0x80}), 1}, // invalid UTF-8 input
	}
	for _, sd := range seeds {
		f.Add(sd.s, sd.n)
	}
	f.Fuzz(func(t *testing.T, s string, n int) {
		cut := utf8SafeCut(s, n) // must never panic

		if cut < 0 || cut > len(s) {
			t.Fatalf("utf8SafeCut(%q, %d) = %d out of bounds [0,%d]", s, n, cut, len(s))
		}
		// The defining property: s[:cut] and s[cut:] must each independently be
		// valid UTF-8 whenever s itself is valid UTF-8 — the cut point must land
		// on a rune boundary, never mid-sequence.
		if utf8.ValidString(s) {
			if !utf8.ValidString(s[:cut]) {
				t.Fatalf("utf8SafeCut(%q, %d) = %d splits a rune in the head: %q", s, n, cut, s[:cut])
			}
			if !utf8.ValidString(s[cut:]) {
				t.Fatalf("utf8SafeCut(%q, %d) = %d splits a rune in the tail: %q", s, n, cut, s[cut:])
			}
		}
	})
}

// FuzzCapObservation drives arbitrary tool-output-shaped strings through
// capObservation and checks it never panics and never returns invalid UTF-8 —
// the value is appended to MemoryContext.Observations and later JSON-encoded
// into prompts and API responses, so invalid UTF-8 in the head/tail it carves
// out would corrupt those downstream encodings.
func FuzzCapObservation(f *testing.F) {
	seeds := []string{
		"",
		"short output",
		string(make([]byte, maxRetainedObservation+1)),       // exactly over the cap, all zero bytes
		"日本語" + string(make([]byte, maxRetainedObservation)), // multi-byte content straddling the cap edges
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, obs string) {
		out := capObservation(obs) // must never panic
		if utf8.ValidString(obs) && !utf8.ValidString(out) {
			t.Fatalf("capObservation produced invalid UTF-8 from valid input (len=%d)", len(obs))
		}
	})
}

// FuzzSummarizeInput and FuzzCompressObservation cover the other two
// head/tail-budget slicers fixed alongside utf8SafeCut, with the same
// no-panic + valid-UTF-8 invariants.
func FuzzSummarizeInput(f *testing.F) {
	seeds := []struct {
		obs    string
		budget int
	}{
		{"short", 100},
		{"日本語のテスト\n---\nmore 日本語", 10},
		{"", 0},
		{"x", -5},
	}
	for _, sd := range seeds {
		f.Add(sd.obs, sd.budget)
	}
	f.Fuzz(func(t *testing.T, obs string, budget int) {
		if obs == "" {
			return // summarizeInput indexes obs[0]; the real caller never passes an empty slice
		}
		out := summarizeInput([]string{obs}, budget) // must never panic
		if utf8.ValidString(obs) && !utf8.ValidString(out) {
			t.Fatalf("summarizeInput produced invalid UTF-8 from valid input (budget=%d)", budget)
		}
	})
}

func FuzzCompressObservation(f *testing.F) {
	seeds := []string{
		"",
		"short",
		"日本語" + string(make([]byte, compressHeadBytes+compressTailBytes)),
		string(make([]byte, compressHeadBytes+compressTailBytes+1)),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, output string) {
		out := CompressObservation(output) // must never panic
		if utf8.ValidString(output) && !utf8.ValidString(out) {
			t.Fatalf("CompressObservation produced invalid UTF-8 from valid input (len=%d)", len(output))
		}
	})
}
