package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

// agent_terminals.go: a watch-only view of the terminals the AGENT opens in
// the sandbox (term_open/term_run/...). The operator can read them; the
// operator can never write to them. Control stays entirely with the agent.
//
// Write-enforcement lives in one place: agentTerminalCall accepts only the
// two read verbs below. term_run, term_keys, term_wait, term_close and
// term_open are deliberately NOT reachable from this file, so an operator
// request cannot type into or close a session.

// agentTerminalCaller is the orchestrator's sandbox MCP client. Only
// CallTool is used.
type agentTerminalCaller interface {
	CallTool(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error)
}

var (
	agentTerminalReadVerbs = map[string]bool{"term_list": true, "term_read": true}
	agentTerminalIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

const (
	agentTerminalDefaultLines = 200
	agentTerminalMaxLines     = 1000
)

// allowedAgentTerminalVerb reports whether a tool may be called from the
// operator-facing terminal view.
func allowedAgentTerminalVerb(tool string) bool {
	return agentTerminalReadVerbs[tool]
}

// agentTerminalCall is the single chokepoint for every sandbox call made by
// this view. A non-read verb is refused before any MCP traffic.
func agentTerminalCall(ctx context.Context, c agentTerminalCaller, tool string, args map[string]any) (string, error) {
	if !allowedAgentTerminalVerb(tool) {
		return "", fmt.Errorf("terminal view is read-only: %q is not permitted", tool)
	}
	if c == nil {
		return "", fmt.Errorf("sandbox terminal client not configured")
	}
	res, err := c.CallTool(ctx, tool, args)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	if res.IsError {
		return "", fmt.Errorf("%s", strings.TrimSpace(b.String()))
	}
	return b.String(), nil
}

// AgentTerminal is one open agent-owned session as the UI shows it.
type AgentTerminal struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Windows string `json:"windows"`
	Size    string `json:"size"`
	Created string `json:"created"`
}

// parseTermList turns term_list's text rows back into structured entries.
// Row shape (sandbox-mcp/terminal.go): id  Nw  WxH  created  [label=...].
// The "no open sessions" text yields an empty list.
func parseTermList(out string) []AgentTerminal {
	terms := []AgentTerminal{} // never nil: the UI reads .length on this
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || strings.HasPrefix(line, "no open") {
			continue
		}
		term := AgentTerminal{ID: fields[0], Windows: fields[1], Size: fields[2], Created: strings.Join(fields[3:], " ")}
		if n := len(fields); n > 4 && strings.HasPrefix(fields[n-1], "label=") {
			term.Label = strings.TrimPrefix(fields[n-1], "label=")
			term.Created = strings.Join(fields[3:n-1], " ")
		}
		terms = append(terms, term)
	}
	return terms
}

// HandleListAgentTerminals lists the agent's open terminals.
func HandleListAgentTerminals(c agentTerminalCaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := agentTerminalCall(r.Context(), c, "term_list", map[string]any{})
		if err != nil {
			writeError(w, http.StatusBadGateway, "terminal list failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"terminals": parseTermList(out)})
	}
}

// HandleReadAgentTerminal returns the tail of one agent terminal's screen.
// Output is ANSI-stripped for display. The model-facing threat scan does not
// apply here because this response never enters model context; it is
// scanned only so a suspicious payload is at least flagged to the operator.
func HandleReadAgentTerminal(c agentTerminalCaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if !agentTerminalIDPattern.MatchString(id) {
			writeError(w, http.StatusBadRequest, "invalid terminal id", nil)
			return
		}
		lines := agentTerminalDefaultLines
		if q := r.URL.Query().Get("lines"); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || n < 1 {
				writeError(w, http.StatusBadRequest, "invalid lines", nil)
				return
			}
			if n > agentTerminalMaxLines {
				n = agentTerminalMaxLines
			}
			lines = n
		}
		out, err := agentTerminalCall(r.Context(), c, "term_read", map[string]any{"id": id, "lines": lines})
		if err != nil {
			writeError(w, http.StatusBadGateway, "terminal read failed", err)
			return
		}
		clean := threatscan.StripANSI(out)
		flag := threatscan.FirstThreatMessage(clean, threatscan.ScopeAll)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "output": clean, "flagged": flag != "", "flag_reason": flag})
	}
}
