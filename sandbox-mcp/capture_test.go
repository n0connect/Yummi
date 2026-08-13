package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunCaptured_Success(t *testing.T) {
	result, _, err := runCaptured(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatalf("runCaptured: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got IsError with content %+v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "hello") {
		t.Fatalf("expected output containing 'hello', got %+v", result.Content)
	}
}

func TestRunCaptured_NonZeroExit(t *testing.T) {
	result, _, err := runCaptured(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err != nil {
		t.Fatalf("runCaptured returned a Go error for a normal non-zero exit: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for a non-zero exit")
	}
}

func TestRunCaptured_LaunchFailure(t *testing.T) {
	result, _, err := runCaptured(context.Background(), "yummi-definitely-not-a-real-binary")
	if err != nil {
		t.Fatalf("runCaptured returned a Go error for a launch failure: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true when the binary does not exist")
	}
}

func TestCappedBuffer_Truncates(t *testing.T) {
	b := newCappedBuffer(10)
	_, _ = b.Write([]byte("0123456789ABCDEF"))
	if !strings.HasSuffix(b.String(), truncationMark) {
		t.Fatalf("expected truncation marker, got %q", b.String())
	}
	if len(b.String()) != 10+len(truncationMark) {
		t.Fatalf("expected capped length, got %d", len(b.String()))
	}
}
