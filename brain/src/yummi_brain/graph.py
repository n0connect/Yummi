"""The LangGraph brain: a supervisor node routes to one of Yummi's existing
specialist roles (persona/v0.1/subagents/*.yaml — recon, web, osint,
exploiter, source-analysis, reverser), each a ReAct tool-calling loop bound
to the yummi_* MCP tools (mcp_server.py). Deterministic routing edges, not
LLM-guessed control flow — the supervisor's decision is the only place an
LLM call picks the next node; everything else is a plain graph edge. This
is the reason LangGraph was chosen over a more autonomous multi-agent
framework: for a security tool, the graph itself should be legible and
auditable independent of what any single model call decides.

Roster and persona are loaded from persona/v0.1 (persona.py), not
hand-duplicated here — a brain running on a hand-written approximation of
Yummi's identity is not really running as Yummi, and it silently drifts the
moment an operator edits persona/v0.1 without knowing this module exists.
"""

from __future__ import annotations

import asyncio
import json
import logging
import sys
from typing import Any, Awaitable, Callable, Literal, TypedDict, TypeVar

from langchain_mcp_adapters.client import MultiServerMCPClient
from langchain_openai import ChatOpenAI
from langgraph.checkpoint.sqlite.aio import AsyncSqliteSaver
from langgraph.graph import END, StateGraph
from langgraph.prebuilt import create_react_agent
from langgraph.types import Command

from . import config
from . import persona as persona_mod
from .persona import Persona, SubAgentPersona

logger = logging.getLogger(__name__)
_T = TypeVar("_T")


async def _with_retry(call: Callable[[], Awaitable[_T]], *, attempts: int = 3, base_delay: float = 5.0) -> _T:
    """Retries a transient upstream LLM-call failure a few times with linear
    backoff. A free-tier OpenRouter model's shared provider pool getting
    momentarily rate-limited or overloaded is routine, not a bug — live-
    observed across four different backend providers (Decart, GMICloud,
    Nvidia) in a single session — and crashing an entire multi-minute run
    over one blip wastes far more than a short retry costs. Deliberately
    catches Exception broadly rather than specific status-code types:
    some providers wrap a genuine 502 inside an HTTP 200 response body,
    which surfaces from langchain_openai as a bare ValueError, not an
    HTTP-level error the OpenAI SDK's own retry logic would ever see.
    """
    last_exc: Exception | None = None
    for attempt in range(1, attempts + 1):
        try:
            return await call()
        except Exception as e:  # noqa: BLE001 - intentionally broad, see docstring
            last_exc = e
            if attempt < attempts:
                delay = base_delay * attempt
                logger.warning(
                    "transient LLM call failure (attempt %d/%d): %s — retrying in %.0fs",
                    attempt, attempts, e, delay,
                )
                await asyncio.sleep(delay)
    assert last_exc is not None
    raise last_exc

# Used only if persona/v0.1/subagents fails to load at all (missing
# directory, bad checkout) — keeps the graph functional in degraded mode
# instead of compiling with zero specialist nodes. routing_hint text here
# is deliberately minimal; real behavior in the normal case comes entirely
# from the loaded YAML, not from this list.
_FALLBACK_ROLES: dict[str, str] = {
    "recon": "Port/service scanning and host discovery.",
    "web": "Web application assessment.",
    "osint": "Passive open-source reconnaissance.",
    "exploiter": "Confirms and exploits an already-scoped weakness.",
    "source-analysis": "Reviews source code or leaked artifacts.",
    "reverser": "Reverse-engineers a binary or compiled artifact.",
}

PLANNER_SYSTEM = """You are Yummi's mission planner. Given the operator's \
goal, produce a short ordered plan — the same discipline Yummi's other \
backends follow: cover every distinct part of the goal, as many steps as it \
genuinely needs and no filler, each step a concrete one-sentence subtask \
assigned to exactly one specialist role.

Specialist roles available:
{roster_desc}

Yummi's own reviewed skill guidance (use only what genuinely fits this \
goal — most of this will be irrelevant, that's normal, skim and ignore):
{skills_summary}

Known tactics — past command patterns and their outcomes, if any are \
relevant:
{tactics_summary}

Persistent notes from earlier work in this session, if any:
{context_summary}

Return ONLY a JSON array, nothing else — no markdown fences, no prose \
before or after. Each element: {{"role": "<one of the roles above>", \
"description": "<concrete subtask>"}}."""


