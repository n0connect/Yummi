// Package controlaccess identifies explicitly trusted control-plane peers.
// Forwarded headers are deliberately irrelevant to this decision.
package controlaccess

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

type Peers struct{ hosts map[netip.Addr]struct{} }

// ParsePeers accepts a comma-separated list of exact IPs or host prefixes.
// Networks and an empty list are rejected rather than broadening trust.
func ParsePeers(raw string) (Peers, error) {
	p := Peers{hosts: make(map[netip.Addr]struct{})}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		ip, err := netip.ParseAddr(value)
		if err != nil {
			prefix, prefixErr := netip.ParsePrefix(value)
			if prefixErr != nil || prefix.Bits() != prefix.Addr().BitLen() {
				return Peers{}, fmt.Errorf("control peer must be an exact IP or host prefix: %q", value)
			}
			ip = prefix.Addr()
		}
		if ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return Peers{}, fmt.Errorf("invalid control peer: %q", value)
		}
		p.hosts[ip.Unmap()] = struct{}{}
	}
	return p, nil
}

// Allows compares the socket peer, never a caller-supplied forwarding header.
func (p Peers) Allows(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return false
	}
	_, ok := p.hosts[ip.Unmap()]
	return ok
}
