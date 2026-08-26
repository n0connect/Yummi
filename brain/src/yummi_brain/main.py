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
        thread = {"configurable": {"thread_id": cfg.session_id}}
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
        await mcp_client.__aexit__(None, None, None) if hasattr(mcp_client, "__aexit__") else None
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
