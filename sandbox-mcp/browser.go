package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type browserManager struct {
	mu     sync.Mutex
	driver BrowserDriver
}

var globalBrowser = &browserManager{}

func (m *browserManager) getDriver() BrowserDriver {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.driver == nil {
		m.driver = NewRodDriver()
	}
	return m.driver
}

// ── browser_open ─────────────────────────────────────────────────────────────

type browserOpenArgs struct {
	URL       string            `json:"url,omitempty" jsonschema:"the target URL to navigate to (e.g. http://172.20.0.5/login)"`
	Target    string            `json:"target,omitempty" jsonschema:"alias for url"`
	URI       string            `json:"uri,omitempty" jsonschema:"alias for url"`
	WaitUntil string            `json:"wait_until,omitempty" jsonschema:"optional wait strategy: load (default), networkidle (for SPAs), domcontentloaded"`
	WaitMs    int               `json:"wait_ms,omitempty" jsonschema:"optional additional wait in milliseconds after page load (0-10000); useful for SPAs that animate content in after the load event"`
	Session   string            `json:"session,omitempty" jsonschema:"optional isolated session label (default: default)"`
	Headers   map[string]string `json:"headers,omitempty" jsonschema:"optional custom HTTP request headers (e.g. Authorization: Bearer <token>, X-Forwarded-For: 127.0.0.1)"`
	Headless  *bool             `json:"headless,omitempty" jsonschema:"optional headless override: true for background headless, false for visual desktop display"`
}

func browserOpenHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserOpenArgs) (*mcp.CallToolResult, any, error) {
	u := strings.TrimSpace(args.URL)
	if u == "" {
		u = strings.TrimSpace(args.Target)
	}
	if u == "" {
		u = strings.TrimSpace(args.URI)
	}
	if u == "" {
		return errResult("url is required")
	}
	if err := rejectIfFlagLike("url", u); err != nil {
		return errResult(err.Error())
	}
	waitMs := args.WaitMs
	if waitMs < 0 {
		waitMs = 0
	}
	if waitMs > 10000 {
		waitMs = 10000
	}

	driver := globalBrowser.getDriver()
	info, err := driver.Open(ctx, OpenParams{
		URL:       u,
		WaitUntil: args.WaitUntil,
		WaitMs:    waitMs,
		Session:   args.Session,
		Headers:   args.Headers,
		Headless:  args.Headless,
	})
	if err != nil {
		return errResult(fmt.Sprintf("browser_open failed: %v", err))
	}

	var text strings.Builder
	fmt.Fprintf(&text, "Navigated to: %s\nTitle: %s\nHTTP status: %d (0 = unavailable/non-HTTP)\n", info.URL, info.Title, info.HTTPStatus)
	if info.InitialSnapshot != nil && info.InitialSnapshot.TreeText != "" {
		text.WriteString("\n--- Accessibility Snapshot ---\n")
		text.WriteString(info.InitialSnapshot.TreeText)
	}

	return textResult(text.String())
}

// ── browser_snapshot ─────────────────────────────────────────────────────────

type browserSnapshotArgs struct {
	InteractiveOnly *bool  `json:"interactive_only,omitempty" jsonschema:"filter to interactive elements only (buttons, inputs, links) to keep token count ultra-low (~200-400 tokens, default: true)"`
	Full            bool   `json:"full,omitempty" jsonschema:"capture full document tree (alias for interactive_only=false)"`
	Compact         *bool  `json:"compact,omitempty" jsonschema:"strip empty structural containers (default: true)"`
	Depth           int    `json:"depth,omitempty" jsonschema:"max tree depth (default: 4)"`
	Selector        string `json:"selector,omitempty" jsonschema:"optional CSS selector to scope the snapshot (e.g. '#login-form')"`
}

func browserSnapshotHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserSnapshotArgs) (*mcp.CallToolResult, any, error) {
	interOnly := true
	if args.InteractiveOnly != nil {
		interOnly = *args.InteractiveOnly
	}
	if args.Full {
		interOnly = false
	}
	compact := true
	if args.Compact != nil {
		compact = *args.Compact
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 4
	}

	driver := globalBrowser.getDriver()
	snap, err := driver.Snapshot(ctx, interOnly, compact, depth, args.Selector)
	if err != nil {
		return errResult(fmt.Sprintf("browser_snapshot failed: %v", err))
	}
	return textResult(snap.TreeText)
}

// ── browser_interact ─────────────────────────────────────────────────────────

