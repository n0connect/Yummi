package eino

import (
	"context"
	"fmt"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// Context-window overflow handling for the Eino backend.
//
// Eino's ChatModelAgent keeps the whole tool-calling history inside the ADK, so
// nothing here can truncate it. The recovery is therefore a segment restart —
// the same mechanism the transient-error retry uses — with the prior work
// carried forward as a compacted summary instead of a raw dump. A raw dump of
// every earlier result (the old carry-forward) grows with the task and can
// itself overflow the window it was meant to recover from.

const (
	// workLogHeadPairs / workLogTailPairs bound the carry-forward work log: the
	// first pairs establish topology, the most recent ones show current state.
	workLogHeadPairs = 3
	workLogTailPairs = 17
	// workLogResultRunes bounds each result carried forward.
	workLogResultRunes = 300
	// recoverySourceRunes bounds what the compaction model is asked to summarize.
	recoverySourceRunes = 12000
	// maxOverflowRecoveries bounds segment restarts caused by overflow per task.
	maxOverflowRecoveries = 2
)

// boundedWorkLog renders prior tool calls for a carry-forward message: at most
// workLogHeadPairs + workLogTailPairs pairs, each result cut to
// workLogResultRunes, with the omitted middle counted, never silently dropped.
// Empty when there are no pairs.
func boundedWorkLog(pairs []tracePair) string {
	if len(pairs) == 0 {
		return ""
	}
	keep := pairs
	omitted := 0
	if limit := workLogHeadPairs + workLogTailPairs; len(pairs) > limit {
		omitted = len(pairs) - limit
		keep = append(append([]tracePair{}, pairs[:workLogHeadPairs]...), pairs[len(pairs)-workLogTailPairs:]...)
	}
	var b strings.Builder
	for i, p := range keep {
		if omitted > 0 && i == workLogHeadPairs {
			fmt.Fprintf(&b, "  - … %d middle tool calls omitted for length\n", omitted)
		}
		fmt.Fprintf(&b, "  - %s: %s\n", p.Tool, truncateRunes(strings.TrimSpace(agent.CompressObservation(p.Result)), workLogResultRunes))
	}
	return b.String()
}

// compactTraceForRecovery produces the compacted prior-work summary a
// recovered segment starts from. With a compaction model available it asks for
// a consolidated summary (preserving targets, ports, credentials, paths,
// vulnerabilities — the same brief graph's summarizeNode uses); without one, or
// if that call fails, it falls back to the bounded work log so recovery still
// proceeds deterministically.
func compactTraceForRecovery(ctx context.Context, llm agent.LLM, trace *runTrace) string {
	pairs := trace.pairs()
	fallback := boundedWorkLog(pairs)
	if llm == nil || len(pairs) == 0 {
		return fallback
	}
	var src strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&src, "- %s: %s\n", p.Tool, truncateRunes(strings.TrimSpace(p.Result), 600))
	}
	summary, err := llm.Chat(ctx,
		"You are a penetration testing assistant. Summarize prior tool results concisely.",
		"Combine these prior tool results into one consolidated summary of at most 400 words. "+
			"Keep every concrete fact an operator would need to continue: target addresses, open ports, "+
			"service versions, credentials, paths and URLs, vulnerabilities, and what was already ruled out.\n\n"+
			truncateRunes(src.String(), recoverySourceRunes))
	if err != nil || strings.TrimSpace(summary) == "" {
		return fallback
	}
	return strings.TrimSpace(summary)
}

// buildOverflowRecoveryMessage is the opening message for a segment restarted
// after a context-window overflow.
func buildOverflowRecoveryMessage(goal, compacted string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Continuing toward the original goal: %s\n\n", goal)
	b.WriteString("The previous attempt reached the model's context window limit, so its conversation was compacted. ")
	b.WriteString("Prior work is summarized below: treat it as already done and do NOT repeat it.\n\n")
	b.WriteString("COMPACTED PRIOR WORK:\n")
	b.WriteString(compacted)
	b.WriteString("\n\nContinue from here toward the goal, using fewer, more targeted tool calls.")
	return b.String()
}

func truncateRunes(s string, max int) string {
	if rs := []rune(s); len(rs) > max {
		return string(rs[:max]) + "…"
	}
	return s
}
