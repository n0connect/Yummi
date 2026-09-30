package scope

import (
	"strings"
	"testing"
)

// FuzzExtractTargets hardens the single source of truth every scope decision
// in the codebase depends on (see package doc: validator and executor must
// always agree on the same target, or a command could be scope-checked
// against one host and unlocked for another). Seeded with the existing
// hand-written regression cases plus known-tricky shapes (scope-confusion
// IP-prefix hostnames, boundary octets, IPv6 literals, quoting) so the fuzzer
// starts mutating from real, meaningful inputs rather than blank bytes.
func FuzzExtractTargets(f *testing.F) {
	seeds := []string{
		"nmap 10.0.0.1 8.8.8.8 example.com",
		"curl https://example.com https://api.example.com/x https://example.com/y",
		"nmap -iL /workspace/targets.txt --top-ports 100",
		"nmap -sV -p 80 10.0.0.1",
		"nmap -sV --top-ports 100 scanme.nmap.org",
		"curl http://user:pass@10.0.0.7:8080/x",
		"ffuf -w /usr/share/wordlists/common.txt -u http://10.0.0.1/FUZZ",
		"ffuf --url=http://10.0.0.9/FUZZ",
		"nmap -sn 192.168.1.0/24",
		"curl http://[2001:db8::1]/x",
		"nmap 256.1.1.1",
		"nmap 999.999.999.999",
		"nmap 1.2.3.4.evil.com",
		"curl http://1.2.3.4.evil.com/x",
		`curl "10.0.0.1"`,
		`nikto -h 'example.org'`,
		`ffuf --url="http://10.0.0.9/FUZZ"`,
		"echo hello",
		"nmap localhost",
		"",
		"   ",
		"nmap --flag=",
		"nmap -",
		"nmap =",
		"curl http://",
		"curl ://nohost",
		"nmap \x00\x01\x02",
		"nmap " + strings.Repeat("a", 5000) + ".com",
		"nmap " + strings.Repeat("1.", 2000) + "1",
		"curl http://[::1",
		"curl http://user:pass@[::1]:80/x",
		"nmap 你好.example.com",
		"nmap İstanbul.example.com",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, command string) {
		// Invariant 1: never panics (checked implicitly — testing.F fails the
		// corpus entry and reports it if the function under test panics).
		targets := ExtractTargets(command)

		// Invariant 2: every returned target is non-empty and independently
		// re-validates as a well-formed target on its own — ExtractTargets
		// must never emit something validTarget itself would reject, or a
		// caller trusting the list could scope-check something malformed.
		for _, target := range targets {
			if target == "" {
				t.Fatalf("ExtractTargets(%q) returned an empty target string", command)
			}
			if revalidated := validTarget(target); revalidated == "" {
				t.Fatalf(
					"ExtractTargets(%q) returned %q, but validTarget(%q) rejects it — "+
						"an invalid target must never reach a caller",
					command, target, target,
				)
			}
		}

		// Invariant 3: no duplicates — ExtractTargets already de-duplicates
		// internally (the `seen` map); a fuzz-found input that slips a dup
		// through would indicate the de-dup key doesn't match the actual
		// returned value in some encoding edge case.
		seen := make(map[string]bool, len(targets))
		for _, target := range targets {
			if seen[target] {
				t.Fatalf("ExtractTargets(%q) returned duplicate target %q: %v", command, target, targets)
			}
			seen[target] = true
		}

		// Invariant 4: ExtractTarget (the single-target convenience wrapper)
		// must always agree with ExtractTargets[0] — two implementations of
		// "the first target" must never diverge.
		single := ExtractTarget(command)
		if len(targets) == 0 {
			if single != "" {
				t.Fatalf("ExtractTarget(%q) = %q but ExtractTargets returned none", command, single)
			}
		} else if single != targets[0] {
			t.Fatalf("ExtractTarget(%q) = %q, want %q (ExtractTargets[0])", command, single, targets[0])
		}
	})
}