type browserInteractArgs struct {
	Action       string  `json:"action" jsonschema:"interaction action: click, dblclick, right_click, context_menu, fill, type, select, check, uncheck, press, hover, upload, scroll, scroll_to, click_at, move_to, drag_and_drop, set_viewport, clipboard_read, clipboard_write, wait_for_selector, wait"`
	Ref          string  `json:"ref,omitempty" jsonschema:"target element reference from snapshot, e.g. @e1, @e2 (preferred for accuracy)"`
	Element      string  `json:"element,omitempty" jsonschema:"alias for ref"`
	Target       string  `json:"target,omitempty" jsonschema:"alias for ref or selector"`
	Selector     string  `json:"selector,omitempty" jsonschema:"fallback CSS selector if ref is not used"`
	Value        string  `json:"value,omitempty" jsonschema:"value to type/fill, select option, key to press (Enter, Tab, Escape), or absolute file path to upload"`
	Text         string  `json:"text,omitempty" jsonschema:"alias for value in fill/type"`
	Val          string  `json:"val,omitempty" jsonschema:"alias for value in fill/type"`
	Key          string  `json:"key,omitempty" jsonschema:"alias for value in press"`
	ScrollDir    string  `json:"scroll_dir,omitempty" jsonschema:"scroll direction for action=scroll: up, down"`
	ScrollAmount int     `json:"scroll_amount,omitempty" jsonschema:"pixels to scroll (default: 500)"`
	X            float64 `json:"x,omitempty" jsonschema:"viewport X coordinate for click_at, move_to, drag start, or scroll_to"`
	Y            float64 `json:"y,omitempty" jsonschema:"viewport Y coordinate for click_at, move_to, drag start, or scroll_to"`
	ToX          float64 `json:"to_x,omitempty" jsonschema:"destination viewport X coordinate for drag_and_drop"`
	ToY          float64 `json:"to_y,omitempty" jsonschema:"destination viewport Y coordinate for drag_and_drop"`
	ToRef        string  `json:"to_ref,omitempty" jsonschema:"destination element ref for drag_and_drop, e.g. @e5"`
	ToElement    string  `json:"to_element,omitempty" jsonschema:"alias for to_ref"`
	Width        int     `json:"width,omitempty" jsonschema:"viewport width for set_viewport (e.g. 1920 or 375 for mobile emulation)"`
	Height       int     `json:"height,omitempty" jsonschema:"viewport height for set_viewport (e.g. 1080 or 812 for mobile emulation)"`
	Duration     float64 `json:"duration,omitempty" jsonschema:"duration in seconds for wait or wait_for_selector timeout (default: 2s for wait, 5s for wait_for_selector)"`
}

func browserInteractHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserInteractArgs) (*mcp.CallToolResult, any, error) {
	act := strings.TrimSpace(args.Action)
	if act == "" {
		return errResult("action is required")
	}

	ref := strings.TrimSpace(args.Ref)
	if ref == "" {
		ref = strings.TrimSpace(args.Element)
	}

	selector := strings.TrimSpace(args.Selector)
	target := strings.TrimSpace(args.Target)
	if ref == "" && target != "" {
		if strings.HasPrefix(target, "@e") {
			ref = target
		} else if selector == "" {
			selector = target
		}
	}

	val := args.Value
	if val == "" {
		val = args.Text
	}
	if val == "" {
		val = args.Val
	}
	if val == "" {
		val = args.Key
	}

	toRef := strings.TrimSpace(args.ToRef)
	if toRef == "" {
		toRef = strings.TrimSpace(args.ToElement)
	}

	driver := globalBrowser.getDriver()
	res, err := driver.Interact(ctx, InteractParams{
		Action:       act,
		Ref:          ref,
		Selector:     selector,
		Value:        val,
		ScrollDir:    args.ScrollDir,
		ScrollAmount: args.ScrollAmount,
		X:            args.X,
		Y:            args.Y,
		ToX:          args.ToX,
		ToY:          args.ToY,
		ToRef:        toRef,
		Width:        args.Width,
		Height:       args.Height,
		Duration:     args.Duration,
	})
	if err != nil {
		return errResult(fmt.Sprintf("browser_interact error: %v", err))
	}

	var text strings.Builder
	text.WriteString(res.Message + "\n")
	if res.Navigated {
		fmt.Fprintf(&text, "Navigation detected -> New URL: %s (Title: %s)\n", res.CurrentURL, res.CurrentTitle)
		text.WriteString("Tip: Refs are now stale. Run browser_snapshot to see the new page elements.")
	}

	return textResult(text.String())
}

