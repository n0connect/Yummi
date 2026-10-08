"""Coverage for brain's skill-growth parity gap closing: a brain-driven task
previously never fed Go's skillcurator, so it never grew the skill library
the way a graph/eino run does automatically on finish. _propose_skill_from_run
is the fire-and-forget hook that closes that gap from summarize_node — see
its own doc comment in graph.py for the design rationale (same best-effort,
can-never-block-the-task contract as _fetch_tool_text).
"""

from __future__ import annotations

from typing import Any

import pytest
from langchain_core.messages import AIMessage, HumanMessage

from yummi_brain import graph


class _RecordingTool:
    """Stands in for the real yummi_propose_skill MCP tool object — records
    exactly what it was invoked with instead of making a real HTTP call.
    """

    def __init__(self, *, raise_error: bool = False) -> None:
        self.calls: list[dict[str, Any]] = []
        self.raise_error = raise_error

    async def ainvoke(self, args: dict[str, Any]) -> str:
        self.calls.append(args)
        if self.raise_error:
            raise RuntimeError("orchestrator unreachable")
        return '{"accepted":true}'


def _state(report: str = "Scanned the target, found an open port.") -> graph.BrainState:
    return {
        "messages": [
            HumanMessage(content="scan 198.51.100.10"),
            AIMessage(content="nmap found port 80 open"),
        ],
        "session_id": "sess-123",
        "goal": "scan 198.51.100.10 and report open ports",
        "next": "",
        "plan": [],
        "report": report,
    }


@pytest.mark.asyncio
async def test_propose_skill_calls_tool_with_goal_and_report() -> None:
    tool = _RecordingTool()
    await graph._propose_skill_from_run({"yummi_propose_skill": tool}, _state(), "Found port 80 open, fingerprinted Apache.")

    assert len(tool.calls) == 1
    call = tool.calls[0]
    assert call["task_id"] == "brain:sess-123"
    assert call["session_id"] == "sess-123"
    assert call["goal"] == "scan 198.51.100.10 and report open ports"
    assert call["summary"] == "Found port 80 open, fingerprinted Apache."
    assert any("port 80" in obs for obs in call["observations"])


@pytest.mark.asyncio
async def test_propose_skill_is_noop_when_tool_not_available() -> None:
    # The common case: brain/ wired without MCPExec, or the tool genuinely
    # missing from the catalog — must degrade silently, never raise.
    await graph._propose_skill_from_run({}, _state(), "Found port 80 open.")


@pytest.mark.asyncio
async def test_propose_skill_skips_empty_report() -> None:
    tool = _RecordingTool()
    await graph._propose_skill_from_run({"yummi_propose_skill": tool}, _state(), "   ")
    assert tool.calls == []


@pytest.mark.asyncio
async def test_propose_skill_swallows_tool_errors() -> None:
    # Fire-and-forget: a REJECTED/unreachable orchestrator must never
    # propagate out of summarize_node and fail the whole task.
    tool = _RecordingTool(raise_error=True)
    await graph._propose_skill_from_run({"yummi_propose_skill": tool}, _state(), "Found port 80 open.")
    assert len(tool.calls) == 1  # it was called, just failed — and that's fine
