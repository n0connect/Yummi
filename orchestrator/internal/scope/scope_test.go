package scope

import "testing"

func TestExtractTargets(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{"several positional targets", "nmap 10.0.0.1 8.8.8.8 example.com", []string{"10.0.0.1", "8.8.8.8", "example.com"}},
		{"urls and duplicate", "curl https://example.com https://api.example.com/x https://example.com/y", []string{"example.com", "api.example.com"}},
		{"paths and counters ignored", "nmap -iL /workspace/targets.txt --top-ports 100", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTargets(tt.command)
			if len(got) != len(tt.want) {
				t.Fatalf("ExtractTargets(%q) = %v, want %v", tt.command, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("ExtractTargets(%q) = %v, want %v", tt.command, got, tt.want)
				}
			}
		})
	}
}

func TestExtractTarget(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		// Bare IPs and hostnames after flags.
		{"ip after flag", "nmap -sV 192.168.1.1", "192.168.1.1"},
		{"ip after valued flag", "nmap -sV -p 80 10.0.0.1", "10.0.0.1"},
		{"hostname after flag", "nikto -h example.com", "example.com"},
		{"top-ports counter ignored", "nmap -sV --top-ports 100 scanme.nmap.org", "scanme.nmap.org"},

		// URLs: host is extracted, scheme/path/port dropped.
		{"https host", "whatweb https://target.local", "target.local"},
		{"http host with path", "curl -s http://api.example.com/health", "api.example.com"},
		{"url ip with path", "ffuf -u http://10.10.10.1/FUZZ -w /usr/share/wordlists/common.txt", "10.10.10.1"},
		{"absolute tool path, url ip", "/usr/bin/nikto -h https://45.33.32.156", "45.33.32.156"},
		{"url with userinfo and port", "curl http://user:pass@10.0.0.7:8080/x", "10.0.0.7"},

		// Wordlist path must not win over the real target, regardless of order.
		{"wordlist before url", "ffuf -w /usr/share/wordlists/common.txt -u http://10.0.0.1/FUZZ", "10.0.0.1"},

		// --flag=value forms.
		{"equals url flag", "ffuf --url=http://10.0.0.9/FUZZ", "10.0.0.9"},
		{"equals host flag", "nikto --host=example.org", "example.org"},

		// CIDR target preserved.
		{"cidr", "nmap -sn 192.168.1.0/24", "192.168.1.0/24"},

		// IPv6 literal in a URL is unwrapped and accepted.
		{"ipv6 url literal", "curl http://[2001:db8::1]/x", "2001:db8::1"},

		// Octet bound check: out-of-range octets are NOT valid IPs, and with a
		// digits-only final label they are not valid hostnames either, so they
		// must not be returned as targets.
		{"octet over 255", "nmap 256.1.1.1", ""},
		{"all octets over 255", "nmap 999.999.999.999", ""},

		// Scope-confusion guard: an IP-shaped PREFIX of a longer hostname must
		// be treated as the (unresolvable-here, fail-closed) hostname, never
		// peeled off as the IP — otherwise the gateway would open an nftables
		// rule for the wrong host while the sandbox's DNS resolves elsewhere.
		{"ip prefix of hostname", "nmap 1.2.3.4.evil.com", "1.2.3.4.evil.com"},
		{"ip prefix of hostname in url", "curl http://1.2.3.4.evil.com/x", "1.2.3.4.evil.com"},

		// Quoted targets resolved.
		{"quoted ip", `curl "10.0.0.1"`, "10.0.0.1"},
		{"single-quoted hostname", `nikto -h 'example.org'`, "example.org"},
		{"quoted equals url flag", `ffuf --url="http://10.0.0.9/FUZZ"`, "10.0.0.9"},

		// Host:port targets without URL scheme.
		{"ip with port", "nikto -h 192.168.1.10:8080", "192.168.1.10"},
		{"hostname with port", "curl example.com:8443", "example.com"},
		{"ipv6 literal with port", "curl [2001:db8::1]:8080", "2001:db8::1"},
		{"invalid port number ignored", "curl example.com:99999", ""},
		{"port range not mistaken for target", "nmap -p 80:443 10.0.0.1", "10.0.0.1"},

		// No target.
		{"local only", "echo hello", ""},
		{"whoami", "whoami", ""},
		{"empty", "", ""},
		{"bare word no dot", "nmap localhost", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTarget(tt.command); got != tt.want {
				t.Errorf("ExtractTarget(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}