// ── browser_eval ─────────────────────────────────────────────────────────────

type browserEvalArgs struct {
	Script     string `json:"script,omitempty" jsonschema:"JavaScript snippet to evaluate in the page context (e.g. document.title, window.__SECRET__, localStorage)"`
	Expression string `json:"expression,omitempty" jsonschema:"alias for script"`
	Code       string `json:"code,omitempty" jsonschema:"alias for script"`
	JS         string `json:"js,omitempty" jsonschema:"alias for script"`
}

func browserEvalHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserEvalArgs) (*mcp.CallToolResult, any, error) {
	script := strings.TrimSpace(args.Script)
	if script == "" {
		script = strings.TrimSpace(args.Expression)
	}
	if script == "" {
		script = strings.TrimSpace(args.Code)
	}
	if script == "" {
		script = strings.TrimSpace(args.JS)
	}
	if script == "" {
		return errResult("script is required")
	}

	driver := globalBrowser.getDriver()
	val, err := driver.Eval(ctx, script)
	if err != nil {
		return errResult(fmt.Sprintf("browser_eval error: %v", err))
	}

	outJSON, _ := json.MarshalIndent(val, "", "  ")
	return textResult(string(outJSON))
}

// ── browser_screenshot ───────────────────────────────────────────────────────

type browserScreenshotArgs struct {
	FullPage   bool   `json:"full_page,omitempty" jsonschema:"capture full scroll height instead of current viewport"`
	Annotate   bool   `json:"annotate,omitempty" jsonschema:"render numbered Set-of-Marks labels matching snapshot @eN refs onto the screenshot"`
	OutputFile string `json:"output_file,omitempty" jsonschema:"optional absolute path under /home/pentester/work/ to save screenshot PNG"`
	Path       string `json:"path,omitempty" jsonschema:"alias for output_file"`
	Filename   string `json:"filename,omitempty" jsonschema:"alias for output_file"`
	File       string `json:"file,omitempty" jsonschema:"alias for output_file"`
}

func browserScreenshotHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserScreenshotArgs) (*mcp.CallToolResult, any, error) {
	outPath := strings.TrimSpace(args.OutputFile)
	if outPath == "" {
		outPath = strings.TrimSpace(args.Path)
	}
	if outPath == "" {
		outPath = strings.TrimSpace(args.Filename)
	}
	if outPath == "" {
		outPath = strings.TrimSpace(args.File)
	}

	driver := globalBrowser.getDriver()
	res, err := driver.Screenshot(ctx, args.FullPage, args.Annotate, outPath)
	if err != nil {
		return errResult(fmt.Sprintf("browser_screenshot error: %v", err))
	}

	msg := fmt.Sprintf("Screenshot saved to: %s (%dx%d)", res.FilePath, res.Width, res.Height)
	return textResult(msg)
}

// ── browser_storage ─────────────────────────────────────────────────────────

type browserStorageArgs struct {
	Action string `json:"action" jsonschema:"storage action: get_cookies, get_storage, clear, save_state, load_state, set_cookie"`
	Path   string `json:"path,omitempty" jsonschema:"absolute path for save_state or load_state (e.g. /home/pentester/work/session.json)"`
	Name   string `json:"name,omitempty" jsonschema:"cookie name for set_cookie"`
	Value  string `json:"value,omitempty" jsonschema:"cookie value for set_cookie"`
	Domain string `json:"domain,omitempty" jsonschema:"optional cookie domain for set_cookie"`
}

func browserStorageHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserStorageArgs) (*mcp.CallToolResult, any, error) {
	act := strings.ToLower(strings.TrimSpace(args.Action))
	if act == "" {
		return errResult("action is required (get_cookies, get_storage, clear, save_state, load_state, set_cookie)")
	}

	driver := globalBrowser.getDriver()
	res, err := driver.Storage(ctx, StorageParams{
		Action: act,
		Path:   args.Path,
		Name:   args.Name,
		Value:  args.Value,
		Domain: args.Domain,
	})
	if err != nil {
		return errResult(fmt.Sprintf("browser_storage error: %v", err))
	}

	if act == "get_cookies" {
		b, _ := json.MarshalIndent(res.Cookies, "", "  ")
		return textResult(string(b))
	}
	if act == "get_storage" {
		b, _ := json.MarshalIndent(res.Storage, "", "  ")
		return textResult(string(b))
	}
	return textResult(res.Message)
}

// ── browser_tabs ────────────────────────────────────────────────────────────