def _planner_node(llm: ChatOpenAI, pz: Persona, tools_by_name: dict[str, Any]):
    roster = _roster(pz)
    roster_desc = "\n".join(f"- {name}: {desc}" for name, desc in roster.items())

    async def node(state: BrainState) -> Command[Literal["supervisor"]]:
        skills, tactics, context = await asyncio.gather(
            _fetch_tool_text(tools_by_name, "yummi_list_skills", {}),
            _fetch_tool_text(tools_by_name, "yummi_list_tactics", {}),
            _fetch_tool_text(tools_by_name, "yummi_get_context", {"session_id": state["session_id"]}),
        )
        system = PLANNER_SYSTEM.format(
            roster_desc=roster_desc,
            skills_summary=(skills[:3000] or "(none available)"),
            tactics_summary=(tactics[:2000] or "(none available)"),
            context_summary=(context[:1500] or "(none)"),
        )
        prompt = [
            {"role": "system", "content": system},
            {"role": "user", "content": f"Operator's goal: {state['goal']}"},
        ]
        reply = await _with_retry(lambda: llm.ainvoke(prompt))
        plan = _parse_plan(str(reply.content or ""), fallback_goal=state["goal"], valid_roles=set(roster.keys()))
        return Command(goto="supervisor", update={"plan": plan})

    return node


SUPERVISOR_RULES = """You are the supervisor of a penetration-testing agent \
team working inside Yummi's fail-closed sandbox. Given the operator's goal, \
the plan, and the conversation so far, decide which specialist should act \
next, or that the goal is genuinely complete.

Specialists and when to route to them:
{roster}

The plan below is a starting guide, not a rigid script — a specialist's \
actual findings can reveal a step is unnecessary, already covered, or that \
a different order makes more sense. Use it to stay organized, not to \
follow blindly.

Rules:
- Route to exactly one specialist per turn. They report back to you.
- Only choose "FINISH" when EVERY distinct part of the operator's goal has \
been genuinely attempted — not just the first one or two. Re-read the goal \
before finishing; a goal with several distinct asks needs several distinct \
specialist turns, not one. An unchecked plan step is a strong signal that \
goal is not yet done, but a checked step is not proof either — judge by \
the actual conversation.
- Judge progress by whether a specialist's report actually addresses the \
operator's goal, not by whether it did SOME work — a report about something \
unrelated to the goal is NOT progress, even if it looks like real tool \
output.
- If a specialist reports it made no real progress, or reports something \
unrelated to the goal, either try a different specialist or a clear, \
specific, corrective instruction that names the actual goal again — never \
send it back with the same instruction that already failed or drifted.
"""


class PlanStep(TypedDict):
    id: str
    role: str
    description: str
    status: str  # "pending" | "done"


class BrainState(TypedDict):
    messages: list
    session_id: str
    goal: str
    next: str
    plan: list[PlanStep]
    report: str


def _roster(pz: Persona) -> dict[str, str]:
    if pz.subagents:
        return {role: sub.routing_hint or sub.identity[:200] for role, sub in pz.subagents.items()}
    return _FALLBACK_ROLES


def _render_plan(plan: list[PlanStep]) -> str:
    if not plan:
        return "(no plan yet)"
    lines = []
    for step in plan:
        marker = "x" if step.get("status") == "done" else " "
        role = step.get("role") or "any"
        lines.append(f"[{marker}] ({role}) {step.get('description', '')}")
    return "\n".join(lines)


def _parse_plan(raw: str, *, fallback_goal: str, valid_roles: set[str]) -> list[PlanStep]:
    """Parses the planner LLM's JSON array reply into a validated plan.
    Falls back to a single catch-all step covering the whole goal on any
    parse failure or empty/invalid result — a planner that can't produce
    structured output must still let the run proceed unplanned, not crash
    it; the supervisor's own routing loop remains the real safety net
    either way.
    """
    text = raw.strip()
    if text.startswith("```"):
        text = text.strip("`")
        if "\n" in text:
            text = text.split("\n", 1)[1]
    start, end = text.find("["), text.rfind("]")
    if start != -1 and end != -1 and end > start:
        text = text[start : end + 1]
    try:
        items = json.loads(text)
        if not isinstance(items, list) or not items:
            raise ValueError("planner reply is not a non-empty JSON array")
        plan: list[PlanStep] = []
        for i, item in enumerate(items):
            if not isinstance(item, dict):
                continue
            role = item.get("role")
            description = item.get("description")
            if role not in valid_roles or not description:
                continue
            plan.append({"id": f"s{i + 1}", "role": str(role), "description": str(description), "status": "pending"})
        if plan:
            return plan
        raise ValueError("no valid steps survived validation")
    except Exception as e:  # noqa: BLE001 - any parse/shape failure degrades the same way
        logger.warning("planner: could not parse a structured plan (%s) — falling back to a single step", e)
        return [{"id": "s1", "role": "", "description": fallback_goal, "status": "pending"}]


