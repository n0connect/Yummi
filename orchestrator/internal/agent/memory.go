package agent

import "fmt"

// maxRetainedObservation caps the bytes any single observation contributes to
// the in-memory history. A command can return a multi-megabyte dump (a large
// file read, gobuster on a big wordlist); retaining every byte for the SEND_FULL
// buffer and summarization input would grow unbounded and pressure memory. The
// edges of tool output are the dense part (headers at the top, results at the
// bottom), so an over-cap observation is kept as head+tail with a marker — the
// raw bytes still live in the execution store's audit record. 256 KB is far
// above any normal observation yet bounds the pathological case.
const maxRetainedObservation = 256 * 1024

// MemoryContext manages token pressure during task execution.
// When TokensUsed exceeds 80% of TokenBudget, the graph routes to
// SummarizeNode to compress observation history before the next step.
type MemoryContext struct {
	TokensUsed   int      `json:"tokens_used"`
	TokenBudget  int      `json:"token_budget"`
	Observations []string `json:"observations,omitempty"` // full outputs, cleared after summarization
	Summary      string   `json:"summary,omitempty"`      // compressed history for prompt injection
}

// NeedsCompression reports whether the token budget is ≥80% consumed.
func (m *MemoryContext) NeedsCompression() bool {
	return m.TokenBudget > 0 && m.TokensUsed >= int(float64(m.TokenBudget)*0.8)
}

// AddObservation records a tool output and updates the rough token count. An
// observation larger than maxRetainedObservation is capped to head+tail so a
// single pathological output can't grow the history without bound.
func (m *MemoryContext) AddObservation(obs string) {
	obs = capObservation(obs)
	m.Observations = append(m.Observations, obs)
	m.TokensUsed += EstimateTokens(obs)
}

// capObservation bounds a single observation's retained size, preserving the
// dense head and tail with a byte-count marker in the middle.
func capObservation(obs string) string {
	if len(obs) <= maxRetainedObservation {
		return obs
	}
	const edge = maxRetainedObservation / 2
	head := obs[:edge]
	tail := obs[len(obs)-edge:]
	return fmt.Sprintf("%s\n...[observation capped: %d bytes total, middle elided]...\n%s",
		head, len(obs), tail)
}
