package sandboxmcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Real Docker transport, MCP process and Chromium. No injected transport.
func TestLiveBrowserSurvivesCompletedCallContext(t *testing.T) {
	if os.Getenv("YUMMI_REAL_BROWSER_QC") != "true" {
		t.Skip("explicit real sandbox browser QC required")
	}
	client := NewClient("yummi-sandbox")
	t.Cleanup(func() { _ = client.Close() })
	first, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	opened, err := client.CallTool(first, "browser_open", map[string]any{
		"url": "http://127.0.0.1:18888/browser-qa.html", "headless": true, "session": "lifetime-qc",
	})
	cancel() // execution.Store cancels each completed call's context too.
	if err != nil || opened.IsError || !strings.Contains(textOf(opened), "Yummi Browser Quality Lab") {
		t.Fatalf("real browser open: %v %s", err, textOf(opened))
	}
	next, finish := context.WithTimeout(context.Background(), 30*time.Second)
	defer finish()
	snapshot, err := client.CallTool(next, "browser_snapshot", map[string]any{})
	if err != nil || snapshot.IsError || !strings.Contains(textOf(snapshot), "Yummi Browser Quality Lab") {
		t.Fatalf("browser state lost when previous call completed: %v %s", err, textOf(snapshot))
	}
	filled, err := client.CallTool(next, "browser_interact", map[string]any{"action": "fill", "ref": "@e1", "value": "OpenSSH"})
	if err != nil || filled.IsError {
		t.Fatalf("real ref lost between calls: %v %s", err, textOf(filled))
	}
}
