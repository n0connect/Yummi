package sandboxmcp

import (
	"fmt"
	"strings"
)

// ToolSpec names the argument field that carries a cataloged tool's network
// target and how to render its structured arguments into a human-readable
// command line for the execution record, approval prompt, and audit trail.
type ToolSpec struct {
	TargetField string
	Render      func(args map[string]any) string
}

var catalog = map[string]ToolSpec{
	"nmap_scan":    {TargetField: "target", Render: renderNmap},
	"whatweb_scan": {TargetField: "url", Render: renderWhatweb},
	"curl_probe":   {TargetField: "url", Render: renderCurl},
	"gobuster_dir": {TargetField: "url", Render: renderGobuster},
	"ffuf_fuzz":    {TargetField: "url", Render: renderFfuf},
	"nikto_scan":   {TargetField: "host", Render: renderNikto},
}

// Target returns the network target named by tool's schema, or ("", false)
// for an unknown tool or a missing/empty target field.
func Target(tool string, args map[string]any) (string, bool) {
	spec, ok := catalog[tool]
	if !ok {
		return "", false
	}
	v, _ := args[spec.TargetField].(string)
	return v, v != ""
}

// RenderCommand turns args into a human-readable command line. Falls back to
// the bare tool name for an unrecognized tool.
func RenderCommand(tool string, args map[string]any) string {
	spec, ok := catalog[tool]
	if !ok {
		return tool
	}
	return spec.Render(args)
}

func renderNmap(a map[string]any) string {
	var parts []string
	if b, _ := a["service_detection"].(bool); b {
		parts = append(parts, "-sV")
	}
	if b, _ := a["default_scripts"].(bool); b {
		parts = append(parts, "-sC")
	}
	if p, _ := a["ports"].(string); p != "" {
		parts = append(parts, "-p", p)
	}
	target, _ := a["target"].(string)
	parts = append(parts, target)
	return "nmap " + strings.Join(parts, " ")
}

func renderWhatweb(a map[string]any) string {
	url, _ := a["url"].(string)
	return "whatweb " + url
}

func renderCurl(a map[string]any) string {
	var parts []string
	if b, _ := a["headers_only"].(bool); b {
		parts = append(parts, "-I")
	} else {
		parts = append(parts, "-i")
	}
	if b, _ := a["follow_redirects"].(bool); b {
		parts = append(parts, "-L")
	}
	url, _ := a["url"].(string)
	parts = append(parts, url)
	return "curl -s " + strings.Join(parts, " ")
}

func renderGobuster(a map[string]any) string {
	url, _ := a["url"].(string)
	wl, _ := a["wordlist"].(string)
	s := fmt.Sprintf("gobuster dir -u %s -w %s", url, wl)
	if ext, _ := a["extensions"].(string); ext != "" {
		s += " -x " + ext
	}
	return s
}

func renderFfuf(a map[string]any) string {
	url, _ := a["url"].(string)
	wl, _ := a["wordlist"].(string)
	return fmt.Sprintf("ffuf -u %s -w %s", url, wl)
}

func renderNikto(a map[string]any) string {
	host, _ := a["host"].(string)
	return "nikto -h " + host
}
