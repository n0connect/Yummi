package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// consolidate_hosts is the one tool in this catalog that execs nothing at
// all — pure local file I/O and regex validation. It exists because every
// recon methodology's "merge every source into one de-duplicated candidate
// list" step was, until now, something the model reconstructed by hand as a
// raw shell awk/grep/sort pipeline every single run: fragile (a live session
// mistook amass's own progress-bar/banner lines for real subdomain data
// because the ad-hoc regex didn't filter them), and a repeat of the exact
// same logic that belongs in one well-tested place instead. TargetField is
// deliberately "" in the catalog (see sandboxmcp.NeedsTarget) — there is
// nothing here for the gateway to scope-check.
type consolidateArgs struct {
	InputFiles         []string `json:"input_files" jsonschema:"absolute paths to merge and de-duplicate, one hostname per line each — a missing or unreadable file is skipped and noted in the summary, not a hard failure"`
	Domain             string   `json:"domain" jsonschema:"the apex domain — only lines that are the bare domain itself or a *.<domain> subdomain are kept, filtering out banner/progress-bar noise mixed into any input file"`
	OutputFile         string   `json:"output_file" jsonschema:"absolute path to write the de-duplicated, validated result to"`
	HighConfidenceOnly bool     `json:"high_confidence_only,omitempty" jsonschema:"also drop machine-generated-looking names (very short, all-numeric, or unusually long leftmost label) — a secondary triage filter on top of domain-suffix validity, not a correctness requirement"`
}

func consolidateHandler(_ context.Context, _ *mcp.CallToolRequest, args consolidateArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.ToLower(strings.TrimSpace(args.Domain))
	if domain == "" {
		return errResult("domain is required")
	}
	if err := rejectIfFlagLike("domain", domain); err != nil {
		return errResult(err.Error())
	}
	if len(args.InputFiles) == 0 {
		return errResult("input_files is required")
	}
	outputFile := strings.TrimSpace(args.OutputFile)
	if outputFile == "" {
		return errResult("output_file is required")
	}

	// Matches the bare apex ("example.com") or any depth of subdomain
	// ("a.example.com", "a.b.example.com") — each label RFC1035-shaped
	// (alphanumeric, internal hyphens only, max 63 chars).
	pattern, err := regexp.Compile(`^(([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+)?` + regexp.QuoteMeta(domain) + `$`)
	if err != nil {
		return errResult(fmt.Sprintf("invalid domain %q", domain))
	}

	seen := make(map[string]struct{})
	var linesRead, dropped int
	var unreadable []string
	for _, path := range args.InputFiles {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			unreadable = append(unreadable, fmt.Sprintf("%s (not an absolute path)", path))
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s (%v)", path, readErr))
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.ToLower(strings.TrimSpace(line))
			if line == "" {
				continue
			}
			linesRead++
			if !pattern.MatchString(line) || (args.HighConfidenceOnly && !looksHighConfidence(line)) {
				dropped++
				continue
			}
			seen[line] = struct{}{}
		}
	}

	unique := make([]string, 0, len(seen))
	for h := range seen {
		unique = append(unique, h)
	}
	sort.Strings(unique)

	clean, reason := writeConfinedFile(outputFile, []byte(strings.Join(unique, "\n")+"\n"))
	if reason != "" {
		return errResult(fmt.Sprintf("output_file %q was not written: %s", outputFile, reason))
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: consolidateSummary(args, domain, clean, linesRead, len(unique), dropped, unreadable, unique)}}}, nil, nil
}

// looksHighConfidence rejects the same class of machine-generated-looking
// names a live agent session's own hand-written awk filter used: a leftmost
// label under 3 chars or made entirely of digits reads as noise (hash
// fragments, sequence numbers) rather than a deliberately chosen name, and
// an overall line past 70 chars is unusually long for a real-world hostname.
func looksHighConfidence(hostname string) bool {
	if len(hostname) >= 70 {
		return false
	}
	leftmost := hostname
	if i := strings.Index(hostname, "."); i >= 0 {
		leftmost = hostname[:i]
	}
	if len(leftmost) < 3 {
		return false
	}
	for _, r := range leftmost {
		if r < '0' || r > '9' {
			return true
		}
	}
	return false // every rune was a digit
}

func consolidateSummary(args consolidateArgs, domain, outputFile string, linesRead, uniqueCount, dropped int, unreadable, unique []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Consolidated %d input file(s) into %s\n", len(args.InputFiles), outputFile)
	fmt.Fprintf(&b, "  lines read: %d\n", linesRead)
	fmt.Fprintf(&b, "  valid *.%s hostnames (unique): %d\n", domain, uniqueCount)
	filterNote := ""
	if args.HighConfidenceOnly {
		filterNote = " or below the high-confidence bar"
	}
	fmt.Fprintf(&b, "  dropped (not a valid *.%s hostname%s): %d\n", domain, filterNote, dropped)
	if len(unreadable) > 0 {
		fmt.Fprintf(&b, "  unreadable input file(s): %s\n", strings.Join(unreadable, "; "))
	}
	if len(unique) > 0 {
		n := min(10, len(unique))
		fmt.Fprintf(&b, "  sample: %s", strings.Join(unique[:n], ", "))
	}
	return b.String()
}

func registerConsolidateTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "consolidate_hosts",
		Description: "Merge multiple hostname list files into one de-duplicated, domain-validated list — replaces a hand-written union/dedupe/regex shell pipeline.",
	}, consolidateHandler)
}
