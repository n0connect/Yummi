package sandboxmcp

import "testing"

func TestTarget(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want string
		ok   bool
	}{
		{"nmap_scan", map[string]any{"target": "10.0.0.5"}, "10.0.0.5", true},
		{"nmap_scan", map[string]any{"target": ""}, "", false},
		{"gobuster_dir", map[string]any{"url": "http://x"}, "http://x", true},
		{"unknown_tool", map[string]any{"target": "x"}, "", false},
	}
	for _, c := range cases {
		got, ok := Target(c.tool, c.args)
		if got != c.want || ok != c.ok {
			t.Errorf("Target(%q, %v) = (%q, %v), want (%q, %v)", c.tool, c.args, got, ok, c.want, c.ok)
		}
	}
}

func TestRenderCommand(t *testing.T) {
	got := RenderCommand("nmap_scan", map[string]any{
		"target": "10.0.0.5", "service_detection": true, "default_scripts": true, "ports": "1-1000",
	})
	want := "nmap -sV -sC -p 1-1000 10.0.0.5"
	if got != want {
		t.Errorf("RenderCommand = %q, want %q", got, want)
	}
}

func TestRenderCommand_UnknownTool(t *testing.T) {
	if got := RenderCommand("nope", nil); got != "nope" {
		t.Errorf("RenderCommand(unknown) = %q, want tool name unchanged", got)
	}
}

func TestExtendedCatalog_TargetAndRender(t *testing.T) {
	cases := []struct {
		tool       string
		args       map[string]any
		wantTarget string
		wantRender string
	}{
		{"amass_enum", map[string]any{"domain": "example.com", "passive": true}, "example.com", "amass enum -d example.com -passive"},
		{"subfinder_enum", map[string]any{"domain": "example.com"}, "example.com", "subfinder -d example.com -silent"},
		{"theharvester_scan", map[string]any{"domain": "example.com"}, "example.com", "theHarvester -d example.com -b all"},
		{"dnsenum_scan", map[string]any{"domain": "example.com"}, "example.com", "dnsenum example.com"},
		{"enum4linux_scan", map[string]any{"target": "10.0.0.5"}, "10.0.0.5", "enum4linux -a 10.0.0.5"},
		{"onesixtyone_scan", map[string]any{"target": "10.0.0.5"}, "10.0.0.5", "onesixtyone 10.0.0.5"},
		{"wafw00f_scan", map[string]any{"url": "http://x"}, "http://x", "wafw00f http://x"},
		{"wpscan_scan", map[string]any{"url": "http://x"}, "http://x", "wpscan --url http://x"},
		{"nuclei_scan", map[string]any{"target": "http://x"}, "http://x", "nuclei -u http://x"},
		{"sqlmap_scan", map[string]any{"url": "http://x?id=1"}, "http://x?id=1", "sqlmap -u http://x?id=1 --batch"},
		{"dirb_scan", map[string]any{"url": "http://x"}, "http://x", "dirb http://x"},
		{"dirb_scan", map[string]any{"url": "http://x", "wordlist": "common.txt"}, "http://x", "dirb http://x common.txt"},
		{
			"hydra_bruteforce",
			map[string]any{"target": "10.0.0.5", "service": "ssh", "user": "admin", "pass_list": "rockyou.txt"},
			"10.0.0.5", "hydra -l admin -P rockyou.txt 10.0.0.5 ssh",
		},
		{
			"medusa_bruteforce",
			map[string]any{"target": "10.0.0.5", "module": "ssh", "user_list": "users.txt", "pass_list": "rockyou.txt"},
			"10.0.0.5", "medusa -h 10.0.0.5 -U users.txt -P rockyou.txt -M ssh",
		},
	}
	for _, c := range cases {
		gotTarget, ok := Target(c.tool, c.args)
		if !ok || gotTarget != c.wantTarget {
			t.Errorf("%s: Target = (%q, %v), want (%q, true)", c.tool, gotTarget, ok, c.wantTarget)
		}
		if gotRender := RenderCommand(c.tool, c.args); gotRender != c.wantRender {
			t.Errorf("%s: RenderCommand = %q, want %q", c.tool, gotRender, c.wantRender)
		}
	}
}
