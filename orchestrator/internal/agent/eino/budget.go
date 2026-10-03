package eino

import (
	"fmt"
	"strconv"
	"strings"
)

// budget.go: what happens when a task exhausts its step budget.
//
// Previously the run failed outright with "exceeds max iterations" and the
// operator got a generic message while all the evidence gathered so far was
// lost. Now the run pauses with an evidence digest and asks the operator
// whether to grant more steps. Nothing is granted without an answer.

const (
	// budgetPausePrefix marks a paused task's Question as a budget pause, so
	// Start() can tell a budget decision apart from an ordinary ask_operator.
	budgetPausePrefix = "Step budget exhausted"

	budgetDefaultGrant = 10  // used when the operator answers without a number
	budgetMaxGrant     = 30  // per decision
	budgetMaxTotal     = 120 // hard ceiling across all grants on one task
)

// isBudgetExhausted reports whether a run error is the step-budget limit.
func isBudgetExhausted(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "max iterations")
}

// budgetPauseQuestion is the operator-facing pause text. It stays short on
// purpose: the operator already sees every command and result in the activity
// feed, plus any report_progress notes the agent chose to write.
func budgetPauseQuestion(budget int) string {
	return fmt.Sprintf("%s (%d steps). Reply with a number to grant that many more steps "+
		"(max %d per decision), or \"stop\" to end the task here.", budgetPausePrefix, budget, budgetMaxGrant)
}

// parseBudgetAnswer interprets the operator's reply to a budget pause.
// "stop"-style replies end the task; a number grants that many steps; any
// other reply grants the default.
func parseBudgetAnswer(answer string) (extra int, stop bool) {
	low := strings.ToLower(strings.TrimSpace(answer))
	for _, w := range []string{"stop", "dur", "bitir", "finish", "yeter"} {
		if strings.Contains(low, w) {
			return 0, true
		}
	}
	for _, f := range strings.Fields(low) {
		if n, err := strconv.Atoi(strings.Trim(f, ".,;:!")); err == nil {
			return clampGrant(n), false
		}
	}
	return budgetDefaultGrant, false
}

func clampGrant(n int) int {
	if n < 1 {
		return 1
	}
	if n > budgetMaxGrant {
		return budgetMaxGrant
	}
	return n
}

// grantBudget returns the new step budget after an operator grant, capped at
// budgetMaxTotal.
func grantBudget(current, extra int) int {
	next := current + extra
	if next > budgetMaxTotal {
		return budgetMaxTotal
	}
	return next
}
