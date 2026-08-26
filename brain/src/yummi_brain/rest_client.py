"""Thin async client over the orchestrator's existing REST API.

Every method here maps to a route already defined in
orchestrator/internal/api/routes.go and already exercised by the web UI —
this file adds no new server-side behavior, it only calls what exists.
Deliberately NOT a general-purpose REST client: each method matches exactly
one MCP tool in mcp_server.py, so the mapping stays obvious to audit.
"""

from __future__ import annotations

import json as jsonlib
from dataclasses import dataclass, field
from typing import Any

import httpx


class YummiAPIError(RuntimeError):
    """Raised for EVERY way a request to the orchestrator can fail — an HTTP
    4xx/5xx (e.g. HandleSandboxExec returning 403 because RunScopedCommand
    found the target out of scope), a transport-level failure (orchestrator
    unreachable, connection refused, timeout), or a 2xx response whose body
    isn't valid JSON. mcp_server.py's tools all catch only this one type —
    letting httpx's own exception hierarchy leak through was a real gap
    found in review: an unreachable orchestrator (down, wrong URL, network
    blip) would have raised httpx.ConnectError/TimeoutException instead,
    which every tool's `except YummiAPIError` doesn't match, so the model
    would see a raw Python traceback instead of a normal "REJECTED: ..."
    it can reason about and retry or report. The message is deliberately
    the server's own detail text where one exists: the model needs to know
    WHY (out of scope vs. gateway unavailable vs. validation error), not
    just that something failed.
    """


@dataclass
class YummiClient:
    base_url: str
    timeout_secs: float = 300.0  # a real nmap/nikto/gobuster run can take minutes
    # One shared client for this process's lifetime instead of opening a
    # fresh connection (TCP handshake + TLS if applicable) on every single
    # tool call — a real task makes dozens of these in a run. httpx.AsyncClient
    # is explicitly documented as safe to reuse and pool connections across
    # concurrent requests.
    _http: httpx.AsyncClient = field(init=False, repr=False)

    def __post_init__(self) -> None:
        self._http = httpx.AsyncClient(timeout=self.timeout_secs)

    async def aclose(self) -> None:
        await self._http.aclose()

    async def _request(self, method: str, path: str, **kwargs: Any) -> dict[str, Any]:
        try:
            resp = await self._http.request(method, f"{self.base_url}{path}", **kwargs)
        except httpx.RequestError as e:
            # Covers ConnectError, TimeoutException, ReadError, and every
            # other transport-level failure httpx can raise — none of them
            # are YummiAPIError by default, so without this they'd bypass
            # every tool's error handling entirely (see class doc above).
            raise YummiAPIError(f"{method} {path} -> could not reach orchestrator: {e}") from e
        if resp.status_code >= 400:
            detail = resp.text
            try:
                body = resp.json()
                detail = body.get("detail") or body.get("title") or detail
            except Exception:
                pass
            raise YummiAPIError(f"{method} {path} -> {resp.status_code}: {detail}")
        if not resp.content:
            return {}
        try:
            return resp.json()
        except jsonlib.JSONDecodeError as e:
            raise YummiAPIError(
                f"{method} {path} -> 200 OK but response body isn't valid JSON: {e}"
            ) from e

    # -- execution -----------------------------------------------------------
    # POST /api/v1/sandbox/exec — internal/api/handlers_system.go's
    # HandleSandboxExec. Blocking: returns once the command finishes. All
    # scope enforcement happens server-side inside RunScopedCommand — this
    # client sends the raw command and gets back exit_code/stdout/stderr,
    # nothing more, nothing less.
    async def exec_command(self, command: str, session_id: str) -> dict[str, Any]:
        return await self._request(
            "POST", "/api/v1/sandbox/exec",
            json={"command": command, "session_id": session_id},
        )

    # -- scope / gateway -------------------------------------------------------
    async def get_gateway_status(self) -> dict[str, Any]:
        return await self._request("GET", "/api/v1/gateway/status")

    async def list_gateway_rules(self) -> dict[str, Any]:
        return await self._request("GET", "/api/v1/gateway/rules")

    async def get_egress_mode(self) -> dict[str, Any]:
        return await self._request("GET", "/api/v1/gateway/egress-mode")

    async def add_temporary_rule(self, network: str, reason: str, ttl_secs: int = 300) -> dict[str, Any]:
        return await self._request(
            "POST", "/api/v1/gateway/rules",
            json={"network": network, "ttl_secs": ttl_secs, "reason": reason},
        )

    async def read_gateway_audit(self, limit: int = 20) -> dict[str, Any]:
        return await self._request("GET", "/api/v1/gateway/audit", params={"limit": limit})

    # -- findings --------------------------------------------------------------
    async def record_finding(
        self, session_id: str, title: str, severity: str, description: str = "", evidence: str = ""
    ) -> dict[str, Any]:
        return await self._request(
            "POST", f"/api/v1/sessions/{session_id}/findings",
            json={"title": title, "severity": severity, "description": description, "evidence": evidence},
        )

    async def list_findings(self, session_id: str) -> dict[str, Any]:
        return await self._request("GET", f"/api/v1/sessions/{session_id}/findings")

    # -- health ------------------------------------------------------------------
    async def system_status(self) -> dict[str, Any]:
        return await self._request("GET", "/api/v1/system/status")
