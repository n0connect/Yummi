package skillcurator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

func sampleTrajectory() Trajectory {
	return Trajectory{
		TaskID: "t1", SessionID: "s1",
		Goal:    "enumerate open web services on the target",
		Summary: "found an exposed admin panel via directory brute force",
		Tactics: []knowledge.Tactic{
			{Tool: "gobuster", CommandPattern: "gobuster dir -u <TARGET> -w common.txt", Outcome: "worked", HitCount: 1},
		},
		Observations: []string{"gobuster found /admin (200)"},
	}
}

func TestDraftSkillParsesValidJSON(t *testing.T) {
	chat := func(_ context.Context, _, _ string) (string, error) {
		return `{"name":"web-admin-panel-discovery","description":"find hidden admin panels via directory brute force",
		"triggers":["admin panel","directory brute force"],"tools":["gobuster"],
		"knowledge":"run gobuster dir against the target with a common wordlist to find hidden admin endpoints"}`, nil
	}
	got, err := DraftSkill(context.Background(), chat, sampleTrajectory())
	if err != nil {
		t.Fatalf("DraftSkill: %v", err)
	}
	if got.Name != "web-admin-panel-discovery" {
		t.Errorf("unexpected name: %q", got.Name)
	}
	if len(got.Tools) != 1 || got.Tools[0] != "gobuster" {
		t.Errorf("unexpected tools: %+v", got.Tools)
	}
	if got.Knowledge == "" {
		t.Error("expected non-empty knowledge field")
	}
}

func TestDraftSkillStripsMarkdownCodeFences(t *testing.T) {
	chat := func(_ context.Context, _, _ string) (string, error) {
		return "```json\n" + `{"name":"x","description":"d","triggers":["t"],"tools":["gobuster"],"knowledge":"k"}` + "\n```", nil
	}
	got, err := DraftSkill(context.Background(), chat, sampleTrajectory())
	if err != nil {
		t.Fatalf("DraftSkill: %v", err)
	}
	if got.Name != "x" {
		t.Errorf("expected code-fenced JSON to parse, got %+v", got)
	}
}

func TestDraftSkillMalformedJSONIsError(t *testing.T) {
	chat := func(_ context.Context, _, _ string) (string, error) {
		return "not json at all", nil
	}
	_, err := DraftSkill(context.Background(), chat, sampleTrajectory())
	if err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestDraftSkillChatErrorPropagates(t *testing.T) {
	chat := func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("ollama unreachable")
	}
	_, err := DraftSkill(context.Background(), chat, sampleTrajectory())
	if err == nil || !strings.Contains(err.Error(), "ollama unreachable") {
		t.Fatalf("expected chat error to propagate, got %v", err)
	}
}

func TestDraftSkillEmptyTrajectoryIsErrorBeforeCallingChat(t *testing.T) {
	called := false
	chat := func(_ context.Context, _, _ string) (string, error) {
		called = true
		return `{"name":"x","description":"d","triggers":["t"],"tools":["x"],"knowledge":"k"}`, nil
	}
	_, err := DraftSkill(context.Background(), chat, Trajectory{TaskID: "t1", SessionID: "s1"})
	if err == nil {
		t.Fatal("expected an error for an empty trajectory")
	}
	if called {
		t.Fatal("expected DraftSkill to reject an empty trajectory before calling chat")
	}
}
