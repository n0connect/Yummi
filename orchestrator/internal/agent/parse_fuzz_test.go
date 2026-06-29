package agent

import (
	"strings"
	"testing"
)

// FuzzParseControl drives arbitrary "LLM replies" through the control parser.
// LLM output is untrusted (a small/jailbroken model, or one fed injected target
// output, can emit anything), so the parser must never panic and must never
// return a ControlMsg whose Tag is outside the LLM→agent vocabulary — a tag the
// agent does not understand could route the state machine into an undefined edge.
func FuzzParseControl(f *testing.F) {
	for _, s := range []string{
		`[RUN_COMMAND]{"command":"nmap x"}`,
		`<think>reasoning</think>[FINISH]{"summary":"done"}`,
		"{\"command\":\"id\"}",
		"CONTINUE",
		"```json\n{\"steps\":[{\"id\":\"s1\"}]}\n```",
		"[UNKNOWN]", // agent→LLM only; must NOT be accepted as a control
		"[[[[",
		"{{{{{{",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		msg, err := ParseControl(raw)
		if err != nil {
			return // a parse error is a valid outcome
		}
		if _, ok := lookupTag(msg.Tag); !ok {
			t.Fatalf("ParseControl(%q) returned unregistered tag %q", raw, msg.Tag)
		}
		if def, _ := lookupTag(msg.Tag); def.Dir != LLMToAgent {
			t.Fatalf("ParseControl(%q) accepted agent→LLM tag %q as a control", raw, msg.Tag)
		}
	})
}

// FuzzExtractJSON checks the JSON-extraction helper never panics and always
// returns a substring of the (think-stripped) input — it must not fabricate
// bytes, only carve out a candidate object.
func FuzzExtractJSON(f *testing.F) {
	for _, s := range []string{
		`prose {"a":1} trailer`,
		"no json here",
		"{unterminated",
		"<think>x</think>{\"k\":\"v\"}",
		"}{}{",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		out := extractJSON(raw) // must not panic
		stripped := stripThinkTokens(raw)
		if out != "" && !strings.Contains(stripped, out) {
			t.Fatalf("extractJSON(%q) = %q is not a substring of the stripped input %q", raw, out, stripped)
		}
	})
}
