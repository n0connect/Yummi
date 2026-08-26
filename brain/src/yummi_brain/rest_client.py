"""Thin async client over the orchestrator's existing REST API.

Every method here maps to a route already defined in
orchestrator/internal/api/routes.go and already exercised by the web UI —
this file adds no new server-side behavior, it only calls what exists.
Deliberately NOT a general-purpose REST client: each method matches exactly
one MCP tool in mcp_server.py, so the mapping stays obvious to audit.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import httpx


class YummiAPIError(RuntimeError):
    """Raised when the orchestrator rejects a request — e.g. HandleSandboxExec
    returning 403 because RunScopedCommand found the target out of scope.
    The message is deliberately the server's own detail text: the model
    reading this needs to know WHY (out of scope vs. gateway unavailable vs.
    validation error), not just that something failed.
    """


@dataclass
class YummiClient:
    base_url: str
    timeout_secs: float = 300.0  # a real nmap/nikto/gobuster run can take minutes

    async def _request(self, method: str, path: str, **kwargs: Any) -> dict[str, Any]:
        async with httpx.AsyncClient(timeout=self.timeout_secs) as client:
            resp = await client.request(method, f"{self.base_url}{path}", **kwargs)
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
        return resp.json()

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
