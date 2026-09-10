package knowledge

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ScopeRuleKind is either an include (whitelist) or exclude (blocklist)
// directive from a bug-bounty program's published scope.
type ScopeRuleKind string

const (
	ScopeInclude ScopeRuleKind = "include"
	ScopeExclude ScopeRuleKind = "exclude"
)

// ScopeRule is one program-scope directive for a session: a hostname
// pattern (exact, or "*.suffix" for the apex + any subdomain) paired with
// whether it's in or out of the engagement's authorized scope.
//
// This is deliberately independent of the gateway's own network-egress
// scope (gateway/config's scope.allow/scope.block) — that's a fail-closed
// execution-time boundary enforced by nftables; ScopeRule is knowledge-layer
// bookkeeping only, used to auto-classify discovered assets and to keep an
// out-of-scope host out of a generated report (internal/report), which
// matters because submitting an out-of-scope finding to a bug-bounty
// program is a policy violation, not just noise. It never gates execution.
type ScopeRule struct {
	ID        string        `json:"id"`
	SessionID string        `json:"session_id"`
	Pattern   string        `json:"pattern"` // "example.com" or "*.example.com"
	Kind      ScopeRuleKind `json:"kind"`
	Note      string        `json:"note,omitempty"`
	CreatedAt int64         `json:"created_at"`
}

// ScopeVerdict is the result of classifying one hostname against a
// session's ScopeRules.
type ScopeVerdict struct {
	InScope     bool   `json:"in_scope"`
	Reason      string `json:"reason"`
	MatchedRule string `json:"matched_rule,omitempty"`
}

const scopeRulesSchema = `
CREATE TABLE IF NOT EXISTS program_scope_rules (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    pattern TEXT NOT NULL,
    kind TEXT NOT NULL,
    note TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_k_scope_rules_session ON program_scope_rules(session_id);
`

// SaveScopeRule stores one program-scope rule. ID and CreatedAt are filled
// when empty. A malformed pattern (empty, or bare "*.") is rejected.
func (s *Store) SaveScopeRule(ctx context.Context, r ScopeRule) (ScopeRule, error) {
	pat, ok := normalizeScopePattern(r.Pattern)
	if !ok {
		return ScopeRule{}, fmt.Errorf("invalid scope pattern %q", r.Pattern)
	}
	r.Pattern = pat
	if r.Kind != ScopeInclude && r.Kind != ScopeExclude {
		return ScopeRule{}, fmt.Errorf("invalid scope rule kind %q (want %q or %q)", r.Kind, ScopeInclude, ScopeExclude)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().Unix()
	}

	if s.db != nil {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO program_scope_rules(id,session_id,pattern,kind,note,created_at)
			 VALUES(?,?,?,?,?,?)`,
			r.ID, r.SessionID, r.Pattern, string(r.Kind), r.Note, r.CreatedAt)
		if err != nil {
			return ScopeRule{}, fmt.Errorf("save scope rule: %w", err)
		}
	}

	if s.scopeRules == nil {
		s.scopeRules = make(map[string]ScopeRule)
	}
	s.scopeRules[r.ID] = r
	return r, nil
}

// ListScopeRules returns a session's scope rules, oldest first.
func (s *Store) ListScopeRules(ctx context.Context, sessionID string) ([]ScopeRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		out := make([]ScopeRule, 0)
		for _, r := range s.scopeRules {
			if r.SessionID == sessionID {
				out = append(out, r)
			}
		}
		// CreatedAt has one-second resolution (time.Now().Unix()), so two
		// rules saved in the same wall-clock second — routine for a bulk
		// import — tie on it; break the tie on ID so ordering is a pure
		// function of the data, not of the map's randomized iteration order.
		sort.Slice(out, func(i, j int) bool {
			if out[i].CreatedAt != out[j].CreatedAt {
				return out[i].CreatedAt < out[j].CreatedAt
			}
			return out[i].ID < out[j].ID
		})
		return out, nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id,session_id,pattern,kind,COALESCE(note,''),created_at
		 FROM program_scope_rules WHERE session_id=? ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list scope rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]ScopeRule, 0)
	for rows.Next() {
		var r ScopeRule
		var kind string
		if err := rows.Scan(&r.ID, &r.SessionID, &r.Pattern, &kind, &r.Note, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan scope rule: %w", err)
		}
		r.Kind = ScopeRuleKind(kind)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteScopeRule removes one scope rule. Returns false if it didn't exist.
func (s *Store) DeleteScopeRule(ctx context.Context, sessionID, ruleID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.scopeRules[ruleID]
	if !ok || existing.SessionID != sessionID {
		return false, nil
	}

	if s.db != nil {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM program_scope_rules WHERE id=? AND session_id=?`, ruleID, sessionID)
		if err != nil {
			return false, fmt.Errorf("delete scope rule: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, nil
		}
	}

	delete(s.scopeRules, ruleID)
	return true, nil
}

// ClassifyScope decides whether hostname is in or out of a program's scope
// given its rules, mirroring gateway's own hostname_block/hostname_allow
// precedence (gateway/src/policy/hostname.rs) for a consistent mental model
// across both scope systems: an exclude match always wins; otherwise, when
// any include rules exist, hostname must match one of them; with no rules
// restricting it at all, the default is in-scope (a program's scope grows
// by exclusion far more often than it's declared host-by-host).
func ClassifyScope(rules []ScopeRule, hostname string) ScopeVerdict {
	h := normalizeScopeHost(hostname)
	if h == "" {
		return ScopeVerdict{InScope: true, Reason: "not a hostname (IP literal or empty) — program scope is name-based only"}
	}

	var includes []ScopeRule
	for _, r := range rules {
		if r.Kind == ScopeExclude && scopePatternMatches(r.Pattern, h) {
			return ScopeVerdict{InScope: false, Reason: "matched exclude rule", MatchedRule: r.Pattern}
		}
		if r.Kind == ScopeInclude {
			includes = append(includes, r)
		}
	}

	if len(includes) == 0 {
		return ScopeVerdict{InScope: true, Reason: "no scope rules restrict this host"}
	}
	for _, r := range includes {
		if scopePatternMatches(r.Pattern, h) {
			return ScopeVerdict{InScope: true, Reason: "matched include rule", MatchedRule: r.Pattern}
		}
	}
	return ScopeVerdict{InScope: false, Reason: "include rules are defined but none matched (whitelist miss)"}
}

// normalizeScopePattern validates and canonicalizes a rule pattern: lowercase,
// trim a trailing dot and whitespace. Returns ok=false for an empty pattern
// or a bare "*." with no suffix.
func normalizeScopePattern(raw string) (string, bool) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" || p == "*" || p == "*." {
		return "", false
	}
	p = strings.TrimSuffix(p, ".")
	if rest, ok := strings.CutPrefix(p, "*."); ok && rest == "" {
		return "", false
	}
	return p, true
}

// normalizeScopeHost lowercases and trims a trailing dot; returns "" for an
// IP literal or empty input so program scope is a no-op on IP targets (an
// IP has no bearing on a domain-based bug-bounty program's declared scope).
func normalizeScopeHost(host string) string {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" {
		return ""
	}
	if net.ParseIP(h) != nil {
		return ""
	}
	return h
}

// scopePatternMatches reports whether host matches pattern: an exact string,
// or "*.suffix" matching the apex suffix and any subdomain at any depth.
func scopePatternMatches(pattern, host string) bool {
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		return host == rest || strings.HasSuffix(host, "."+rest)
	}
	return host == pattern
}
