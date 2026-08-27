package api

import (
	"net/http"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// OperatorBriefing is what Yummi hands an operator who is about to work by
// hand: the accumulated intelligence — matching skill playbooks, procedural
// tactics that worked before, and findings already on record — surfaced FOR
// the operator to use, not folded into an agent prompt. This is the first
// concrete piece of the "operator drives, AI assists" direction: the same
// knowledge the AI conductor is fed at task start, an expert can now pull for
// their own manual workflow.
type OperatorBriefing struct {
	Goal     string              `json:"goal"`
	Tool     string              `json:"tool"`
	Skills   []skill.Skill       `json:"skills"`
	Tactics  []knowledge.Tactic  `json:"tactics"`
	Findings []knowledge.Finding `json:"findings"`
}

// HandleOperatorBriefing answers GET /api/v1/operator/briefing?goal=&tool=&session=.
// goal/tool steer which skills and tactics are relevant; session (optional)
// scopes the findings already recorded. Every field degrades to an empty list
// rather than erroring, so an operator can pull a briefing with as little as a
// tool name — before a goal is framed or a session is even chosen.
func HandleOperatorBriefing(reg *skill.Registry, ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		goal := strings.TrimSpace(r.URL.Query().Get("goal"))
		tool := strings.TrimSpace(r.URL.Query().Get("tool"))
		session := strings.TrimSpace(r.URL.Query().Get("session"))

		briefing := OperatorBriefing{
			Goal:     goal,
			Tool:     tool,
			Skills:   []skill.Skill{},
			Tactics:  []knowledge.Tactic{},
			Findings: []knowledge.Finding{},
		}

		// Skills: match on goal + tool the same way the agent's own skill
		// selection does (shared skill.Select), so the operator sees exactly
		// what the AI would have been given for the same intent.
		if reg != nil && (goal != "" || tool != "") {
			var tools []string
			if tool != "" {
				tools = []string{tool}
			}
			if selected := reg.Select(goal, tools, 5); selected != nil {
				briefing.Skills = selected
			}
		}

		// Tactics: tool-scoped when a tool is named (the operator's most
		// common entry point — "what has worked with nmap before"), else the
		// most relevant across all tools.
		if ks != nil {
			if tool != "" {
				if ts := ks.ListTactics(tool, 10); ts != nil {
					briefing.Tactics = ts
				}
			} else if ts := ks.ListAllTactics(10); ts != nil {
				briefing.Tactics = ts
			}
		}

		// Findings: only when a valid session is named. A malformed session
		// id is a client error (consistent with the other session-scoped
		// handlers), not a silently-empty result.
		if session != "" {
			if !isValidSessionID(session) {
				writeError(w, http.StatusBadRequest, "invalid session id format", nil)
				return
			}
			if ks != nil {
				if fs := ks.ListFindings(session); fs != nil {
					briefing.Findings = fs
				}
			}
		}

		writeJSON(w, http.StatusOK, briefing)
	}
}
