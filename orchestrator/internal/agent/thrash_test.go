package agent

import "testing"

func TestToolThrashDetectsSameToolRotation(t *testing.T) {
	// Four trivially-different invocations of the SAME tool — distinct strings
	// (so exact-hash duplicate detection misses them) but one tool.
	win := []string{"ps -ef", "ps -ejH", "ps aux", "ps -e"}
	tool, thrash := toolThrash(win)
	if !thrash || tool != "ps" {
		t.Fatalf("expected thrash on ps, got tool=%q thrash=%v", tool, thrash)
	}
}

func TestToolThrashAllowsLegitimateEscalation(t *testing.T) {
	// Three distinct nmap phases is legitimate escalation, not thrash.
	win := []string{"nmap -sV 10.0.0.5", "nmap -p- 10.0.0.5", "nmap -sC 10.0.0.5"}
	if tool, thrash := toolThrash(win); thrash {
		t.Fatalf("3 nmap phases should not be flagged, got tool=%q", tool)
	}
}

func TestToolThrashMixedToolsNotFlagged(t *testing.T) {
	win := []string{"ps -ef", "id", "whoami", "netstat -tlnp", "ps -ejH", "uname -a"}
	if _, thrash := toolThrash(win); thrash {
		t.Fatal("a varied recon sequence must not be flagged as thrash")
	}
}

func TestToolThrashEmptyWindow(t *testing.T) {
	if _, thrash := toolThrash(nil); thrash {
		t.Fatal("empty window must not thrash")
	}
}
