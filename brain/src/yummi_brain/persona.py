"""Loads Yummi's own persona definitions (persona/v0.1/*.yaml) so the brain's
supervisor and specialists share the SAME identity, tone, and reasoning
framework as Yummi's Go backends (graph/eino) — instead of a hand-written
approximation baked into graph.py that silently drifts the moment an operator
edits persona/v0.1 (a real gap: until this module existed, brain/ never
parsed a persona YAML at all — its prompts were entirely separate prose,
including core_identity.yaml's language_parity rule, which is why the brain
had no notion of answering in the operator's own language).

Read-only, loaded once at graph construction time — mirrors the Go
orchestrator's own load-once-at-startup persona.go pattern (persona.go
loads persona/v0.1 at process start; a load failure there logs a warning and
continues with an empty persona rather than failing engine construction —
this module follows the same shape, see load()'s error handling below).
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path

import yaml


def _persona_dir() -> Path:
    # Mirrors the Go orchestrator's own YUMMI_PERSONA_DIR env var (default
    # "persona/v0.1", resolved relative to its own working directory). brain/
    # has no equivalent working-directory convention of its own, so default
    # to the repo-relative path from this file's known location instead
    # (brain/src/yummi_brain/persona.py -> repo root is 3 parents up).
    configured = os.getenv("YUMMI_PERSONA_DIR")
    if configured:
        return Path(configured)
    return Path(__file__).resolve().parents[3] / "persona" / "v0.1"


def _load_yaml(path: Path) -> dict:
    with path.open("r", encoding="utf-8") as f:
        return yaml.safe_load(f) or {}


@dataclass(frozen=True)
class SubAgentPersona:
    role: str
    routing_hint: str
    identity: str
    principles: str
    planning: str


@dataclass(frozen=True)
class Persona:
    # core_identity.yaml — shared across supervisor and every specialist.
    character_role: str = ""
    honesty: str = ""
    economy: str = ""
    language_parity: str = ""
    # agent.yaml — the general reasoning framework every specialist inherits,
    # same as Go's eino backend folding Config's persona fields into its
    # specialists' system prompts once at run start (context_blocks.go).
    agent_identity: str = ""
    agent_principles: str = ""
    agent_planning: str = ""
    # subagents/*.yaml, keyed by the YAML's own `role` field (e.g. "recon",
    # "source-analysis" — hyphenated for that one, matching the file exactly
    # rather than inventing a separate underscored convention).
    subagents: dict[str, SubAgentPersona] = field(default_factory=dict)

    @property
    def loaded(self) -> bool:
        return bool(self.character_role or self.subagents)


_EMPTY = Persona()


def load(persona_dir: Path | None = None) -> Persona:
    """Best-effort load. A missing or malformed persona directory returns an
    empty Persona (all fields "", no subagents) rather than raising — the
    same nil-safe-degradation the rest of Yummi's own code follows, so a
    brain/ checkout that doesn't sit next to persona/ (e.g. copied
    standalone) still runs, just without persona-flavored prompts.
    """
    d = persona_dir or _persona_dir()
    try:
        core = _load_yaml(d / "core_identity.yaml")
        agent = _load_yaml(d / "agent.yaml")
    except OSError:
        return _EMPTY

    character = core.get("character") or {}
    disposition = character.get("disposition") or {}

    subagents: dict[str, SubAgentPersona] = {}
    subagents_dir = d / "subagents"
    if subagents_dir.is_dir():
        for path in sorted(subagents_dir.glob("*.yaml")):
            try:
                sub = _load_yaml(path)
            except OSError:
                continue
            role = sub.get("role") or path.stem
            subagents[role] = SubAgentPersona(
                role=role,
                routing_hint=(sub.get("routing_hint") or "").strip(),
                identity=(sub.get("identity") or "").strip(),
                principles=(sub.get("principles") or "").strip(),
                planning=(sub.get("planning") or "").strip(),
            )

    return Persona(
        character_role=(character.get("role") or "").strip(),
        honesty=(disposition.get("honesty") or "").strip(),
        economy=(disposition.get("economy") or "").strip(),
        language_parity=(disposition.get("language_parity") or "").strip(),
        agent_identity=(agent.get("identity") or "").strip(),
        agent_principles=(agent.get("principles") or "").strip(),
        agent_planning=(agent.get("planning") or "").strip(),
        subagents=subagents,
    )
