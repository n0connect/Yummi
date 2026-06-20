package agent

import "time"

// ToolResult is the outcome of one command execution.
//
// IsError=true means the command ran but failed (non-zero exit, execution
// error); the Content is surfaced to the LLM as an observation and the loop
// continues. A non-nil Go error return from Execer.Run is an infrastructure
// failure (docker down, enqueue failed) that halts the graph.
type ToolResult struct {
	Content  string
	IsError  bool
	ExitCode int
	Duration time.Duration
}