def _mark_step_done(plan: list[PlanStep], role: str) -> list[PlanStep]:
    # Marks the first still-pending step assigned to this role as done —
    # a reasonable heuristic (the specialist that just reported back
    # presumably worked on the next thing planned for its role) without
    # asking the model to explicitly name which step it completed, which
    # would add another fragile parse to every specialist turn.
    updated: list[PlanStep] = []
    marked = False
    for step in plan:
        if not marked and step.get("role") == role and step.get("status") != "done":
            step = {**step, "status": "done"}
            marked = True
        updated.append(step)
    return updated


async def _fetch_tool_text(tools_by_name: dict[str, Any], name: str, args: dict[str, Any]) -> str:
    """Best-effort direct call to one already-loaded MCP tool object, bypassing
    the ReAct loop — the planner needs this data once, up front, not as
    something a model decides whether to ask for. Any failure (tool
    missing, orchestrator unreachable) degrades to an empty string so a
    knowledge-store hiccup can never block planning.
    """
    tool = tools_by_name.get(name)
    if tool is None:
        return ""
    try:
        result = await tool.ainvoke(args)
    except Exception as e:  # noqa: BLE001
        logger.warning("planner: %s fetch failed: %s", name, e)
        return ""
    return str(result)


def _supervisor_node(llm: ChatOpenAI, pz: Persona):
    roster = _roster(pz)
    roster_desc = "\n".join(f"- {name}: {desc}" for name, desc in roster.items())
    options = list(roster.keys()) + ["FINISH"]
    # character_role/honesty/economy set tone once — the same disposition
    # every Go backend's persona-driven prompt carries — even though the
    # supervisor itself never calls a yummi_* tool.
    tone = "\n\n".join(p for p in (pz.character_role, pz.honesty, pz.economy) if p)
    system = (tone + "\n\n" if tone else "") + SUPERVISOR_RULES.format(roster=roster_desc)

    async def node(state: BrainState) -> Command[Literal[tuple(roster.keys()) + ("summarize",)]]:  # type: ignore[misc]
        history = state["messages"]
        prompt = [
            {"role": "system", "content": system},
            {
                "role": "user",
                "content": f"Operator's goal: {state['goal']}\n\nPlan:\n{_render_plan(state.get('plan', []))}",
            },
            *history,
            {
                "role": "user",
                "content": f"Who should act next? Reply with EXACTLY one of: {', '.join(options)}. "
                "Nothing else.",
            },
        ]
        reply = await _with_retry(lambda: llm.ainvoke(prompt))
        choice = str(reply.content).strip().strip('"').split()[0] if reply.content else "FINISH"
        if choice not in options:
            # A model that doesn't follow the exact-token instruction is a
            # real, expected failure mode — fail closed to FINISH rather
            # than crash the graph on an invalid Command target.
            choice = "FINISH"
        # FINISH routes to summarize (one more LLM call that synthesizes a
        # clean final report) rather than straight to END — without this,
        # the operator's "result" was whatever the last specialist happened
        # to say, not a coherent answer to the actual goal.
        goto = "summarize" if choice == "FINISH" else choice
        return Command(goto=goto, update={"next": choice})

    return node


