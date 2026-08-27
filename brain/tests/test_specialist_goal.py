"""Pins down that a specialist's system prompt actually carries the
operator's goal. This was a real, live-confirmed bug: _specialist_system()
never took a goal parameter at all, and BrainState["messages"] starts empty
and is never written to by the supervisor (only "next" is updated) — so on
the very first supervisor->specialist handoff, the specialist's prompt
contained its role description and nothing else. It never crashed or
errored, so nothing surfaced the gap directly: the model just filled in a
plausible-looking target from whatever it noticed in a tool's own output
(a pre-existing gateway rule for an unrelated network) and confidently
scanned that instead, misreporting success. Live-observed twice, with two
different capable models (Nvidia Nemotron 3 Super and DeepSeek V3.1),
against two different unrelated goals it invented instead of the real one
— this was never a model-quality problem, the goal text simply never
reached the specialist to begin with.
"""

from __future__ import annotations

from yummi_brain import graph, persona


def test_specialist_system_prompt_contains_the_actual_goal() -> None:
    pz = persona.load()
    goal = "Target: 45.33.32.156 — run a top-ports nmap scan and report open ports."
    system = graph._specialist_system("recon", pz, pz.subagents.get("recon"), "sess-1", goal)
    assert goal in system


def test_specialist_system_warns_against_substituting_a_noticed_target() -> None:
    # The specific failure mode observed live: the model saw an unrelated
    # target in a tool's own output (e.g. an existing gateway rule) and
    # treated it as if it were the assigned goal. The prompt must say not
    # to do that, not just state the goal once and hope.
    pz = persona.load()
    system = graph._specialist_system("recon", pz, pz.subagents.get("recon"), "sess-1", "scan X")
    lowered = system.lower()
    assert "goal" in lowered
    assert "substitut" in lowered or "notice" in lowered or "another target" in lowered
