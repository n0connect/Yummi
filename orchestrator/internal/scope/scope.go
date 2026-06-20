// Package scope extracts the network target from a tool command string.
//
// It is the single source of truth shared by every component that needs to know
// "what host is this command going to talk to": the conductor (proposal
// validation), the executor (pre-exec gateway scope check), and the API's direct
// exec endpoint. Keeping one implementation means the validator and the executor
// always reason about the *same* target — a divergence here would let a command
// be scope-checked against one host but actually unlocked for another.
package scope

import (
	"net/url"
	"regexp"
	"strings"
)

// targetPattern matches an IPv4 address (optionally with a CIDR suffix) or a
// dotted hostname. It is deliberately conservative: a bare word with no dot
// (e.g. "localhost", "100") does not match, so loop counters and tool
// subcommands are never mistaken for targets.
var targetPattern = regexp.MustCompile(`\b(\d{1,3}(?:\.\d{1,3}){3}(?:/\d{1,2})?|[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z]{2,})+)\b`)

// ExtractTarget returns the first IP or hostname a command appears to target,
// or "" when none can be identified.
//
// The scan walks the command's tokens, skipping the tool name (first token),
// flags, and filesystem paths, so that wordlist paths like
// "/usr/share/wordlists/common.txt" are never returned as targets. For URL
// arguments the host is taken from the parsed URL; for "--flag=value" forms the
// value is inspected. Each candidate is validated with targetPattern.
func ExtractTarget(command string) string {
	for i, tok := range strings.Fields(command) {
		if i == 0 {
			continue // tool name / binary path
		}

		// "--url=http://x" / "-u=host" — inspect the value, not the flag.
		if strings.HasPrefix(tok, "-") {
			eq := strings.IndexByte(tok, '=')
			if eq < 0 {
				continue // bare flag
			}
			tok = tok[eq+1:]
		}
		if tok == "" {
			continue
		}

		// Filesystem paths are never network targets.
		if strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") {
			continue
		}

		// URL argument: take the host component (handles userinfo, port, path).
		if strings.Contains(tok, "://") {
			if u, err := url.Parse(tok); err == nil {
				if host := targetPattern.FindString(u.Hostname()); host != "" {
					return host
				}
			}
			continue
		}

		if host := targetPattern.FindString(tok); host != "" {
			return host
		}
	}
	return ""
}