def _specialist_system(
    role: str, pz: Persona, sub: SubAgentPersona | None, session_id: str, goal: str
) -> str:
    # Layered exactly like Go's eino backend folds persona into a
    # specialist's prompt once per run (context_blocks.go): shared
    # core_identity (who Yummi is, tone, language_parity) + agent.yaml's
    # general reasoning framework (ORIENT/ACT/OBSERVE/CONCLUDE) + this
    # role's own identity/principles/planning from subagents/*.yaml.
    # language_parity in particular ("respond in the operator's own
    # language") had NO equivalent anywhere in brain/ before this — every
    # response was English regardless of the goal's language.
    parts = [
        pz.character_role,
        pz.honesty,
        pz.economy,
        pz.language_parity,
        pz.agent_identity,
        pz.agent_principles,
        pz.agent_planning,
    ]
    if sub:
        parts.append(f"Your specific role — {sub.role}:\n{sub.identity}")
        if sub.principles:
            parts.append(sub.principles)
        if sub.planning:
            parts.append(sub.planning)
    else:
        parts.append(f"Your specific role — {role}: {_FALLBACK_ROLES.get(role, '')}")

    identity_block = "\n\n".join(p for p in parts if p)

    return (
        f"{identity_block}\n\n"
        f"THE OPERATOR'S GOAL for this session is exactly:\n{goal}\n\n"
        "Everything you do must serve this goal and only this goal. If a "
        "tool's own output happens to mention some other target, network, "
        "or rule (an existing gateway rule, a stray entry in a list, "
        "anything not stated in the goal above) — that is NOT your target. "
        "Never substitute another target you merely noticed for the one "
        "the operator actually gave you, even if it looks plausible or is "
        "already in scope. If the goal above ever seems to conflict with "
        "what you were routed here to do, the goal wins.\n\n"
        f"The session_id for EVERY yummi_* tool call in this conversation is "
        f"exactly: {session_id}\n"
        "Every yummi_* tool requires this as its session_id argument — always "
        "pass this exact value, never invent or omit one.\n\n"
        "Before improvising: call yummi_list_skills and yummi_list_tactics "
        "(optionally filtered by tool) to check whether Yummi already has "
        "reviewed guidance or a known-working command pattern for THIS "
        "SPECIFIC situation — prefer that over guessing, but ONLY if it "
        "actually matches the task you were just handed. Skills cover many "
        "unrelated situations; skim the list, use the one that fits the "
        "current subtask, and ignore every other one. Never execute a "
        "skill's example technique just because it appeared in the list — "
        "if none of them fit what you were asked to do, proceed without "
        "one. Call yummi_get_context once near the start of your work to "
        "pick up any persistent notes left by earlier work in this "
        "session.\n\n"
        "Use the yummi_* tools to do real work — run real commands, request "
        "egress for targets outside the current scope, record real findings. "
        "IMPORTANT: requesting egress for one target does NOT by itself open "
        "external network access — the gateway has a separate operator-set "
        "egress mode (call yummi_get_scope to see it) that must already be "
        "'allow' or 'ask' for a requested rule to matter at all; while it is "
        "'block', every command against that target keeps failing with "
        "\"target not in scope\" no matter how many times you request egress "
        "or retry the same command. If you see this rejection more than "
        "once for the same target, check egress mode — if it is 'block', "
        "stop retrying (a specialist cannot change it) and report clearly "
        "that the operator needs to raise the gateway's egress mode before "
        "this subtask can proceed, instead of repeating the same failing "
        "action.\n\n"
        "Everything you do must serve the subtask you were actually handed "
        "— re-read it before each action. Report back concretely: what you "
        "ran, what you found, and how it answers the subtask. Do not "
        "declare success without evidence from an actual tool call, and "
        "never report an unrelated investigation as if it were progress on "
        "the assigned subtask."
    )


def _specialist_node(role: str, llm: ChatOpenAI, tools: list, pz: Persona):
    sub = pz.subagents.get(role)

    async def node(state: BrainState) -> Command[Literal["supervisor"]]:
        # session_id AND goal are only known at runtime (BrainState) — a
        # system prompt built once at graph-construction time (before any
        # state exists) has no way to include either. Live-observed without
        # goal specifically: the supervisor never writes to state["messages"]
        # (it only updates "next"), so on the first handoff a specialist's
        # only input was its own role description — no goal, no target. It
        # didn't error or refuse; it just picked up an unrelated target it
        # noticed in a tool's own output (an existing gateway rule) and
        # confidently scanned that instead, misreporting success. Without
        # session_id: the model correctly refused to call any tool at all,
        # since it had a mandatory parameter it was never told the value
        # of, and just kept asking the operator for a session_id in a
        # loop until the supervisor gave up and routed to FINISH — zero
        # real tool calls made across an entire run. Rebuilding the agent
        # per invocation (cheap — this wraps an existing llm/tools, it's
        # not a new model load) with session_id baked into the system
        # prompt is the reliable fix, independent of whether this
        # LangGraph version's create_react_agent prompt= accepts a
        # per-call callable.
        system = _specialist_system(role, pz, sub, state["session_id"], state["goal"])
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
        result = await _with_retry(
            lambda: agent.ainvoke({"messages": state["messages"]}, config={"recursion_limit": 10})
        )
        return Command(
            goto="supervisor",
            update={
                "messages": [*result["messages"][len(state["messages"]) :]],
                "plan": _mark_step_done(state.get("plan", []), role),
            },
        )

    return node


