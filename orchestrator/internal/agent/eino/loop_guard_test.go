package eino

import "testing"

func TestLoopGuardDetectsExactDuplicate(t *testing.T) {
	g := &loopGuard{}
	if msg := g.check("nmap -sV 198.51.100.10"); msg != "" {
		t.Fatalf("first run should not be blocked, got: %s", msg)
	}
	if msg := g.check("nmap -sV 198.51.100.10"); msg == "" {
		t.Fatal("exact repeat should be blocked")
	}
}

func TestLoopGuardDetectsStuckRepeats(t *testing.T) {
	g := &loopGuard{}
	cmd := "whoami"
	var lastMsg string
	for i := 0; i < 3; i++ {
		lastMsg = g.check(cmd)
	}
	if lastMsg == "" {
		t.Fatal("3 identical commands should trip stuck detection")
	}
}

func TestLoopGuardDetectsToolThrash(t *testing.T) {
	g := &loopGuard{}
	cmds := []string{"ps -ef", "ps -ejH", "ps aux", "ps -el"}
	var lastMsg string
	for _, c := range cmds {
		lastMsg = g.check(c)
	}
	if lastMsg == "" {
		t.Fatal("4 trivially-different invocations of the same tool should trip thrash detection")
	}
}

func TestLoopGuardAllowsDistinctProgress(t *testing.T) {
	g := &loopGuard{}
	cmds := []string{
		"nmap -sV 198.51.100.10",
		"whatweb http://198.51.100.10",
		"gobuster dir -u http://198.51.100.10 -w /tmp/wordlist.txt",
		"curl -s http://198.51.100.10/robots.txt",
	}
	for _, c := range cmds {
		if msg := g.check(c); msg != "" {
			t.Fatalf("distinct commands making progress should never be blocked, got: %s (cmd=%s)", msg, c)
		}
	}
}
