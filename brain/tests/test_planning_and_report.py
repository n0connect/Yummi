"""End-to-end coverage for the planner -> supervisor -> specialist ->
summarize flow added on top of the original supervisor/specialist design:
a planner node that fetches skills/tactics/context once up front and
produces a structured plan, the supervisor rendering that plan into its
routing prompt, each specialist marking its own plan step done on return,
and a summarize node that synthesizes a final report instead of leaving
the operator with whatever the last specialist happened to say.

Drives a real compiled graph with fake chat models throughout (zero LLM
API calls) so every wiring seam — node registration, edges, state
threading — is actually exercised, not just each helper function in
isolation (those are covered directly in test_graph_recursion.py's sibling
style and inline in this file's plan-helper checks).
"""

from __future__ import annotations

import json

import pytest
from langchain_core.language_models.fake_chat_models import GenericFakeChatModel
from langchain_core.messages import AIMessage
from langchain_core.tools import tool
from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import StateGraph

from yummi_brain import graph, persona


class _FakeToolCallingModel(GenericFakeChatModel):
    def bind_tools(self, tools, **kwargs):  # noqa: ANN001, ANN003
        return self


@pytest.mark.asyncio
async def test_full_flow_produces_plan_marks_steps_and_synthesizes_report() -> None:
    pz = persona.load()

    # Planner's LLM call returns a two-step plan; supervisor routes to
    # "recon" once, then FINISH; summarize's own call returns the final
    # report text. Three distinct fake models because each node builds its
    # own ChatOpenAI-shaped client in the real graph — here we hand each
    # node the exact canned reply it needs, in call order.
    plan_json = json.dumps([
        {"role": "recon", "description": "Scan the target for open ports."},
        {"role": "web", "description": "Fingerprint any web service found."},
    ])
    planner_llm = _FakeToolCallingModel(messages=iter([AIMessage(content=plan_json)]))
    supervisor_llm = _FakeToolCallingModel(messages=iter([AIMessage(content="recon"), AIMessage(content="FINISH")]))
    summarize_llm = _FakeToolCallingModel(messages=iter([AIMessage(content="Final report: scanned target, done.")]))
    specialist_llm = _FakeToolCallingModel(
        messages=iter([AIMessage(content="Recon complete: port 22 and 80 open.", tool_calls=[])])
    )

    @tool
    def noop_tool(x: str) -> str:
        """Unused placeholder tool for the specialist's ReAct agent."""
        return "ok"

    builder = StateGraph(graph.BrainState)
    builder.add_node("planner", graph._planner_node(planner_llm, pz, {}))
    builder.add_node("supervisor", graph._supervisor_node(supervisor_llm, pz))
    # {} means tools_by_name.get("yummi_propose_skill") is None, so
    # _propose_skill_from_run's best-effort call is a no-op here — this test
    # is about plan/report flow, not skill-growth (see test_graph_skill_proposal.py).
    builder.add_node("summarize", graph._summarize_node(summarize_llm, pz, {}))
    builder.add_node("recon", graph._specialist_node("recon", specialist_llm, [noop_tool], pz))
    builder.set_entry_point("planner")
    compiled = builder.compile(checkpointer=MemorySaver())

    final_state = None
    async for event in compiled.astream(
        {"messages": [], "session_id": "test-session", "goal": "scan the target", "next": "", "plan": [], "report": ""},
        config={"configurable": {"thread_id": "t1"}, "recursion_limit": 25},
        stream_mode="values",
    ):
        final_state = event

    assert final_state is not None
    # Plan was created by the planner and the recon step got marked done
    # after the specialist reported back.
    plan = final_state["plan"]
    assert len(plan) == 2
    recon_step = next(s for s in plan if s["role"] == "recon")
    assert recon_step["status"] == "done"
    web_step = next(s for s in plan if s["role"] == "web")
    assert web_step["status"] == "pending"  # never routed to, correctly untouched

    # summarize actually ran and produced the final report, not just the
    # raw last specialist message.
    assert final_state["report"] == "Final report: scanned target, done."


@pytest.mark.asyncio
async def test_planner_degrades_to_fallback_plan_when_tools_unavailable() -> None:
    # No real MCP tools passed (empty tools_by_name) — _fetch_tool_text
    # must degrade to "" for each, and the planner must still produce SOME
    # plan (a knowledge-store hiccup must never block planning outright).
    pz = persona.load()
    planner_llm = _FakeToolCallingModel(messages=iter([AIMessage(content="garbled non-json reply")]))
    node = graph._planner_node(planner_llm, pz, {})

    result = await node({"messages": [], "session_id": "s1", "goal": "do the thing", "next": "", "plan": [], "report": ""})

    assert result.goto == "supervisor"
    assert result.update["plan"] == [{"id": "s1", "role": "", "description": "do the thing", "status": "pending"}]
