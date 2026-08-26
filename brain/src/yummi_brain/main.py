"""CLI entrypoint: run the LangGraph brain against a goal, end to end,
against a real running Yummi stack (orchestrator + gateway + sandbox).

    uv run yummi-brain "203.0.113.10 hedefinde ... 1) ... 2) ... 6) ..."
"""

from __future__ import annotations

import argparse
import asyncio
import sys

from . import config
from .graph import build_graph


async def run(goal: str) -> None:
    cfg = config.load()
    print(f"Target orchestrator: {cfg.orchestrator_url}")
    print(f"Model: {cfg.model_name} @ {cfg.model_base_url}")
    print(f"Session: {cfg.session_id}\n")

    graph, mcp_client, saver_cm = await build_graph(cfg)
    try:
        # recursion_limit is LangGraph's own step-budget equivalent — the
        # SAME class of bound orchestrator/internal/agent/eino/runner.go
        # needed hours of live debugging to get right today (unbounded
        # "let the model decide when it's done" is not safe for a tool
        # that runs real sandbox commands). LangGraph defaults to 25 if
        # unset, which is silent and easy to not notice; setting it
        # explicitly here makes the bound visible. This counts only
        # supervisor<->specialist handoffs (one specialist invocation is
        # one step here, however many internal ReAct turns it takes
        # inside) — see graph.py's own recursion_limit=10 on the
        # specialist's own agent.ainvoke for the inner bound.
        thread = {"configurable": {"thread_id": cfg.session_id}, "recursion_limit": 60}
        final_state = None
        async for event in graph.astream(
            {"messages": [], "session_id": cfg.session_id, "goal": goal, "next": ""},
            config=thread,
            stream_mode="values",
        ):
            final_state = event
            nxt = event.get("next")
            if nxt:
                print(f"--> routed to: {nxt}")
            for msg in event.get("messages", [])[-1:]:
                content = getattr(msg, "content", None)
                if content:
                    role = getattr(msg, "type", getattr(msg, "role", "?"))
                    print(f"[{role}] {str(content)[:2000]}")

        print("\n=== FINAL STATE ===")
        if final_state:
            for msg in final_state.get("messages", []):
                content = getattr(msg, "content", None)
                if content:
                    print(f"- {str(content)[:500]}")
    finally:
        # MultiServerMCPClient is NOT a context manager as of
        # langchain-mcp-adapters 0.1.0 (calling __aexit__ raises
        # NotImplementedError, which previously masked the real error from
        # whatever actually failed above) — each get_tools()/tool
        # invocation manages its own stdio subprocess lifecycle, nothing
        # here needs closing. Only the checkpointer's own context manager
        # needs an explicit exit.
        await saver_cm.__aexit__(None, None, None)


def cli() -> None:
    parser = argparse.ArgumentParser(description="Run Yummi's LangGraph brain against a goal.")
    parser.add_argument("goal", nargs="?", help="The operator's goal, in natural language.")
    args = parser.parse_args()
    goal = args.goal or sys.stdin.read().strip()
    if not goal:
        parser.error("no goal provided (pass as an argument or pipe via stdin)")
    asyncio.run(run(goal))


if __name__ == "__main__":
    cli()