SUMMARY_SYSTEM = """You are Yummi, wrapping up a task for the operator. \
Given the goal, the plan, and everything that happened, write a concise \
final report: what was accomplished, concrete findings (cite actual \
evidence from the conversation below — ports, services, vulnerabilities, \
whatever was really found — never invent anything), and what, if anything, \
remains incomplete or was blocked and why. Plain text, no markdown headers \
needed."""


def _summarize_node(llm: ChatOpenAI, pz: Persona, tools_by_name: dict[str, Any]):
    system = SUMMARY_SYSTEM
    if pz.language_parity:
        system += "\n\n" + pz.language_parity

    async def node(state: BrainState) -> Command[Literal["__end__"]]:
        prompt = [
            {"role": "system", "content": system},
            {
                "role": "user",
                "content": f"Goal: {state['goal']}\n\nPlan:\n{_render_plan(state.get('plan', []))}",
            },
            *state["messages"],
            {"role": "user", "content": "Write the final report now."},
        ]
        reply = await _with_retry(lambda: llm.ainvoke(prompt))
        report = str(reply.content or "").strip()
        await _propose_skill_from_run(tools_by_name, state, report)
        return Command(goto=END, update={"report": report})

    return node


async def _propose_skill_from_run(tools_by_name: dict[str, Any], state: BrainState, report: str) -> None:
    """Closes brain's one real remaining parity gap with graph/eino: a
    brain-driven task previously never grew the skill library, since
    nothing here ever called Go's skillcurator on its behalf (that package
    is only ever invoked in-process by agent.Runner/EinoRunner, which brain
    has no equivalent of). Mirrors graph.go's finishNode / eino's runner.go
    exactly: fire-and-forget, after a run completes, never able to block or
    fail the task — same best-effort, degrade-to-nothing contract
    `_fetch_tool_text` already uses for this file's other direct tool
    calls. An empty or trivial report (nothing really happened) is
    deliberately skipped — DraftSkill would reject it anyway, no sense
    paying for the call.
    """
    report = report.strip()
    if not report:
        return
    tool = tools_by_name.get("yummi_propose_skill")
    if tool is None:
        return
    observations = [
        str(getattr(m, "content", "")).strip()[:500]
        for m in state["messages"][-6:]
        if str(getattr(m, "content", "")).strip()
    ]
    try:
        await tool.ainvoke(
            {
                "task_id": f"brain:{state['session_id']}",
                "session_id": state["session_id"],
                "goal": state["goal"],
                "summary": report,
                "observations": observations,
            }
        )
    except Exception as e:  # noqa: BLE001
        logger.warning("summarize: yummi_propose_skill failed: %s", e)


async def build_graph(cfg: config.Config | None = None):
    """Constructs the compiled graph. Returns (graph, mcp_client, saver_cm).
    mcp_client owns the stdio subprocess running mcp_server.py — nothing
    currently needs to close it explicitly (see main.py's own note on why).
    saver_cm is the checkpointer's own context manager; the caller MUST
    call `await saver_cm.__aexit__(None, None, None)` when done, or the
    checkpoint sqlite connection is never released.
    """
    cfg = cfg or config.load()
    pz = persona_mod.load()
    roster = _roster(pz)

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

    tools_by_name = {tool.name: tool for tool in tools}

    builder = StateGraph(BrainState)
    builder.add_node("planner", _planner_node(llm, pz, tools_by_name))
    builder.add_node("supervisor", _supervisor_node(llm, pz))
    builder.add_node("summarize", _summarize_node(llm, pz, tools_by_name))
    for role in roster:
        builder.add_node(role, _specialist_node(role, llm, tools, pz))
    builder.set_entry_point("planner")

    saver_cm = AsyncSqliteSaver.from_conn_string(cfg.checkpoint_db_path)
    checkpointer = await saver_cm.__aenter__()
    graph = builder.compile(checkpointer=checkpointer)
    return graph, mcp_client, saver_cm
