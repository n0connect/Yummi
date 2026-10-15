package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"time"
)

type researchEgressError struct {
	Kind   string   `json:"kind"`
	Target string   `json:"target"`
	IPs    []string `json:"ips"`
}

func (e *researchEgressError) Error() string {
	return "research destination requires operator egress approval"
}

// requireResearchEgress reads a decision. Only the operator-side ResolveEgress
// flow may open a rule; this endpoint cannot mutate gateway policy.
func requireResearchEgress(ctx context.Context, target string, ips []netip.Addr) error {
	client := &http.Client{
		Timeout:       3 * time.Second,
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("policy redirects refused") },
	}
	defer client.CloseIdleConnections()
	approval := false
	addresses := make([]string, 0, len(ips))
	if len(ips) == 0 || len(ips) > 8 {
		return fmt.Errorf("invalid research address set")
	}
	for _, ip := range ips {
		body, _ := json.Marshal(map[string]string{"ip": ip.String()})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://172.20.0.3:9081/v1/egress/check", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("gateway egress policy unavailable: %w", err)
		}
		var result struct {
			Verdict string `json:"verdict"`
		}
		dec := json.NewDecoder(io.LimitReader(resp.Body, 4096))
		err = dec.Decode(&result)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil {
			return fmt.Errorf("gateway egress policy unavailable")
		}
		switch result.Verdict {
		case "allow":
		case "needs_approval":
			approval = true
		case "block":
			return fmt.Errorf("GATEWAY_BLOCKED: research destination denied")
		default:
			return fmt.Errorf("gateway returned invalid egress verdict")
		}
		addresses = append(addresses, ip.String())
	}
	if approval {
		return &researchEgressError{Kind: "egress_approval_required", Target: target, IPs: addresses}
	}
	return nil
}

func researchFailure(err error, fallback string) string {
	var approval *researchEgressError
	if errors.As(err, &approval) {
		encoded, _ := json.Marshal(approval)
		return string(encoded)
	}
	return fallback
}
