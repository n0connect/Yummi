package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// passiveReconSources is the full default source set for passive_recon_batch
// — matches osint-enum.yaml's own step 1 passive-source list exactly, so the
// batch tool is a drop-in faster alternative to four separate sequential
// tool calls (and four separate operator approvals), not a different
// methodology.
var passiveReconSources = []string{"subfinder", "amass", "theharvester", "gau"}

// passive_recon_batch exists because subfinder/amass/theHarvester/gau are
// each independent and read-only — none depends on another's result — yet
// the only way to run them was one sequential MCP call (and one operator
// approval) per source, live-observed taking several minutes of pure
// wall-clock waiting for a phase that has no real ordering dependency at
// all. This runs all requested sources concurrently in ONE call/approval
// and reports each one's own success/failure independently — a slow or
// failing source (theHarvester timing out is common) never blocks or hides
// the others, matching the same multi-source-resilience philosophy the
// individual tools and osint-enum.yaml's methodology already rely on.
type passiveReconBatchArgs struct {
	Domain    string   `json:"domain" jsonschema:"the target domain to enumerate"`
	Sources   []string `json:"sources,omitempty" jsonschema:"which passive sources to run concurrently: subfinder, amass, theharvester, gau (default: all four)"`
	OutputDir string   `json:"output_dir" jsonschema:"absolute directory to write each source's raw output to as subs_<source>.txt (e.g. subs_subfinder.txt) — sandbox-mcp's own working directory is fixed, so this must be an absolute path"`
}

type passiveReconResult struct {
	source string
	text   string
	isErr  bool
}

func passiveReconBatchHandler(ctx context.Context, _ *mcp.CallToolRequest, args passiveReconBatchArgs) (*mcp.CallToolResult, any, error) {
	domain := strings.TrimSpace(args.Domain)
	if domain == "" {
		return errResult("domain is required")
	}
	if err := rejectIfFlagLike("domain", domain); err != nil {
		return errResult(err.Error())
	}
	outputDir := strings.TrimSpace(args.OutputDir)
	if outputDir == "" {
		return errResult("output_dir is required")
	}
	if !filepath.IsAbs(outputDir) {
		return errResult(fmt.Sprintf("output_dir %q must be an absolute path", outputDir))
	}

	runSet, err := resolvePassiveReconSources(args.Sources)
	if err != nil {
		return errResult(err.Error())
	}

	results := make([]passiveReconResult, len(runSet))
	var wg sync.WaitGroup
	for i, source := range runSet {
		wg.Add(1)
		go func(i int, source string) {
			defer wg.Done()
			results[i] = runPassiveReconSource(ctx, source, domain, outputDir)
		}(i, source)
	}
	wg.Wait()

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: formatPassiveReconResults(domain, results)}}}, nil, nil
}

// resolvePassiveReconSources validates and de-duplicates the requested
// source list, defaulting to every known source when none is given.
func resolvePassiveReconSources(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return passiveReconSources, nil
	}
	knownSet := make(map[string]bool, len(passiveReconSources))
	for _, s := range passiveReconSources {
		knownSet[s] = true
	}
	seen := make(map[string]bool, len(requested))
	var runSet []string
	for _, s := range requested {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		if !knownSet[s] {
			return nil, fmt.Errorf("unknown source %q — must be one of subfinder, amass, theharvester, gau", s)
		}
		seen[s] = true
		runSet = append(runSet, s)
	}
	if len(runSet) == 0 {
		return nil, fmt.Errorf("sources must name at least one known passive source")
	}
	return runSet, nil
}

// runPassiveReconSource execs exactly the same command each source's own
// individual handler (subfinderHandler, amassHandler, theharvesterHandler,
// gauHandler) already builds — this function exists to run one of them
// concurrently with the others, not to diverge from their behavior.
func runPassiveReconSource(ctx context.Context, source, domain, outputDir string) passiveReconResult {
	var result *mcp.CallToolResult
	var err error
	switch source {
	case "subfinder":
		result, _, err = runCaptured(ctx, "subfinder", "-d", domain, "-silent")
	case "amass":
		result, _, err = runCaptured(ctx, amassBinary, "enum", "-d", domain, "-passive")
	case "theharvester":
		result, _, err = runCaptured(ctx, "theHarvester", "-d", domain, "-b", "all")
	case "gau":
		result, _, err = runCaptured(ctx, "gau", "--subs", "--threads", "4", "--timeout", "10", domain)
	}
	text, isErr := "", err != nil
	if err != nil {
		text = err.Error()
	} else if result != nil {
		isErr = result.IsError
		if len(result.Content) > 0 {
			if t, ok := result.Content[0].(*mcp.TextContent); ok {
				text = t.Text
			}
		}
	}
	text += saveNote(filepath.Join(outputDir, "subs_"+source+".txt"), text)
	return passiveReconResult{source: source, text: text, isErr: isErr}
}

// formatPassiveReconResults reports every source's own outcome. The whole
// call is only IsError-worthy in spirit (still returned as a normal,
// non-error MCP result either way — see the caller) when every single
// source failed; a partial failure is the expected, tolerated shape this
// whole methodology already treats as normal, not a batch failure.
func formatPassiveReconResults(domain string, results []passiveReconResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ran %d passive source(s) concurrently against %s:\n\n", len(results), domain)
	for _, r := range results {
		status := "ok"
		if r.isErr {
			status = "FAILED"
		}
		fmt.Fprintf(&b, "=== %s [%s] ===\n%s\n\n", r.source, status, r.text)
	}
	return b.String()
}

func registerPassiveReconTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "passive_recon_batch",
		Description: "Run multiple passive subdomain-enumeration sources (subfinder, amass, theHarvester, gau) " +
			"CONCURRENTLY against one domain in a single tool call, instead of one sequential call (and approval) per source.",
	}, passiveReconBatchHandler)
}
