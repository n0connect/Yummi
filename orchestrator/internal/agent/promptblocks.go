package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// FormatDelegationRoles renders the "Available roles" fragment shared by both
// agent backends' DELEGATION prompt block (node_step.go's buildDelegation for
// graph, eino/context_blocks.go's buildDelegationBlock for Eino) — the two
// backends differ only in which delegation MECHANISM they instruct the model
// to use (a [HANDOFF] tag vs a native tool call), never in how the role list
// itself is formatted, so that part lives here once. hints maps a role name
// to its optional SubAgentDef.RoutingHint; a role missing from hints (or with
// an empty hint) renders as a bare name. Returns "" when subAgents is empty.
func FormatDelegationRoles(subAgents map[string]*Runner, hints map[string]string) string {
	if len(subAgents) == 0 {
		return ""
	}
	roles := make([]string, 0, len(subAgents))
	for r := range subAgents {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	var b strings.Builder
	b.WriteString("Available roles:\n")
	for _, r := range roles {
		if hint := strings.TrimSpace(hints[r]); hint != "" {
			fmt.Fprintf(&b, "  - %s: %s\n", r, hint)
		} else {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	return b.String()
}

// FormatMemoryItems renders the MEMORY prompt block shared by both agent
// backends (node_step.go's buildMemory for graph, eino/context_blocks.go's
// buildMemoryBlock for Eino) — only the retrieval query that produces items
// differs between them (graph re-queries every step with tool/exclude
// filters built from command history; Eino queries once up front, before any
// tool calls exist to filter on). Returns "" for no items.
func FormatMemoryItems(items []knowledge.MemoryItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("MEMORY (prior knowledge — use it, don't rediscover):\n")
	for _, it := range items {
		fmt.Fprintf(&b, "  • %s\n", it.Text)
	}
	return b.String()
}
