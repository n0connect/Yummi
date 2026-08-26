"""The agent-facing Yummi substrate — an MCP server ANY MCP client can
connect to (LangGraph via yummi_brain.graph, Claude Desktop, Cursor,
Windsurf, OpenHands, Cline, or a custom agent). Every tool call is
translated 1:1 into a call against the orchestrator's existing REST API
(rest_client.YummiClient) — never a direct connection to the gateway socket
or the sandbox container. That boundary is deliberate: the orchestrator
stays the single owner of the gateway/sandbox connections and the single
place execution is scope-checked, threatscanned, and audit-logged
(internal/executor.RunScopedCommand, internal/api/handlers_system.go's
HandleSandboxExec). This file adds a protocol translator, not a second
execution path.

Run standalone: `uv run yummi-mcp-server` (stdio transport — the standard
way an MCP client like Claude Desktop spawns a server).
"""

from __future__ import annotations

import json
import os

from mcp.server.fastmcp import FastMCP

from .rest_client import YummiAPIError, YummiClient

mcp = FastMCP("yummi")
# Deliberately NOT config.load(): this server only ever needs the
# orchestrator's URL, never a model API key — config.load() validates
# YUMMI_BRAIN_MODEL_API_KEY, which is a yummi_brain.graph concern, not this
# server's. Coupling the two would make this server (meant to be usable
# standalone by any MCP client, not just this package's own LangGraph brain)
# refuse to start for a client that brings its own model.
_client = YummiClient(base_url=(os.getenv("YUMMI_ORCHESTRATOR_URL") or "http://127.0.0.1:9080").rstrip("/"))


def _fmt(obj: object) -> str:
    return json.dumps(obj, indent=2, default=str)


@mcp.tool()
async def yummi_exec(command: str, session_id: str, rationale: str) -> str:
    """Run a shell command inside Yummi's Kali sandbox. The gateway enforces
    scope fail-closed: a command whose target isn't allowed is rejected, not
    silently dropped or worked around. rationale is recorded for the audit
    trail — explain what this command is for, not just what it does.
    """
    try:
        result = await _client.exec_command(command=command, session_id=session_id)
    except YummiAPIError as e:
        return f"REJECTED: {e}"
    lines = [f"exit_code={result.get('exit_code')}"]
    if result.get("warning"):
        lines.append(f"WARNING: {result['warning']}")
    if result.get("stdout"):
        lines.append("--- stdout ---\n" + result["stdout"])
    if result.get("stderr"):
        lines.append("--- stderr ---\n" + result["stderr"])
    return "\n".join(lines)


@mcp.tool()
async def yummi_get_scope(session_id: str) -> str:
    """Read the operator-set boundary: gateway egress mode (block/allow/ask),
    active temporary rules, and connection status. Call this before assuming
    a target is reachable — it is the ONLY source of truth for what's
    currently allowed, the gateway enforces it independent of anything this
    tool reports.
    """
    try:
        status = await _client.get_gateway_status()
        rules = await _client.list_gateway_rules()
        egress = await _client.get_egress_mode()
    except YummiAPIError as e:
        return f"REJECTED: {e}"
    return _fmt({"gateway_status": status, "egress_mode": egress, "active_rules": rules})


@mcp.tool()
async def yummi_request_egress(target: str, reason: str, ttl_secs: int = 300) -> str:
    """Ask the gateway to temporarily allow reaching `target`. target MUST be
    a literal IP or CIDR (e.g. "203.0.113.10/32") — a hostname is rejected;
    resolve it to an IP first (e.g. `dig +short <host>` via yummi_exec) if
    you only have a hostname. reason must be 1-500 characters, letters/
    numbers/spaces/basic punctuation only ( _ - . , ! ? ' " # ( ) [ ] ) — no
    colons, slashes, or non-ASCII characters (Turkish included), or the
    gateway rejects it. This does NOT bypass the operator's boundary — if
    egress mode is "block", the rule is still added but traffic to anything
    outside it stays fail-closed; the operator's egress mode is the outer
    boundary this can never widen. ttl_secs is capped at 86400 (24h)
    server-side.
    """
    try:
        result = await _client.add_temporary_rule(network=target, reason=reason, ttl_secs=ttl_secs)
    except YummiAPIError as e:
        return f"REJECTED: {e}"
    return _fmt(result)


@mcp.tool()
async def yummi_record_finding(
    session_id: str, title: str, severity: str, description: str = "", evidence: str = ""
) -> str:
    """Record a structured finding. severity must be one of: info, low,
    medium, high, critical. title must be 1-200 characters, letters/numbers/
    spaces/basic punctuation only ( _ - . , ! ? ' " # ( ) [ ] ) — no colons,
    slashes, or non-ASCII characters (Turkish included); put anything that
    needs those in description or evidence instead, which have no character
    restriction (only length caps: description 5000 chars, evidence 10000).
    Use this for anything you'd put in a pentest report — don't just mention
    it in your own summary text, it needs to exist as a durable, queryable
    record.
    """
    try:
        result = await _client.record_finding(
            session_id=session_id, title=title, severity=severity, description=description, evidence=evidence
        )
    except YummiAPIError as e:
        return f"REJECTED: {e}"
    return _fmt(result)


@mcp.tool()
async def yummi_list_findings(session_id: str) -> str:
    """List every finding recorded so far in this session — use this to
    check what's already been documented before declaring the task done.
    """
    try:
        result = await _client.list_findings(session_id)
    except YummiAPIError as e:
        return f"REJECTED: {e}"
    return _fmt(result)


@mcp.tool()
async def yummi_read_gateway_audit(limit: int = 20) -> str:
    """Read the gateway's tamper-evident (SHA-256 hash-chained) audit log —
    every scope decision, rule change, and egress-mode change. Use this to
    understand WHY a command was rejected, not just that it was.
    """
    try:
        result = await _client.read_gateway_audit(limit=limit)
    except YummiAPIError as e:
        return f"REJECTED: {e}"
    return _fmt(result)


def run() -> None:
    mcp.run(transport="stdio")


if __name__ == "__main__":
    run()
