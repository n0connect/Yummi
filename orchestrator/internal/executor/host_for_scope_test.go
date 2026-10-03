package executor

import "testing"

func TestHostForScopeReducesURLsToHosts(t *testing.T) {
	cases := map[string]string{
		"http://198.51.100.10":                  "198.51.100.10",
		"http://198.51.100.10:8080/cgi-bin/x?y": "198.51.100.10",
		"https://lab.example.test/admin/":       "lab.example.test",
		"198.51.100.10":                         "198.51.100.10",
		"lab.example.test":                      "lab.example.test",
	}
	for in, want := range cases {
		if got := hostForScope(in); got != want {
			t.Errorf("hostForScope(%q) = %q, want %q", in, got, want)
		}
	}
}
