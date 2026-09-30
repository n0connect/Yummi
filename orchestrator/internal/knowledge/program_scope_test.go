package knowledge

import (
	"context"
	"testing"
)

func TestSaveAndListScopeRules(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	if _, err := s.SaveScopeRule(ctx, ScopeRule{SessionID: "s1", Pattern: "*.example.com", Kind: ScopeInclude}); err != nil {
		t.Fatalf("SaveScopeRule include: %v", err)
	}
	if _, err := s.SaveScopeRule(ctx, ScopeRule{SessionID: "s1", Pattern: "internal.example.com", Kind: ScopeExclude, Note: "internal only"}); err != nil {
		t.Fatalf("SaveScopeRule exclude: %v", err)
	}
	// Different session — must not leak into s1's list.
	if _, err := s.SaveScopeRule(ctx, ScopeRule{SessionID: "s2", Pattern: "other.com", Kind: ScopeInclude}); err != nil {
		t.Fatalf("SaveScopeRule other session: %v", err)
	}

	got, err := s.ListScopeRules(ctx, "s1")
	if err != nil {
		t.Fatalf("ListScopeRules: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rules for s1, got %d: %+v", len(got), got)
	}
	byPattern := make(map[string]ScopeRule, len(got))
	for _, r := range got {
		if r.ID == "" {
			t.Fatal("rule ID not assigned")
		}
		byPattern[r.Pattern] = r
	}
	if byPattern["internal.example.com"].Note != "internal only" {
		t.Fatalf("note not preserved: %+v", byPattern["internal.example.com"])
	}
}

func TestSaveScopeRuleRejectsMalformedPattern(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	if _, err := s.SaveScopeRule(ctx, ScopeRule{SessionID: "s", Pattern: "", Kind: ScopeInclude}); err == nil {
		t.Fatal("want error for empty pattern")
	}
	if _, err := s.SaveScopeRule(ctx, ScopeRule{SessionID: "s", Pattern: "*.", Kind: ScopeInclude}); err == nil {
		t.Fatal("want error for bare wildcard pattern")
	}
	if _, err := s.SaveScopeRule(ctx, ScopeRule{SessionID: "s", Pattern: "example.com", Kind: "maybe"}); err == nil {
		t.Fatal("want error for invalid kind")
	}
}

func TestDeleteScopeRule(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	r, err := s.SaveScopeRule(ctx, ScopeRule{SessionID: "s1", Pattern: "example.com", Kind: ScopeInclude})
	if err != nil {
		t.Fatalf("SaveScopeRule: %v", err)
	}

	// Wrong session — must refuse.
	if ok, _ := s.DeleteScopeRule(ctx, "wrong-session", r.ID); ok {
		t.Fatal("DeleteScopeRule should not delete across sessions")
	}
	ok, err := s.DeleteScopeRule(ctx, "s1", r.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteScopeRule: ok=%v err=%v", ok, err)
	}
	if ok, _ := s.DeleteScopeRule(ctx, "s1", r.ID); ok {
		t.Fatal("second delete of the same rule should report not-found")
	}

	got, _ := s.ListScopeRules(ctx, "s1")
	if len(got) != 0 {
		t.Fatalf("want 0 rules after delete, got %d", len(got))
	}
}

func TestClassifyScope_NoRulesDefaultsInScope(t *testing.T) {
	v := ClassifyScope(nil, "anything.example.com")
	if !v.InScope {
		t.Fatalf("want in-scope with no rules, got %+v", v)
	}
}

func TestClassifyScope_ExcludeAlwaysWins(t *testing.T) {
	rules := []ScopeRule{
		{Pattern: "*.example.com", Kind: ScopeInclude},
		{Pattern: "internal.example.com", Kind: ScopeExclude},
	}
	v := ClassifyScope(rules, "internal.example.com")
	if v.InScope {
		t.Fatalf("exclude should win over a broader include, got %+v", v)
	}
	if v.MatchedRule != "internal.example.com" {
		t.Fatalf("matched rule not reported: %+v", v)
	}

	v2 := ClassifyScope(rules, "app.example.com")
	if !v2.InScope {
		t.Fatalf("app.example.com should still be in scope via the include, got %+v", v2)
	}
}

func TestClassifyScope_IncludeIsAWhitelistWhenPresent(t *testing.T) {
	rules := []ScopeRule{{Pattern: "*.example.com", Kind: ScopeInclude}}

	v := ClassifyScope(rules, "app.example.com")
	if !v.InScope {
		t.Fatalf("want in-scope, got %+v", v)
	}

	v2 := ClassifyScope(rules, "totally-unrelated.com")
	if v2.InScope {
		t.Fatalf("want out-of-scope for a host matching no include rule, got %+v", v2)
	}
}

func TestClassifyScope_WildcardCoversApexAndAnyDepth(t *testing.T) {
	rules := []ScopeRule{{Pattern: "*.example.com", Kind: ScopeInclude}}

	for _, host := range []string{"example.com", "a.example.com", "a.b.example.com", "EXAMPLE.COM"} {
		if v := ClassifyScope(rules, host); !v.InScope {
			t.Fatalf("expected %s in scope, got %+v", host, v)
		}
	}
	// Suffix-trick host must not match.
	if v := ClassifyScope(rules, "example.com.evil.com"); v.InScope {
		t.Fatalf("suffix-trick host should not match, got %+v", v)
	}
}

func TestClassifyScope_IPLiteralIsAlwaysNeutralInScope(t *testing.T) {
	rules := []ScopeRule{{Pattern: "*.example.com", Kind: ScopeInclude}}
	v := ClassifyScope(rules, "203.0.113.10")
	if !v.InScope {
		t.Fatalf("IP literals are outside program-scope's hostname model, want in-scope neutral, got %+v", v)
	}
}

func TestClassifyScope_ExactPatternDoesNotMatchSubdomains(t *testing.T) {
	rules := []ScopeRule{{Pattern: "example.com", Kind: ScopeInclude}}
	if v := ClassifyScope(rules, "api.example.com"); v.InScope {
		t.Fatalf("exact pattern must not match a subdomain, got %+v", v)
	}
	if v := ClassifyScope(rules, "example.com"); !v.InScope {
		t.Fatalf("exact pattern must match its own literal host, got %+v", v)
	}
}
