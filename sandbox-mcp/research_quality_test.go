package main

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestResearchPublicURLAndAddressBoundary(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://localhost", "https://127.0.0.1", "https://[::1]", "https://10.0.0.1", "https://[::ffff:127.0.0.1]", "https://169.254.169.254", "https://user:password@example.com", "https://example.com:8443"} {
		if _, err := validateResearchURL(raw); err == nil {
			t.Errorf("unsafe URL accepted: %q", raw)
		}
	}
	for _, ip := range []string{"100.64.0.1", "198.18.0.1", "224.0.0.1", "0.0.0.0", "fd00::1", "2001:db8::1", "64:ff9b::a00:1"} {
		if publicResearchIP(netip.MustParseAddr(ip)) {
			t.Errorf("unsafe resolved IP accepted: %q", ip)
		}
	}
	if _, err := validateResearchURL("https://www.openssh.com/releasenotes.html"); err != nil {
		t.Fatal(err)
	}
	if !publicResearchIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
}

func TestResearchHostnameSuffixMatchesGateway(t *testing.T) {
	for _, host := range []string{"example.com", "a.example.com", "a.b.example.com", "EXAMPLE.COM."} {
		if !researchHostMatch(host, "*.example.com") {
			t.Fatal(host)
		}
	}
	for _, host := range []string{"notexample.com", "example.com.attacker.com"} {
		if researchHostMatch(host, "*.example.com") {
			t.Fatal(host)
		}
	}
}

func TestResearchHTMLTokenizerAndFullText(t *testing.T) {
	body := []byte(`<html><head><title>Vendor &amp; release</title><style>secret CSS</style></head><body><script>secret script</script><p>Unicode Türkçe ✓</p><p>` + strings.Repeat("advisory ", 2000) + `</p></body></html>`)
	e, err := normalizeResearchEvidence(researchCandidate{URL: "https://www.openssh.com/", Trust: researchTrustUnclassified}, body, "text/html", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Excerpt, "secret") || !strings.Contains(e.Excerpt, "Türkçe ✓") || len(e.Excerpt) < 12000 || e.Title != "Vendor & release" {
		t.Fatalf("bad extraction: title=%q length=%d", e.Title, len(e.Excerpt))
	}
}

func TestResearchDisabledDoesNotDial(t *testing.T) {
	if requireSandboxRuntime() != nil {
		t.Skip("real sandbox test required")
	}
	t.Setenv("YUMMI_WEB_SEARCH_ENABLED", "false")
	res, _, err := webSearchHandler(context.Background(), nil, webSearchArgs{Query: "OpenSSH advisory"})
	if err != nil || res == nil || !res.IsError {
		t.Fatalf("disabled search accepted: %v %v", res, err)
	}
}

func TestResearchLiteralOffsetUsesOriginalRunePosition(t *testing.T) {
	if got := researchLiteralOffset("İstanbul OpenSSH", "openssh"); got != 9 {
		t.Fatalf("Unicode case mapping changed original source offset: got %d, want 9", got)
	}
	if got := researchLiteralOffset("vendor evidence", "absent"); got != -1 {
		t.Fatalf("missing literal reported as found: %d", got)
	}
}
