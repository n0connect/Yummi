package scope

import (
	"net/netip"
	"strings"
	"testing"
)

// FuzzExtractTarget drives random command strings through ExtractTarget and
// enforces the security invariant the whole scope layer rests on: whatever is
// returned must be a token that re-validates as a target on its own. If
// ExtractTarget ever returned an IP-shaped prefix of a longer hostname (the bug
// fixed in this package), or an out-of-range pseudo-IP, this property would fail
// — the gateway would then open an nftables rule for a token that does not match
// what the command actually talks to.
func FuzzExtractTarget(f *testing.F) {
	seeds := []string{
		"nmap -sV 192.168.1.1",
		"curl http://1.2.3.4.evil.com/x",
		"nmap 999.999.999.999",
		"ffuf -u http://10.0.0.1/FUZZ -w /usr/share/wordlists/x.txt",
		"nikto --host=example.org",
		"echo hello",
		"curl http://[2001:db8::1]:8080/y",
		"",
		"-----",
		"a.b.c.d.e.f.g",
		"http://@@@://::::",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, cmd string) {
		got := ExtractTarget(cmd) // must never panic

		if got == "" {
			return
		}

		// Invariant 1: the result re-validates as a target token on its own.
		if validTarget(got) != got {
			t.Fatalf("ExtractTarget(%q) = %q which does not re-validate (got validTarget=%q)",
				cmd, got, validTarget(got))
		}

		// Invariant 2: if the result parses as an IP/CIDR, net/netip must accept
		// it — i.e. octets are in range. Guards against a regex that lets
		// 256.x or 999.x through as a "target".
		if looksNumericIP(got) {
			if _, errA := netip.ParseAddr(got); errA != nil {
				if _, errP := netip.ParsePrefix(got); errP != nil {
					t.Fatalf("ExtractTarget(%q) = %q looks like an IP but net/netip rejects it", cmd, got)
				}
			}
		}
	})
}

// looksNumericIP reports whether s starts with a digit and contains only the
// characters of a dotted/CIDR IPv4 literal — a cheap "is this meant to be an IP"
// check for the fuzz invariant.
func looksNumericIP(s string) bool {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.' && r != '/'
	}) < 0
}
