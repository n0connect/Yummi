package scope

import "testing"

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
