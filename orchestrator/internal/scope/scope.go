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
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// hostnamePattern matches a syntactically valid DNS hostname as a WHOLE token
// (note the ^…$ anchors): one or more labels of alphanumerics/hyphens, each
// label 1–63 chars and not starting/ending with a hyphen, ending in a letters-
// only TLD. Anchoring is the security-relevant part: it validates the entire
// token, so an IP-shaped prefix can never be peeled off a longer name (e.g.
// "1.2.3.4.evil.com" is the hostname, never the IP "1.2.3.4"). A bare word with
// no dot (e.g. "localhost", "100") does not match, so loop counters and tool
// subcommands are never mistaken for targets.
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*\.[a-zA-Z]{2,}$`)

// validTarget canonicalizes a single, fully-stripped token into a target, or ""
// when the token is not a structurally valid IP, CIDR, or hostname. This is an
// allowlist of well-formed shapes, not a blacklist: IP and CIDR forms are
// bound-checked by net/netip (rejecting out-of-range octets like 999.x or
// 256.1.1.1 and bad prefix lengths), hostnames must match the whole token, and
// host:port endpoints have their port validated and stripped.
// Anything else returns "" — the caller then treats the command as targetless,
// and the gateway (sole scope authority) stays fail-closed.
func validTarget(tok string) string {
	if addr, err := netip.ParseAddr(tok); err == nil {
		return addr.String()
	}
	if pfx, err := netip.ParsePrefix(tok); err == nil {
		return pfx.String()
	}
	if hostnamePattern.MatchString(tok) {
		return tok
	}
	if host, port, err := net.SplitHostPort(tok); err == nil && port != "" {
		if p, err := strconv.Atoi(port); err == nil && p > 0 && p <= 65535 {
			if addr, err := netip.ParseAddr(host); err == nil {
				return addr.String()
			}
			if hostnamePattern.MatchString(host) {
				return host
			}
		}
	}
	return ""
}

// ExtractTargets returns every distinct IP or hostname a command appears to
// target, in argument order. An empty slice means none can be identified.
//
// The scan walks the command's tokens, skipping the tool name (first token),
// flags, and filesystem paths, so that wordlist paths like
// "/usr/share/wordlists/common.txt" are never returned as targets. For URL
// arguments the host is taken from the parsed URL; for "--flag=value" forms the
// value is inspected. Each candidate is validated with targetPattern.
func ExtractTargets(command string) []string {
	var targets []string
	seen := make(map[string]struct{})
	add := func(target string) {
		if target == "" {
			return
		}
		if _, ok := seen[target]; ok {
			return
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}

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

		// Trim surrounding single/double quotes to resolve quoted targets like curl "10.0.0.1"
		tok = strings.Trim(tok, "\"'")

		if tok == "" {
			continue
		}

		// Filesystem paths are never network targets.
		if strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") {
			continue
		}

		// URL argument: take the host component (handles userinfo, port, path,
		// and IPv6 literals like [::1], which url.Hostname unwraps).
		if strings.Contains(tok, "://") {
			if u, err := url.Parse(tok); err == nil {
				add(validTarget(u.Hostname()))
			}
			continue
		}

		add(validTarget(tok))
	}
	return targets
}

// ExtractTarget preserves the original single-target API for callers that
// only need a representative target. Enforcement paths must use
// ExtractTargets so later arguments cannot bypass scope checks.
func ExtractTarget(command string) string {
	targets := ExtractTargets(command)
	if len(targets) == 0 {
		return ""
	}
	return targets[0]
}
