package dnsresolve

import (
	"context"
	"testing"
)

// TestResolveIPLiteralPassthrough proves an IP-shaped target is returned as-is
// (no DNS lookup), deterministically and offline.
func TestResolveIPLiteralPassthrough(t *testing.T) {
	rs := New()
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		got := rs.Resolve(context.Background(), ip)
		if len(got) != 1 || got[0] != ip {
			t.Fatalf("Resolve(%q) = %v, want [%q]", ip, got, ip)
		}
	}
}

func TestResolveCIDRLiteralPassthrough(t *testing.T) {
	got := New().Resolve(context.Background(), "10.0.0.0/24")
	if len(got) != 1 || got[0] != "10.0.0.0/24" {
		t.Fatalf("Resolve(CIDR) = %v, want [10.0.0.0/24]", got)
	}
}

func TestResolveEmptyHost(t *testing.T) {
	if got := New().Resolve(context.Background(), ""); got != nil {
		t.Fatalf("Resolve(\"\") = %v, want nil", got)
	}
}

// TestResolveHostnameViaCloudflare is a real (no-mock) lookup through Cloudflare.
// It self-skips when offline so it never flakes a network-less CI, but when the
// network is up it proves a hostname expands to at least one routable IP.
func TestResolveHostnameViaCloudflare(t *testing.T) {
	got := New().Resolve(context.Background(), "one.one.one.one")
	if len(got) == 0 {
		t.Skip("no network / Cloudflare DNS unreachable — skipping live resolution check")
	}
	// one.one.one.one is Cloudflare's own name; it must include 1.1.1.1 or 1.0.0.1.
	found := false
	for _, ip := range got {
		if ip == "1.1.1.1" || ip == "1.0.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 1.1.1.1/1.0.0.1 among resolved IPs, got %v", got)
	}
	if len(got) > maxResolvedIPs {
		t.Fatalf("resolved %d IPs, exceeds cap %d", len(got), maxResolvedIPs)
	}
}
