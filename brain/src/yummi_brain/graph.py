"""The LangGraph brain: a supervisor node routes to one of Yummi's existing
specialist roles (mirrors persona/v0.1/subagents/*.yaml — recon, web, osint,
exploiter, source-analysis, reverser), each a ReAct tool-calling loop bound
to the yummi_* MCP tools (mcp_server.py). Deterministic routing edges, not
LLM-guessed control flow — the supervisor's decision is the only place an
LLM call picks the next node; everything else is a plain graph edge. This
is the reason LangGraph was chosen over a more autonomous multi-agent
framework: for a security tool, the graph itself should be legible and
auditable independent of what any single model call decides.
"""

from __future__ import annotations

import sys
from typing import Literal, TypedDict

from langchain_mcp_adapters.client import MultiServerMCPClient
from langchain_openai import ChatOpenAI
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
from langgraph.graph import END, StateGraph
from langgraph.prebuilt import create_react_agent
from langgraph.types import Command

from . import config

# Mirrors persona/v0.1/subagents/*.yaml's roster and routing_hint field —
# kept in sync by hand for now (both are hand-authored role definitions in
# their respective languages; a future pass could generate this from the
# YAML directly, see AGENTS.md for the tracked TODO).
ROSTER: dict[str, str] = {
    "recon": (
        "You perform reconnaissance: port/service scanning, host discovery, "
        "OS/service fingerprinting. Route here first for any target Yummi "
        "hasn't scanned yet in this session."
    ),
    "web": (
        "You assess web applications: technology fingerprinting, directory/"
        "file discovery, CMS-specific checks, common web vulnerability classes."
    ),
    "osint": (
        "You perform passive open-source reconnaissance: subdomain "
        "enumeration, DNS records, email/credential exposure — nothing that "
        "touches the target directly."
    ),
    "exploiter": (
        "You confirm and exploit an ALREADY-SCOPED, already-identified "
        "weakness (injection, known CVE, credential issue) — you don't go "
        "looking for new attack surface, you weaponize a specific finding."
    ),
    "source_analysis": (
        "You review source code, configuration, or leaked artifacts pulled "
        "from the target for hardcoded secrets, logic flaws, or dependency "
        "vulnerabilities."
    ),
    "reverser": (
        "You reverse-engineer a binary or compiled artifact recovered from "
        "the target."
    ),
}

SUPERVISOR_SYSTEM = """You are the supervisor of a penetration-testing agent \
team working inside Yummi's fail-closed sandbox. Given the operator's goal \
and the conversation so far, decide which specialist should act next, or \
that the goal is genuinely complete.

Specialists and when to route to them:
{roster}

Rules:
- Route to exactly one specialist per turn. They report back to you.
- Only choose "FINISH" when EVERY distinct part of the operator's goal has \
been genuinely attempted — not just the first one or two. Re-read the goal \
before finishing; a goal with several distinct asks needs several distinct \
specialist turns, not one.
- If a specialist reports it made no real progress, either try a different \
specialist or a different specific instruction — do not send it right back \
with the same instruction that already failed.
"""


class BrainState(TypedDict):
    messages: list
    session_id: str
    goal: str
    next: str


def _supervisor_node(llm: ChatOpenAI):
    roster_desc = "\n".join(f"- {name}: {desc}" for name, desc in ROSTER.items())
    options = list(ROSTER.keys()) + ["FINISH"]

    async def node(state: BrainState) -> Command[Literal[tuple(ROSTER.keys()) + ("__end__",)]]:  # type: ignore[misc]
        system = SUPERVISOR_SYSTEM.format(roster=roster_desc)
        history = state["messages"]
        prompt = [
            {"role": "system", "content": system},
            {"role": "user", "content": f"Operator's goal: {state['goal']}"},
            *history,
            {
                "role": "user",
                "content": f"Who should act next? Reply with EXACTLY one of: {', '.join(options)}. "
                "Nothing else.",
            },
        ]
        reply = await llm.ainvoke(prompt)
        choice = str(reply.content).strip().strip('"').split()[0] if reply.content else "FINISH"
        if choice not in options:
            # A model that doesn't follow the exact-token instruction is a
            # real, expected failure mode — fail closed to FINISH rather
            # than crash the graph on an invalid Command target.
            choice = "FINISH"
        goto = END if choice == "FINISH" else choice
        return Command(goto=goto, update={"next": choice})

    return node


