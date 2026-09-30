package conductor

import (
	"strings"
	"testing"
)

// FuzzClassifyRisk drives arbitrary command strings through classifyRisk and
// checks the invariants the allowlist rewrite depends on: the function must
// never panic, must always return one of the three known risk labels, any
// command containing a shell-significant character OUTSIDE a quoted span must
// never be classified "low" (the whole point of the default-deny allowlist
// on command shape), and — the newer isSafeLocalPipeline widening —
// "medium" is only ever reached via a genuine multi-segment chain, never a
// lone command.
func FuzzClassifyRisk(f *testing.F) {
	seeds := []string{
		"nmap -sV 10.0.0.1",
		"ping 127.0.0.1; ping 8.8.8.8",
		"echo `whoami`",
		"echo $(whoami)",
		"sleep 30 &",
		"cat file > /tmp/out",
		"(cd /tmp; ls)",
		`curl 'http://x/?a=1&b=2'`,
		`grep "a;b|c" file.txt`,
		"",
		"   ",
		"\n",
		strings.Repeat("nmap -p1-65535 10.0.0.1 ", 50),
		"cat a.txt | sort -u | wc -l",
		"cat a.txt | rm -rf /",
		"cat a.txt | awk '{print}' | sort",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cmd string) {
		got := classifyRisk(cmd) // must never panic

		if got != "low" && got != "medium" && got != "critical" {
			t.Fatalf("classifyRisk(%q) returned unknown label %q", cmd, got)
		}

		if got == "low" && hasUnquotedShellMetachar(cmd) {
			t.Fatalf("classifyRisk(%q) = %q but the command has an unquoted shell-significant "+
				"character — the allowlist must never call this low risk", cmd, got)
		}

		if got == "medium" && !hasUnquotedShellMetachar(cmd) {
			t.Fatalf("classifyRisk(%q) = medium but has no unquoted shell metachar at all — "+
				"isSafeLocalPipeline requires a genuine multi-segment chain, never a lone command",
				cmd)
		}
	})
}

// hasUnquotedShellMetachar is the fuzz test's OWN independent check (not a
// call into the production code under test) for whether cmd contains a
// shell-significant character outside any '...'/"..." span. It deliberately
// duplicates a tiny piece of parsing logic so the test can verify
// classifyRisk's allowlist behavior from a different angle than the
// production regex itself.
func hasUnquotedShellMetachar(cmd string) bool {
	const metachars = ";|&`$(){}[]<>#\n\r\\"
	inSingle, inDouble := false, false
	for _, r := range cmd {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case !inSingle && !inDouble && strings.ContainsRune(metachars, r):
			return true
		}
	}
	return false
}
