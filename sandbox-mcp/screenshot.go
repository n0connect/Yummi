package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// screenshot_triage exists for the recon workflow's real bottleneck: once
// osint-enum.yaml's pipeline resolves a hundred-plus live subdomains, opening
// each one by hand to see what it actually is (an admin panel? a default
// nginx page? a real app?) is the slow, manual step a bug-bounty hunter ends
// up doing outside Yummi entirely. This wraps httpx's own headless-Chrome
// screenshot mode (-ss) — reusing the exact binary osint-enum.yaml's other
// steps already invoke, not a new dependency to bundle from scratch — so a
// resolved host list becomes a directory of screenshots plus the same
// status/title/tech triage httpx_probe already gives, in one call.
type screenshotTriageArgs struct {
	Target    string `json:"target" jsonschema:"absolute path to a file of URLs or hosts (one per line) to screenshot — e.g. osint-enum's resolved_hosts.txt or httpx_probe's own output_file"`
	OutputDir string `json:"output_dir" jsonschema:"absolute directory under /home/pentester/work/ to save screenshots into"`
}

func screenshotTriageHandler(ctx context.Context, _ *mcp.CallToolRequest, args screenshotTriageArgs) (*mcp.CallToolResult, any, error) {
	target := strings.TrimSpace(args.Target)
	if target == "" {
		return errResult("target is required")
	}
	if !filepath.IsAbs(target) {
		return errResult(fmt.Sprintf("target %q must be an absolute path to a file of hosts (one per line)", target))
	}

	outDir := strings.TrimSpace(args.OutputDir)
	if outDir == "" {
		return errResult("output_dir is required")
	}
	clean, reason := confineDir(outDir)
	if reason != "" {
		return errResult(fmt.Sprintf("output_dir %q %s", outDir, reason))
	}

	// -system-chrome: use the sandbox's own installed chromium rather than
	// httpx trying to download one at runtime, which the gateway's
	// fail-closed egress would refuse anyway. -srd is httpx's own
	// screenshot-response-dir flag. The rest mirrors httpx_probe's proven
	// triage flags (status/title/tech/server), so one pass gives both the
	// screenshots and the same text summary a subdomain-only probe would.
	argv := []string{
		"-l", target, "-silent",
		"-ss", "-system-chrome", "-srd", clean,
		"-sc", "-title", "-td", "-server", "-fr",
	}
	result, meta, err := runCaptured(ctx, "httpx", argv...)
	if err != nil || result == nil || len(result.Content) == 0 {
		return result, meta, err
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return result, meta, err
	}

	shots := listScreenshotFiles(clean)
	if len(shots) == 0 {
		text.Text += fmt.Sprintf("\n\n[no screenshot files found under %s — check that chromium is installed and the hosts actually responded over HTTP(S)]", clean)
	} else {
		text.Text += fmt.Sprintf("\n\n[%d screenshot(s) saved under %s]\n", len(shots), clean)
		for _, f := range shots {
			text.Text += "  - " + f + "\n"
		}
	}
	return result, meta, nil
}

// confineDir validates path is an absolute directory resolving inside
// sandboxHome (mirrors writeConfinedFile's confinement intent in capture.go,
// applied to a directory an external process — not sandbox-mcp itself —
// writes many files into, so there's no single []byte to hand to
// writeConfinedFile).
func confineDir(path string) (clean string, reason string) {
	if !filepath.IsAbs(path) {
		return "", "must be an absolute path"
	}
	clean = filepath.Clean(path)
	if !hasPathPrefix(clean, sandboxHome) {
		return "", fmt.Sprintf("must be under %s", sandboxHome)
	}
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return "", err.Error()
	}
	realDir, dirErr := filepath.EvalSymlinks(clean)
	realHome, homeErr := filepath.EvalSymlinks(sandboxHome)
	if dirErr == nil && homeErr == nil && !hasPathPrefix(realDir, realHome) {
		return "", fmt.Sprintf("resolves outside %s", sandboxHome)
	}
	return clean, ""
}

// listScreenshotFiles walks dir for image files, returned as paths relative
// to dir. Walked rather than assumed-flat because httpx's screenshot layout
// (flat files vs. a per-host subdirectory) isn't a stable contract across
// versions — reporting whatever image files actually landed is honest
// regardless of which layout the installed httpx uses.
func listScreenshotFiles(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg":
		default:
			return nil
		}
		if rel, relErr := filepath.Rel(dir, path); relErr == nil {
			out = append(out, rel)
		} else {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