def _specialist_node(role: str, llm: ChatOpenAI, tools: list):
    async def node(state: BrainState) -> Command[Literal["supervisor"]]:
        # session_id is only known at runtime (BrainState), but every
        # yummi_* tool's schema requires it as a mandatory argument — a
        # system prompt built once at graph-construction time (before any
        # state exists) has no way to include it. Live-observed without
        # this: the model correctly refused to call any tool at all,
        # since it had a mandatory parameter it was never told the value
        # of, and just kept asking the operator for a session_id in a
        # loop until the supervisor gave up and routed to FINISH — zero
        # real tool calls made across an entire run. Rebuilding the agent
        # per invocation (cheap — this wraps an existing llm/tools, it's
        # not a new model load) with session_id baked into the system
        # prompt is the reliable fix, independent of whether this
        # LangGraph version's create_react_agent prompt= accepts a
        # per-call callable.
        system = (
            f"You are Yummi's {role} specialist, operating inside a fail-closed "
            f"pentest sandbox. {ROSTER[role]}\n\n"
            f"The session_id for EVERY yummi_* tool call in this conversation is "
            f"exactly: {state['session_id']}\n"
            "Every yummi_* tool requires this as its session_id argument — always "
            "pass this exact value, never invent or omit one.\n\n"
            "Use the yummi_* tools to do real work — run real commands, request "
            "egress for targets outside the current scope, record real findings. "
            "Report back concretely: what you ran, what you found. Do not "
            "declare success without evidence from an actual tool call."
        )
        agent = create_react_agent(llm, tools, prompt=system)
        # recursion_limit here bounds ONE specialist's own internal ReAct
        # turns — separate from, and not covered by, the outer graph's own
        # recursion_limit (main.py's config, which only counts
        # supervisor<->specialist handoffs as steps; a single specialist
        # invocation is one of those steps no matter how many internal
        # turns it takes). Without this, LangGraph's create_react_agent
        # default (25) applies per specialist call, so the real worst-case
        # cost is (outer limit) x 25, not just the outer limit — expensive
        # in both wall-clock time and, on a rate-limited free-tier model,
        # API quota. 10 is generous for the kind of single-target
        # reconnaissance/assessment step this graph asks one specialist to
        # do per turn.
        result = await agent.ainvoke({"messages": state["messages"]}, config={"recursion_limit": 10})
        return Command(
            goto="supervisor",
            update={"messages": [*result["messages"][len(state["messages"]) :]]},
        )

    return node


async def build_graph(cfg: config.Config | None = None):
    """Constructs the compiled graph. Returns (graph, mcp_client, saver_cm).
    mcp_client owns the stdio subprocess running mcp_server.py — nothing
    currently needs to close it explicitly (see main.py's own note on why).
    saver_cm is the checkpointer's own context manager; the caller MUST
    call `await saver_cm.__aexit__(None, None, None)` when done, or the
    checkpoint sqlite connection is never released.
    """
    cfg = cfg or config.load()

    mcp_client = MultiServerMCPClient(
        {
            "yummi": {
                "command": sys.executable,
                "args": ["-m", "yummi_brain.mcp_server"],
                "transport": "stdio",
            }
        }
    )
    tools = await mcp_client.get_tools()

    llm = ChatOpenAI(
        base_url=cfg.model_base_url,
        api_key=cfg.model_api_key,
        model=cfg.model_name,
        temperature=0,
    )

    builder = StateGraph(BrainState)
    builder.add_node("supervisor", _supervisor_node(llm))
    for role in ROSTER:
        builder.add_node(role, _specialist_node(role, llm, tools))
    builder.set_entry_point("supervisor")

    saver_cm = AsyncSqliteSaver.from_conn_string(cfg.checkpoint_db_path)
    checkpointer = await saver_cm.__aenter__()
    graph = builder.compile(checkpointer=checkpointer)
    return graph, mcp_client, saver_cm
