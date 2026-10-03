package eino

import (
	"strings"
	"testing"
)

// The screenshot that motivated this guard: different tools and encodings,
// all retrying the same cgi-bin traversal hypothesis on one host.
func TestLoopGuardFamilyCatchesRetriedHypothesis(t *testing.T) {
	attempts := []string{
		`printf "GET /cgi-bin/.%%2e/%%2e%%2e/etc/passwd HTTP/1.1\r\nHost: 198.51.100.10\r\n\r\n" | nc 198.51.100.10 80`,
		`curl -s --path-as-is "http://198.51.100.10/cgi-bin/.%2e/.%2e/.%2e/.%2e/etc/passwd"`,
		`curl -s --path-as-is "http://198.51.100.10/cgi-bin/.%2e/.%2e/.%2e/.%2e/bin/sh"`,
		`for d in x; do curl -s "http://198.51.100.10/cgi-bin/../../../../etc/hosts"; done`,
		`curl -s -X POST --data "echo; id" "http://198.51.100.10/cgi-bin/.%2e/.%2e/bin/sh"`,
	}
	g := &loopGuard{}
	var blocked string
	for _, a := range attempts {
		if msg := g.check(a); msg != "" {
			blocked = msg
		}
	}
	if !strings.Contains(blocked, "same route") {
		t.Fatalf("expected family guard to fire on retried cgi-bin hypothesis; got %q", blocked)
	}
}

func TestLoopGuardFamilyIgnoresDistinctRoutesAndHostScans(t *testing.T) {
	g := &loopGuard{}
	cmds := []string{
		`curl -s "http://198.51.100.10/admin/"`,
		`curl -s "http://198.51.100.10/backup/"`,
		`curl -s "http://198.51.100.10/icons/"`,
		`curl -s "http://198.51.100.10/images/"`,
		`curl -s "http://198.51.100.10/secret/"`,
		`nmap -sV 198.51.100.10`,
		`nmap -sC 198.51.100.10`,
		`nmap -p- 198.51.100.10`,
		`nmap -sU 198.51.100.10`,
		`nmap -A 198.51.100.10`,
	}
	for _, c := range cmds {
		if msg := g.check(c); msg != "" && strings.Contains(msg, "same route") {
			t.Fatalf("family guard fired on distinct route/host scan %q: %s", c, msg)
		}
	}
}
