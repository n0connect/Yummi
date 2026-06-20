package agent

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

// AddObservation records a full tool output and updates the rough token count.
func (m *MemoryContext) AddObservation(obs string) {
	m.Observations = append(m.Observations, obs)
	m.TokensUsed += EstimateTokens(obs)
}
