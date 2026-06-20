package cli

import "testing"

func TestParseTopLevelOptionsDefaultUI(t *testing.T) {
	top, err := parseTopLevelOptions(nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if top.Action != actionUI {
		t.Fatalf("expected action %q, got %q", actionUI, top.Action)
	}
}

func TestParseTopLevelOptionsSingleOperation(t *testing.T) {
	top, err := parseTopLevelOptions([]string{"--start"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if top.Action != actionStart {
		t.Fatalf("expected action %q, got %q", actionStart, top.Action)
	}
}

func TestParseTopLevelOptionsJSONFlagRemoved(t *testing.T) {
	if _, err := parseTopLevelOptions([]string{"--json"}); err == nil {
		t.Fatal("expected error for removed --json flag")
	}
}

func TestParseTopLevelOptionsMultipleOperationFails(t *testing.T) {
	if _, err := parseTopLevelOptions([]string{"--start", "--stop"}); err == nil {
		t.Fatal("expected error for multiple operation flags")
	}
}

func TestParseTopLevelOptionsSubcommandFails(t *testing.T) {
	if _, err := parseTopLevelOptions([]string{"start"}); err == nil {
		t.Fatal("expected error for subcommand syntax")
	}
}

func TestParseTopLevelOptionsUnknownFlagFails(t *testing.T) {
	if _, err := parseTopLevelOptions([]string{"--wat"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}
