// Package invariants holds executable architectural fitness functions: tests
// that walk the orchestrator source tree and FAIL when a core security invariant
// is violated. Yummi's crown-jewel guarantees (single exec path, mcp dormancy,
// agent purity, no telemetry) were previously enforced only by CLAUDE.md prose
// and code review; these tests turn them into regression-proof, CI-enforced
// rules so a future change (or a transitive dependency) can't silently breach
// them. (Pattern adapted from openclaude's scripts/*.test.ts guards.)
package invariants

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// moduleRoot finds the orchestrator module root by walking up from this test
// file until it sees a go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test file")
		}
		dir = parent
	}
}

// goFiles returns every .go file under root (skipping vendor/testdata). When
// includeTests is false, *_test.go files are skipped.
func goFiles(t *testing.T, root string, includeTests bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return out
}

// importsOf returns the import paths of a Go file (imports-only parse).
func importsOf(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := make([]string, 0, len(f.Imports))
	for _, im := range f.Imports {
		out = append(out, strings.Trim(im.Path.Value, `"`))
	}
	return out
}

func relSlash(root, path string) string {
	rel, _ := filepath.Rel(root, path)
	return filepath.ToSlash(rel)
}

// TestExecImportAllowlist enforces the single-exec-path invariant: os/exec may be
// imported ONLY by the audited shell-boundary files. Every other production path
// must reach a shell through sandbox.Bridge.Exec. A new os/exec import elsewhere —
// especially anything agent-reachable — is the single most dangerous regression
// class for a tool that runs attacker-influenced commands.
func TestExecImportAllowlist(t *testing.T) {
	root := moduleRoot(t)
	allow := map[string]bool{
		"internal/sandbox/bridge.go":    true, // the one production shell path (docker exec)
		"internal/api/terminal.go":      true, // operator interactive terminal (also docker exec)
		"internal/mcp/client.go":        true, // dormant; spawns an MCP server on the host (not imported by prod — see TestMCPRemainsDormant)
		"internal/sandboxmcp/client.go": true, // persistent docker exec -i into the sandbox-resident MCP server — see TestSandboxMCPOnlyExecsDocker below, which locks its exec calls to literal "docker" only
	}
	seen := map[string]bool{}
	for _, f := range goFiles(t, root, false) {
		rel := relSlash(root, f)
		for _, imp := range importsOf(t, f) {
			if imp == "os/exec" {
				seen[rel] = true
				if !allow[rel] {
					t.Errorf("os/exec imported outside the allowlist: %s\n"+
						"  Every shell path must go through sandbox.Bridge.Exec (single-exec-path invariant).\n"+
						"  If this is a genuinely operator-side, non-agent-reachable path, add it to the allowlist WITH justification.", rel)
				}
			}
		}
	}
	// Allowlist hygiene: an entry that no longer imports os/exec is stale and
	// should be removed, so the allowlist never grants standing permission to a
	// file that doesn't need it.
	for rel := range allow {
		if !seen[rel] {
			t.Errorf("stale allowlist entry %q no longer imports os/exec — remove it from the allowlist.", rel)
		}
	}
}

// TestMCPRemainsDormant locks in the documented dormancy of internal/mcp: it can
// spawn a host-side MCP server (an exec path outside the sandbox boundary), so it
// must not be imported by any production code until that path is made safe.
func TestMCPRemainsDormant(t *testing.T) {
	root := moduleRoot(t)
	const mcpPkg = "github.com/n0connect/yummi/orchestrator/internal/mcp"
	for _, f := range goFiles(t, root, false) {
		rel := relSlash(root, f)
		if strings.HasPrefix(rel, "internal/mcp/") {
			continue // the package's own files
		}
		for _, imp := range importsOf(t, f) {
			if imp == mcpPkg {
				t.Errorf("internal/mcp imported by production file %s — it must stay dormant\n"+
					"  (it can spawn host-side processes, violating the single-exec-path invariant).", rel)
			}
		}
	}
}

