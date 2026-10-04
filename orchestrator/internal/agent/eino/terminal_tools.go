package eino

import (
	"github.com/cloudwego/eino/schema"
)

// terminal_tools.go exposes the seven persistent-terminal verbs
// (sandbox-mcp/terminal.go) as first-class Eino tools, so a capable model can
// open and drive real tmux sessions instead of one-shot commands: connect a
// VPN and keep it up, run a listener in one session while exploiting from
// another, answer an interactive sqlmap/msf prompt, serve a payload over
// http.server and fetch it, etc.
//
// They are ordinary pentestTools (reusing newPentestTool's whole
// validate→gate→exec→verify→threatscan pipeline). Two properties make that
// reuse correct:
//   - Their catalog entries carry TargetField "" (sandboxmcp.NeedsTarget=false),
//     so pentestTool.InvokableRun skips the target requirement and processMCP
//     skips the per-command gateway scope check — external reachability is
//     still enforced at the packet layer by the fail-closed gateway.
//   - term_run's RenderCommand embeds the raw command, so the validator's risk
//     floor and the operator's approval prompt operate on what actually runs
//     inside the session — approval gating is identical to any other command.
//
// Every terminal read/wait result flows back through runToolPipeline's
// threatscan step, so target-controlled terminal output is scanned for
// prompt-injection/exfiltration before it enters the model's context, exactly
// like every scan tool's output.

var idParam = &schema.ParameterInfo{Type: "string", Desc: "Session id returned by term_open", Required: true}

var termOpenSpec = pentestToolSpec{
	Name: "term_open",
	Desc: "Open a new PERSISTENT terminal (tmux) session and get back its id. Sessions survive across tool calls and keep shell state (cwd, env, background jobs). Open several — e.g. one to drive the target, one to host a payload. Use this instead of run_command whenever you need state to persist, a long-running/interactive process, or more than one terminal at once.",
	Params: map[string]*schema.ParameterInfo{
		"label": {Type: "string", Desc: "Optional short name for the session (letters, digits, _ and -), e.g. 'target' or 'serve'. Auto-generated if omitted.", Required: false},
		"cols":  {Type: "integer", Desc: "Optional terminal width in columns (default 200)", Required: false},
		"rows":  {Type: "integer", Desc: "Optional terminal height in rows (default 50)", Required: false},
	},
}

var termRunSpec = pentestToolSpec{
	Name: "term_run",
	Desc: "Type a command into a session and (by default) wait for it to finish, returning its output and exit code. Set wait=false to fire-and-return for a long-running or interactive command (VPN connect, a listener, python3 -m http.server, msfconsole), then observe it with term_wait/term_read and drive it with term_keys.",
	Params: map[string]*schema.ParameterInfo{
		"id":           idParam,
		"command":      {Type: "string", Desc: "Shell command line to run in the session; pipes, &&, redirection all work (it is interpreted by the session's own shell)", Required: true},
		"wait":         {Type: "boolean", Desc: "true (default): block until the command finishes. false: return immediately (for interactive/long-running commands).", Required: false},
		"timeout_secs": {Type: "integer", Desc: "Max seconds to wait when wait=true (default 30, max 1800). For a slow scan, raise this or use wait=false.", Required: false},
	},
}

var termReadSpec = pentestToolSpec{
	Name: "term_read",
	Desc: "Read a session's current screen/scrollback. Use to inspect the progress of a fire-and-return (wait=false) command.",
	Params: map[string]*schema.ParameterInfo{
		"id":    idParam,
		"lines": {Type: "integer", Desc: "How many trailing lines to return (default 200)", Required: false},
		"full":  {Type: "boolean", Desc: "Return the entire scrollback instead of just the tail", Required: false},
	},
}

var termWaitSpec = pentestToolSpec{
	Name: "term_wait",
	Desc: "Block until a session's output matches a regex pattern (e.g. 'password:' or 'yes/no' or a shell prompt) or has gone idle for idle_secs. This is how you 'watch' an interactive or long command and know when to act, instead of a human staring at the screen.",
	Params: map[string]*schema.ParameterInfo{
		"id":           idParam,
		"pattern":      {Type: "string", Desc: "Regular expression to wait for in the pane output, e.g. 'password:' or '\\$\\s*$'", Required: false},
		"idle_secs":    {Type: "integer", Desc: "Return once the pane has not changed for this many seconds (detects a settled/finished command)", Required: false},
		"timeout_secs": {Type: "integer", Desc: "Max seconds to wait (default 30, max 1800)", Required: false},
	},
}

var termKeysSpec = pentestToolSpec{
	Name: "term_keys",
	Desc: "Send raw keys, input, or commands to a session to interact with a running program — answer a prompt, interrupt with Ctrl-C, navigate a menu, or type into an interactive shell. Spaces and special characters are preserved.",
	Params: map[string]*schema.ParameterInfo{
		"id":      idParam,
		"keys":    {Type: "string", Desc: "Keys or command to send. Supports tmux key names (e.g. 'C-c', 'Enter', 'Up Up Enter') as well as text and commands. Spaces, quotes, and punctuation are fully preserved.", Required: true},
		"literal": {Type: "boolean", Desc: "Force sending keys strictly as literal characters without interpreting any tmux key names", Required: false},
		"enter":   {Type: "boolean", Desc: "Append a Return keypress after sending (useful when typing a command or answering a prompt)", Required: false},
	},
}

var termListSpec = pentestToolSpec{
	Name:   "term_list",
	Desc:   "List the open terminal sessions with their size and label.",
	Params: map[string]*schema.ParameterInfo{},
}

var termCloseSpec = pentestToolSpec{
	Name: "term_close",
	Desc: "Close (kill) a terminal session you no longer need.",
	Params: map[string]*schema.ParameterInfo{
		"id": idParam,
	},
}

// terminalToolSpecs is every terminal-session verb exposed to the Eino
// backend — runner.go builds one pentestTool per entry, right after the
// pentest tool specs, whenever an MCPExec is configured.
var terminalToolSpecs = []pentestToolSpec{
	termOpenSpec, termRunSpec, termReadSpec, termWaitSpec, termKeysSpec, termListSpec, termCloseSpec,
}