type browserTabsArgs struct {
	Action string `json:"action" jsonschema:"tab action: list, new, switch, close"`
	TabID  int    `json:"tab_id,omitempty" jsonschema:"tab index to switch to or close (1-based)"`
	URL    string `json:"url,omitempty" jsonschema:"optional URL to open when action=new"`
}

func browserTabsHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserTabsArgs) (*mcp.CallToolResult, any, error) {
	act := strings.TrimSpace(args.Action)
	if act == "" {
		return errResult("action is required (list, new, switch, close)")
	}

	driver := globalBrowser.getDriver()
	res, err := driver.Tabs(ctx, act, args.TabID, args.URL)
	if err != nil {
		return errResult(fmt.Sprintf("browser_tabs error: %v", err))
	}

	if act == "list" {
		var b strings.Builder
		b.WriteString("Open Tabs:\n")
		for _, tab := range res.Tabs {
			marker := " "
			if tab.IsActive {
				marker = "*"
			}
			fmt.Fprintf(&b, " [%s] Tab %d: %s (%s)\n", marker, tab.ID, tab.Title, tab.URL)
		}
		return textResult(b.String())
	}

	return textResult(fmt.Sprintf("tab action %s completed", act))
}

// ── browser_logs ────────────────────────────────────────────────────────────

type browserLogsArgs struct {
	Filter string `json:"filter,omitempty" jsonschema:"optional filter for log messages: all, console, network, error, dialog"`
	Clear  bool   `json:"clear,omitempty" jsonschema:"clear buffered logs after reading"`
}

func browserLogsHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserLogsArgs) (*mcp.CallToolResult, any, error) {
	driver := globalBrowser.getDriver()
	res, err := driver.Logs(ctx, args.Filter, args.Clear)
	if err != nil {
		return errResult(fmt.Sprintf("browser_logs error: %v", err))
	}

	if len(res.Entries) == 0 {
		return textResult("No console logs captured.")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Captured %d log entries:\n", len(res.Entries))
	for _, entry := range res.Entries {
		fmt.Fprintf(&b, "[%s] %s: %s\n", entry.Timestamp, entry.Type, entry.Text)
	}
	return textResult(b.String())
}

// registerBrowserTools registers all 8 browser tools onto the MCP server.
func registerBrowserTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "browser_session", Description: "Read actual browser mode and isolated active session, or reset all browser sessions before changing headless/full mode."}, browserSessionHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_open",
		Description: "Navigate to a URL with Chromium. Automatically extracts page title, initial @eN accessibility snapshot, and supports custom HTTP request headers.",
	}, browserOpenHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_snapshot",
		Description: "Capture a compact Accessibility Tree snapshot of the current page with @eN element references (~200-400 tokens), traversing shadow DOM and iframes.",
	}, browserSnapshotHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_interact",
		Description: "Interact with an element or viewport: click, dblclick, fill, type, select, press (Enter/Tab/Esc), hover, upload file, scroll, click_at (x,y), move_to, drag_and_drop, set_viewport, clipboard_read, clipboard_write.",
	}, browserInteractHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_eval",
		Description: "Evaluate arbitrary JavaScript in the page context. Essential for DOM XSS, checking window variables, or triggering events.",
	}, browserEvalHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_screenshot",
		Description: "Capture a screenshot of the current page to /home/pentester/work/, with optional Set-of-Marks numbered @eN badge overlay.",
	}, browserScreenshotHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_storage",
		Description: "Inspect/save/load browser cookies, localStorage, and sessionStorage. Enables saving login state to share with sqlmap/ffuf or clearing sessions.",
	}, browserStorageHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_tabs",
		Description: "Manage multiple browser tabs: list open tabs, open a new tab, switch tabs, or close tabs.",
	}, browserTabsHandler)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "browser_logs",
		Description: "Retrieve captured browser console logs (console.log, console.error) and network traffic (requests, errors, APIs).",
	}, browserLogsHandler)
}

type browserSessionArgs struct {
	Action string `json:"action" jsonschema:"status or reset (reset closes all tabs and isolated sessions)"`
}

func browserSessionHandler(ctx context.Context, _ *mcp.CallToolRequest, args browserSessionArgs) (*mcp.CallToolResult, any, error) {
	result, err := globalBrowser.getDriver().Session(ctx, args.Action)
	if err != nil {
		return errResult(err.Error())
	}
	b, err := json.Marshal(result)
	if err != nil {
		return errResult("browser status encoding failed")
	}
	return textResult(string(b))
}
