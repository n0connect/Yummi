// Package dnsresolve maps a hostname to IP(s) via Cloudflare DNS — the same
// resolver the sandbox is configured to use (docker-compose sets the sandbox's
// dns to 1.1.1.1). Resolving through the same upstream is what lets the
// orchestrator open a gateway allow rule for the very IP the sandbox will then
// connect to.
//
// DNS resolution is deliberately NOT the trust boundary: knowing a name's IP
// grants nothing. The gateway's nftables rules still gate the actual
// connection, so resolving here only answers "which IP should the operator's
// egress approval open", never "is this reachable".
package dnsresolve

import (
	"context"
	"net"
	"net/netip"
	"time"
)

// cloudflareDNS is the upstream resolver. Matches the sandbox's configured DNS
// so both sides agree on a name's address(es).
const cloudflareDNS = "1.1.1.1:53"

// maxResolvedIPs bounds how many addresses a single name can expand to, so a
// CDN hostname resolving to a large set can't flood the gateway rule table.
const maxResolvedIPs = 8

const lookupTimeout = 5 * time.Second

// Resolver resolves hostnames to IPs through Cloudflare DNS.
type Resolver struct {
	r *net.Resolver
}

// New constructs a Resolver that dials Cloudflare for every lookup (PreferGo so
// it never consults the host's /etc/resolv.conf).
func New() *Resolver {
	return &Resolver{
		r: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: lookupTimeout}
				return d.DialContext(ctx, network, cloudflareDNS)
			},
		},
	}
}

// Resolve returns up to maxResolvedIPs unique IP strings for host.
//   - An IP/CIDR-shaped literal is returned as-is (single entry) — nothing to resolve.
//   - A hostname is looked up via Cloudflare; an empty or failed lookup returns nil.
func (rs *Resolver) Resolve(ctx context.Context, host string) []string {
	if host == "" {
		return nil
	}
	// Already an IP literal — pass through unchanged.
	if ip := net.ParseIP(host); ip != nil {
		return []string{host}
	}
	if prefix, err := netip.ParsePrefix(host); err == nil {
		return []string{prefix.String()}
	}

	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	addrs, err := rs.r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil
	}

	out := make([]string, 0, len(addrs))
	seen := make(map[string]struct{}, len(addrs))
	for _, a := range addrs {
		s := a.IP.String()
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) >= maxResolvedIPs {
			break
		}
	}
	return out
}
