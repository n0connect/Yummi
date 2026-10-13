package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const researchProviderURL = "https://openrouter.ai/api/v1/chat/completions"

// getGatewayHostnamePolicy queries the orchestrator/gateway hostname-policy endpoint.
func getGatewayHostnamePolicy(ctx context.Context) (allow []string, block []string, err error) {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("policy redirects refused")
	}}
	defer client.CloseIdleConnections()
	var policy struct {
		Status string   `json:"status"`
		Allow  []string `json:"allow"`
		Block  []string `json:"block"`
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://172.20.0.3:9080/api/v1/gateway/hostname-policy", nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("gateway hostname policy unavailable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK || json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 65536)).Decode(&policy) != nil || policy.Status != "connected" {
		return nil, nil, fmt.Errorf("gateway hostname policy unavailable")
	}
	return policy.Allow, policy.Block, nil
}

// getGatewayBlockedURLPatterns returns wildcard URL patterns for CDP Network.setBlockedURLs
// covering http, https, ws, and wss for all gateway-blocked hostnames.
func getGatewayBlockedURLPatterns(ctx context.Context) ([]string, error) {
	_, block, err := getGatewayHostnamePolicy(ctx)
	if err != nil {
		return nil, err
	}
	var patterns []string
	for _, b := range block {
		clean := strings.TrimPrefix(b, "*.")
		patterns = append(patterns,
			"*://*."+clean+"/*",
			"*://"+clean+"/*",
			"ws://*."+clean+"/*",
			"ws://"+clean+"/*",
			"wss://*."+clean+"/*",
			"wss://"+clean+"/*",
		)
	}
	return patterns, nil
}

// Only this fixed control-plane address is used to READ gateway policy. No
// task argument can select a control-plane URL or mutate policy.
func researchHostnameAllowed(ctx context.Context, host string) error {
	allow, block, err := getGatewayHostnamePolicy(ctx)
	if err != nil {
		return err
	}
	for _, pattern := range block {
		if researchHostMatch(host, pattern) {
			return fmt.Errorf("GATEWAY_BLOCKED: hostname denied")
		}
	}
	if len(allow) == 0 {
		return nil
	}
	for _, pattern := range allow {
		if researchHostMatch(host, pattern) {
			return nil
		}
	}
	return fmt.Errorf("GATEWAY_BLOCKED: hostname outside allow policy")
}

func researchHostMatch(host, pattern string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	pattern = strings.TrimSuffix(strings.ToLower(pattern), ".")
	if strings.HasPrefix(pattern, "*.") {
		base := strings.TrimPrefix(pattern, "*.")
		return host == base || strings.HasSuffix(host, "."+base)
	}
	return host == pattern
}

func validateResearchURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("research requires a public HTTPS URL on port 443 without credentials")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicResearchIP(ip) {
		return nil, fmt.Errorf("research refuses non-public addresses")
	}
	if strings.EqualFold(u.Hostname(), "localhost") || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, fmt.Errorf("research refuses local or malformed URLs")
	}
	u.Fragment = ""
	return u, nil
}

func publicResearchIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}

// The transport resolves and connects to the SAME checked address, never
// a second DNS lookup. Proxy environment variables are deliberately ignored.
func newResearchHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" {
			return nil, fmt.Errorf("invalid research destination")
		}
		if err := researchHostnameAllowed(ctx, host); err != nil {
			return nil, err
		}
		ips, err := resolveResearchIPs(ctx, host)
		if err != nil || len(ips) == 0 || len(ips) > 16 {
			return nil, fmt.Errorf("research DNS resolution failed")
		}
		for _, ip := range ips {
			if !publicResearchIP(ip) {
				return nil, fmt.Errorf("research DNS returned a non-public address")
			}
		}
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("research connection blocked or unavailable; check gateway egress approval")
	}
	return &http.Client{Transport: transport, Timeout: 90 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return fmt.Errorf("research redirect limit exceeded")
		}
		_, err := validateResearchURL(req.URL.String())
		return err
	}}
}

// Fixed Cloudflare DNS-over-HTTPS endpoint. This is sandbox traffic subject to
// the gateway, not a host resolver or proxy. It works where upstream port 53
// is unavailable; rules-only mode must explicitly permit 1.1.1.1:443.
func resolveResearchIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", "1.1.1.1:443")
	}}
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("DNS redirects refused") }}
	defer client.CloseIdleConnections()
	var ips []netip.Addr
	for _, kind := range []string{"A", "AAAA"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://1.1.1.1/dns-query?name="+url.QueryEscape(host)+"&type="+kind, nil)
		req.Header.Set("Accept", "application/dns-json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("sandbox DNS-over-HTTPS unavailable; check gateway egress")
		}
		var answer struct {
			Status int `json:"Status"`
			Answer []struct {
				Type int    `json:"type"`
				Data string `json:"data"`
			} `json:"Answer"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&answer)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || answer.Status != 0 {
			return nil, fmt.Errorf("sandbox DNS-over-HTTPS response invalid")
		}
		for _, entry := range answer.Answer {
			if entry.Type == 1 || entry.Type == 28 {
				if ip, err := netip.ParseAddr(entry.Data); err == nil {
					ips = append(ips, ip)
				}
			}
		}
	}
	return ips, nil
}
