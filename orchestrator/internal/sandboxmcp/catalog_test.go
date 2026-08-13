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
