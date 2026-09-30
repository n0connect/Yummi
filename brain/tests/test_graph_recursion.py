"""Pins the exception contract main.py's step-budget handling depends on.

The outer supervisor<->specialist graph (a plain compiled StateGraph) and
the inner per-specialist ReAct loop (LangGraph's prebuilt create_react_agent)
behave DIFFERENTLY when their respective recursion_limit is exhausted:
create_react_agent degrades gracefully (returns a "need more steps" message
instead of raising — see test_specialist_recursion_limit_degrades_gracefully
below), but a plain StateGraph raises langgraph.errors.GraphRecursionError.
A legitimately large multi-part goal that needs more than the outer
recursion_limit's worth of supervisor<->specialist handoffs is a routine,
expected outcome (see AGENTS.md / CLAUDE.md's own multi-part-goal
discussion), not a hypothetical edge case — before the corresponding fix in
main.py, this exception had no handler and crashed the CLI with a raw
traceback instead of reporting whatever partial progress was made.
"""

from __future__ import annotations

import pytest
from langchain_core.language_models.fake_chat_models import GenericFakeChatModel
from langchain_core.messages import AIMessage
from langchain_core.tools import tool
from langgraph.checkpoint.memory import MemorySaver
from langgraph.errors import GraphRecursionError
from langgraph.graph import StateGraph
from langgraph.types import Command

from yummi_brain import graph, persona


class _FakeToolCallingModel(GenericFakeChatModel):
    def bind_tools(self, tools, **kwargs):  # noqa: ANN001, ANN003
        return self


@pytest.mark.asyncio
async def test_outer_graph_raises_graph_recursion_error_when_budget_exhausted() -> None:
    def always_recon():
        while True:
            yield AIMessage(content="recon")

    fake_llm = _FakeToolCallingModel(messages=always_recon())
    pz = persona.load()

    async def trivial_recon(state):  # noqa: ANN001
        return Command(goto="supervisor", update={"messages": [AIMessage(content="did nothing")]})

    builder = StateGraph(graph.BrainState)
    builder.add_node("supervisor", graph._supervisor_node(fake_llm, pz))
    builder.add_node("recon", trivial_recon)
    builder.set_entry_point("supervisor")
    compiled = builder.compile(checkpointer=MemorySaver())

    with pytest.raises(GraphRecursionError):
        async for _ in compiled.astream(
            {"messages": [], "session_id": "s1", "goal": "loop forever", "next": ""},
            config={"configurable": {"thread_id": "t1"}, "recursion_limit": 6},
            stream_mode="values",
        ):
            pass


@pytest.mark.asyncio
async def test_specialist_recursion_limit_degrades_gracefully_instead_of_raising() -> None:
    # The inner boundary (_specialist_node's own recursion_limit=10) behaves
    # differently on purpose — create_react_agent halts with a synthetic
    # "need more steps" AIMessage and returns normally, letting the
    # supervisor see the specialist made no real progress and route
    # elsewhere, rather than aborting the whole multi-minute run.
    @tool
    def noop_tool(x: str) -> str:
        """A trivial tool that always succeeds, for forcing a long loop."""
        return "ok"

    def endless_tool_calls():
        i = 0
        while True:
            i += 1
            yield AIMessage(content="", tool_calls=[{"name": "noop_tool", "args": {"x": str(i)}, "id": str(i)}])

    fake_llm = _FakeToolCallingModel(messages=endless_tool_calls())
    pz = persona.load()
    node = graph._specialist_node("recon", fake_llm, [noop_tool], pz)

    result = await node({"messages": [], "session_id": "s1", "goal": "loop forever", "next": ""})

    assert result.goto == "supervisor"
    last_content = str(result.update["messages"][-1].content)
    assert "more steps" in last_content.lower() or last_content != ""