// TestAgentNeverImportsOSExec asserts the agent package never shells out itself:
// it enqueues into execution.Store, which the executor drains through the gateway
// scope check and sandbox exec. A direct os/exec import in internal/agent would
// give LLM-driven output a host-side command path.
func TestAgentNeverImportsOSExec(t *testing.T) {
	root := moduleRoot(t)
	for _, f := range goFiles(t, root, false) {
		rel := relSlash(root, f)
		if !strings.HasPrefix(rel, "internal/agent/") {
			continue
		}
		for _, imp := range importsOf(t, f) {
			if imp == "os/exec" {
				t.Errorf("internal/agent must not import os/exec (%s): the agent never shells out — it enqueues into execution.Store.", rel)
			}
		}
	}
}

// TestEinoBackendNeverImportsOSExec asserts the Eino agentic backend never
// shells out itself, mirroring TestAgentNeverImportsOSExec for the graph
// engine — every tool (tools.go) reaches a shell exclusively through the
// same agent.Execer interface the graph engine uses, which is itself
// audited by TestExecImportAllowlist above. This is already covered by
// TestAgentNeverImportsOSExec's "internal/agent/" prefix walk (eino/ is a
// subdirectory), but is asserted explicitly and by name so the invariant is
// self-documenting rather than relying on the reader noticing the walk is
// broader than its name suggests.
func TestEinoBackendNeverImportsOSExec(t *testing.T) {
	root := moduleRoot(t)
	for _, f := range goFiles(t, root, false) {
		rel := relSlash(root, f)
		if !strings.HasPrefix(rel, "internal/agent/eino/") {
			continue
		}
		for _, imp := range importsOf(t, f) {
			if imp == "os/exec" {
				t.Errorf("internal/agent/eino must not import os/exec (%s): tools reach a shell only through agent.Execer.", rel)
			}
		}
	}
}

// TestExecutionProcessorDoesNotInjectRawOutputIntoChatHistory locks the
// content-security boundary between sandbox execution and the conductor. Raw
// target output must first pass execution threat review and then enter chat
// through the typed from_feedback route; main must not append it eagerly.
func TestExecutionProcessorDoesNotInjectRawOutputIntoChatHistory(t *testing.T) {
	root := moduleRoot(t)
	path := filepath.Join(root, "cmd", "orchestrator", "main.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "AppendObservation" {
			t.Errorf("main eagerly calls AppendObservation at %s; raw output must enter through reviewed from_feedback only", fset.Position(call.Pos()))
		}
		return true
	})
}

// TestNoTelemetryEndpoints backs the privacy-first claim: no analytics/telemetry
// phone-home endpoint may appear anywhere in source (incl. tests). (openclaude's
// verify-no-phone-home, as a source-level guard.)
func TestNoTelemetryEndpoints(t *testing.T) {
	root := moduleRoot(t)
	banned := []string{
		"datadoghq.com",
		"api.posthog.com",
		"api.mixpanel.com",
		"google-analytics.com",
		"sentry.io",
		"api.amplitude.com",
		"api.segment.io",
	}
	for _, f := range goFiles(t, root, true) {
		rel := relSlash(root, f)
		if strings.HasSuffix(rel, "invariants_test.go") {
			continue // this guard file names the patterns it forbids
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		s := string(b)
		for _, bad := range banned {
			if strings.Contains(s, bad) {
				t.Errorf("telemetry/analytics endpoint %q found in %s — Yummi is privacy-first (no phone-home).", bad, rel)
			}
		}
	}
}

// TestSandboxMCPOnlyExecsDocker locks internal/sandboxmcp to a narrow
// exception in the single-exec-path invariant: it may run `docker exec -i`
// to reach the sandbox-resident MCP server, but every call site's first
// argument must be the literal string "docker" — it must never be able to
// run an operator/agent/model-controlled binary on the host.
func TestSandboxMCPOnlyExecsDocker(t *testing.T) {
	fset := token.NewFileSet()
	dir := filepath.Join(moduleRoot(t), "internal", "sandboxmcp")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
				return true
			}
			// Command(name, ...) vs CommandContext(ctx, name, ...): the binary
			// name is the first arg after any leading context.Context arg.
			argIdx := 0
			if sel.Sel.Name == "CommandContext" {
				argIdx = 1
			}
			if len(call.Args) <= argIdx {
				return true
			}
			lit, ok := call.Args[argIdx].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: exec.%s's binary argument must be a literal, got a non-literal expression", path, sel.Sel.Name)
				return true
			}
			if lit.Value != `"docker"` {
				t.Errorf("%s: exec.%s must run literal \"docker\", got %s", path, sel.Sel.Name, lit.Value)
			}
			return true
		})
	}
}
