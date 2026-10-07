package agent

import (
	"reflect"
	"testing"
)

func TestInferMCPToolFromShellGuess(t *testing.T) {
	known := map[string]string{"term_open": "label, cols, rows", "term_run": "id, command"}

	cases := []struct {
		name     string
		command  string
		wantTool string
		wantArgs map[string]any
		wantOK   bool
	}{
		{
			name:     "live-observed guess with space-separated flag",
			command:  "term_open --label check5",
			wantTool: "term_open",
			wantArgs: map[string]any{"label": "check5"},
			wantOK:   true,
		},
		{
			name:     "equals-separated flag",
			command:  "term_open --label=check5",
			wantTool: "term_open",
			wantArgs: map[string]any{"label": "check5"},
			wantOK:   true,
		},
		{
			name:     "multiple flags",
			command:  "term_run --id check4 --command id",
			wantTool: "term_run",
			wantArgs: map[string]any{"id": "check4", "command": "id"},
			wantOK:   true,
		},
		{
			name:     "quoted value",
			command:  `term_open --label "check5"`,
			wantTool: "term_open",
			wantArgs: map[string]any{"label": "check5"},
			wantOK:   true,
		},
		{
			name:    "unknown verb is never rewritten",
			command: "echo --label check5",
			wantOK:  false,
		},
		{
			name:    "empty command",
			command: "",
			wantOK:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, args, ok := InferMCPToolFromShellGuess(c.command, known)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if tool != c.wantTool {
				t.Errorf("tool = %q, want %q", tool, c.wantTool)
			}
			if !reflect.DeepEqual(args, c.wantArgs) {
				t.Errorf("args = %#v, want %#v", args, c.wantArgs)
			}
		})
	}
}
