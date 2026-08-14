package eino

import "sync"

// runTrace collects tool-call outcomes during one Run() invocation. Eino's
// native tool-calling loop keeps its own chat history internally, but doesn't
// expose a step-by-step observation log the way graph.go's MemoryContext
// does — this rebuilds just enough of one (tool outputs + which tools ran)
// to feed the optional SkillCurator hook on successful completion. Safe for
// concurrent use: the ADK may dispatch multiple tool calls from one model
// turn in parallel.
type runTrace struct {
	mu           sync.Mutex
	observations []string
	tools        []string
}

// record appends one tool's raw command (its first token is the tool name
// knowledge.ListTactics keys tactics by, e.g. "nmap" from "nmap -sV ...")
// and the result text it returned.
func (t *runTrace) record(command, result string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.observations = append(t.observations, result)
	if tool := firstToken(command); tool != "" {
		t.tools = append(t.tools, tool)
	}
}

// snapshot returns copies of the collected observations and distinct tool
// names, safe to read after Run()'s event loop has finished.
func (t *runTrace) snapshot() (observations []string, tools []string) {
	if t == nil {
		return nil, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	observations = append([]string{}, t.observations...)
	seen := make(map[string]bool, len(t.tools))
	for _, tl := range t.tools {
		if !seen[tl] {
			seen[tl] = true
			tools = append(tools, tl)
		}
	}
	return observations, tools
}

func firstToken(s string) string {
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i]
		}
	}
	return s
}

// finishCapture holds the structured summary/findings the finish tool
// received, since Run()'s event loop only sees the final chat message, not
// the arguments a tool call carried. A pointer to one instance is shared
// between the finishTool built for a task and the Run() call that built it.
type finishCapture struct {
	mu       sync.Mutex
	summary  string
	findings []string
	called   bool
}

func (f *finishCapture) set(summary string, findings []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summary, f.findings, f.called = summary, findings, true
}

func (f *finishCapture) get() (summary string, findings []string, called bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.summary, f.findings, f.called
}

// askCapture is finishCapture's sibling for the ask_operator tool: both tools
// are registered as ReturnDirectly, so Run()'s event loop can't tell from the
// chat message alone which one ended the turn — it checks these captures.
type askCapture struct {
	mu       sync.Mutex
	question string
	called   bool
}

func (a *askCapture) set(question string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.question, a.called = question, true
}

func (a *askCapture) get() (question string, called bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.question, a.called
}
