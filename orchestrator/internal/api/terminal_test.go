package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTerminalControlMsgResize(t *testing.T) {
	data := `{"type":"resize","cols":120,"rows":40}`
	var msg terminalControlMsg
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		t.Fatalf("unmarshal resize msg: %v", err)
	}
	if msg.Type != "resize" {
		t.Errorf("expected type resize, got %q", msg.Type)
	}
	if msg.Cols != 120 {
		t.Errorf("expected cols 120, got %d", msg.Cols)
	}
	if msg.Rows != 40 {
		t.Errorf("expected rows 40, got %d", msg.Rows)
	}
}

func TestTerminalControlMsgUnknownType(t *testing.T) {
	// Unknown control type must parse without error (backend silently ignores it).
	data := `{"type":"ping"}`
	var msg terminalControlMsg
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		t.Fatalf("unmarshal unknown msg: %v", err)
	}
	if msg.Type != "ping" {
		t.Errorf("got type %q", msg.Type)
	}
	// Non-resize messages should have zero dimensions.
	if msg.Cols != 0 || msg.Rows != 0 {
		t.Errorf("expected zero cols/rows for non-resize msg, got cols=%d rows=%d", msg.Cols, msg.Rows)
	}
}

func TestTerminalControlMsgResizeZeroDimensions(t *testing.T) {
	// Resize with zero cols/rows is invalid — handler should ignore it.
	data := `{"type":"resize","cols":0,"rows":0}`
	var msg terminalControlMsg
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// cols=0 and rows=0 → handler guard `ctrl.Cols > 0 && ctrl.Rows > 0` rejects it.
	if msg.Cols != 0 || msg.Rows != 0 {
		t.Errorf("expected zero dimensions, got cols=%d rows=%d", msg.Cols, msg.Rows)
	}
}

func TestTerminalHandlerBridgeNil(t *testing.T) {
	handler := HandleTerminalWSBridge(nil)
	req := httptest.NewRequest(http.MethodGet, "/ws/terminal", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when bridge nil, got %d", w.Code)
	}
}
