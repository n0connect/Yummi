package main

import (
	"context"
)

// BrowserDriver defines the modular abstraction for browser automation.
// Both the primary Go-Rod implementation (RodDriver) and any future alternative
// (such as a Playwright or CDP subprocess bridge) satisfy this exact interface,
// completely decoupling MCP tool dispatch and agent protocols from the underlying driver.
type BrowserDriver interface {
	// Open navigates to the given URL and returns page metadata along with an initial snapshot.
	Open(ctx context.Context, p OpenParams) (*PageInfo, error)

	// Snapshot generates a compact, token-efficient Accessibility Tree snapshot
	// with numbered @eN references for interactive elements.
	Snapshot(ctx context.Context, interactiveOnly, compact bool, depth int, selector string) (*SnapshotResult, error)

	// Interact performs an action against an @eN reference, selector, or viewport coordinates.
	Interact(ctx context.Context, p InteractParams) (*InteractResult, error)

	// Eval executes arbitrary JavaScript within the page execution context.
	Eval(ctx context.Context, script string) (any, error)

	// Screenshot captures the viewport or full page and saves it to outputFile.
	Screenshot(ctx context.Context, fullPage, annotate bool, outputFile string) (*ScreenshotResult, error)

	// Storage handles cookie inspection, setting, or saving/restoring full session state.
	Storage(ctx context.Context, p StorageParams) (*StorageResult, error)

	// Tabs manages multiple browser tabs (list, new, switch, close).
	Tabs(ctx context.Context, action string, tabID int, url string) (*TabsResult, error)

	// Logs retrieves buffered browser console messages and network traffic.
	Logs(ctx context.Context, filter string, clear bool) (*LogsResult, error)

	// Close terminates the browser session and frees resources.
	Close() error
	Session(ctx context.Context, action string) (map[string]any, error)
}

// OpenParams defines parameters for browser_open.
type OpenParams struct {
	URL       string            `json:"url"`
	WaitUntil string            `json:"wait_until,omitempty"`
	Session   string            `json:"session,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Headless  *bool             `json:"headless,omitempty"`
}

// InteractParams defines parameters for browser_interact.
type InteractParams struct {
	Action       string  `json:"action"`
	Ref          string  `json:"ref,omitempty"`
	Selector     string  `json:"selector,omitempty"`
	Value        string  `json:"value,omitempty"`
	ScrollDir    string  `json:"scroll_dir,omitempty"`
	ScrollAmount int     `json:"scroll_amount,omitempty"`
	X            float64 `json:"x,omitempty"`
	Y            float64 `json:"y,omitempty"`
	ToX          float64 `json:"to_x,omitempty"`
	ToY          float64 `json:"to_y,omitempty"`
	ToRef        string  `json:"to_ref,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	Duration     float64 `json:"duration,omitempty"`
}

// StorageParams defines parameters for browser_storage.
type StorageParams struct {
	Action string `json:"action"`
	Path   string `json:"path,omitempty"`
	Name   string `json:"name,omitempty"`
	Value  string `json:"value,omitempty"`
	Domain string `json:"domain,omitempty"`
}

// PageInfo represents initial metadata after a page navigation.
type PageInfo struct {
	URL             string          `json:"url"`
	Title           string          `json:"title"`
	HTTPStatus      int             `json:"http_status,omitempty"`
	InitialSnapshot *SnapshotResult `json:"initial_snapshot,omitempty"`
}

// SnapshotElement represents an element indexed with an @eN reference.
type SnapshotElement struct {
	Ref         string            `json:"ref"`         // e.g. "@e1"
	Role        string            `json:"role"`        // e.g. "button", "input", "link", "form"
	Name        string            `json:"name"`        // accessible name / inner text
	Type        string            `json:"type"`        // input type (text, password, submit, etc.)
	Value       string            `json:"value"`       // current value
	Placeholder string            `json:"placeholder"` // input placeholder
	Href        string            `json:"href"`        // link target
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// SnapshotResult carries the rendered text tree and ref mappings.
type SnapshotResult struct {
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	TreeText string            `json:"tree_text"` // compact formatted text for the LLM prompt
	Elements []SnapshotElement `json:"elements,omitempty"`
}

// InteractResult reports the outcome of a click, fill, or other interaction.
type InteractResult struct {
	Action       string `json:"action"`
	Target       string `json:"target"`
	Success      bool   `json:"success"`
	Navigated    bool   `json:"navigated,omitempty"`
	CurrentURL   string `json:"current_url,omitempty"`
	CurrentTitle string `json:"current_title,omitempty"`
	Message      string `json:"message"`
}

// ScreenshotResult reports where a captured screenshot was written.
type ScreenshotResult struct {
	FilePath  string `json:"file_path"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Annotated bool   `json:"annotated"`
}

// StorageResult holds cookie or session state data.
type StorageResult struct {
	Action  string                       `json:"action"`
	Path    string                       `json:"path,omitempty"`
	Cookies []map[string]any             `json:"cookies,omitempty"`
	Storage map[string]map[string]string `json:"storage,omitempty"`
	Message string                       `json:"message"`
}

// TabItem represents an open tab.
type TabItem struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	IsActive bool   `json:"is_active"`
}

// TabsResult represents tab operations.
type TabsResult struct {
	Action    string    `json:"action"`
	ActiveTab int       `json:"active_tab"`
	Tabs      []TabItem `json:"tabs"`
}

// LogEntry is a captured console message or network failure.
type LogEntry struct {
	Type      string `json:"type"` // "console.log", "console.error", "network.failed"
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

// LogsResult holds collected logs.
type LogsResult struct {
	TotalEntries int        `json:"total_entries"`
	Entries      []LogEntry `json:"entries"`
}
