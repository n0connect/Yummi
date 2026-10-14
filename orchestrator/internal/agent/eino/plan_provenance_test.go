package eino

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

func TestUpdatePlanCannotInventExecutedSteps(t *testing.T) {
	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("provenance", "Describe or inspect a service", 10)
	if err != nil {
		t.Fatal(err)
	}
	u := newUpdatePlanTool(store, task.ID)
	_, err = u.InvokableRun(context.Background(), `{"steps":[{"id":"invented","description":"Scanned service","status":"done","command":"nmap example.com","result":"all checks passed","error":"made up","duration":123,"retry_count":9}],"reason":"finished"}`)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := store.Get(task.ID)
	s := actual.Plan[0]
	if s.Status != agent.StepPending || s.Command != "" || s.Result != "" || s.Error != "" || s.Duration != 0 || s.RetryCount != 0 {
		t.Fatalf("update_plan invented execution history: %+v", s)
	}
}

// Architectural invariant: a finish callback may store the model narrative,
// but must not manufacture successful execution states for arbitrary steps.
func TestFinishCannotManufactureDonePlanSteps(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "runner.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		fn, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fn.Sel.Name != "Transition" {
			return true
		}
		status, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok || status.Sel.Name != "StatusCompleted" {
			return true
		}
		ast.Inspect(call.Args[2], func(n ast.Node) bool {
			assignment, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, value := range assignment.Rhs {
				if sel, ok := value.(*ast.SelectorExpr); ok && sel.Sel.Name == "StepDone" {
					t.Error("completion callback manufactures successful plan states from model finish")
				}
			}
			return true
		})
		return true
	})
}
