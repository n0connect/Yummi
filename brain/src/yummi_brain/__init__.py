"""yummi_brain: a frontier-model agent brain (LangGraph) that drives Yummi's
fail-closed pentest substrate (Rust gateway + Kali sandbox) over MCP.

This package never touches the sandbox or gateway directly — every tool call
goes through the orchestrator's existing REST API (internal/api/routes.go),
which is the same fail-closed, gateway-authoritative path the Go graph/eino
backends already use (see internal/executor.RunScopedCommand). This package
adds NO new execution path and NO new attack surface: it is a thin protocol
translator (MCP <-> REST) plus a LangGraph orchestration layer on top.
"""

__version__ = "0.1.0"
