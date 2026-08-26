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

import logging
import os
from dataclasses import dataclass, field
from pathlib import Path

import yaml

logger = logging.getLogger(__name__)


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


def _load_yaml_dict(path: Path) -> dict | None:
    """Loads one YAML file and returns it as a dict, or None to signal
    "skip this file entirely". persona/v0.1 is hand-edited by operators —
    a syntax error (yaml.YAMLError) or a root that parses fine but isn't a
    mapping (e.g. a stray leading "- " turning it into a list) are both
    routine typos, not exceptional conditions, and must not propagate:
    every caller below builds a Persona out of whatever files DID parse
    correctly, rather than the whole load failing over one bad file. An
    empty file legitimately parses to None from yaml.safe_load and is
    treated as a valid empty document ({}), distinct from a genuine error.
    """
    try:
        with path.open("r", encoding="utf-8") as f:
            data = yaml.safe_load(f)
    except (OSError, yaml.YAMLError) as e:
        logger.warning("skipping unreadable persona file %s: %s", path, e)
        return None
    if data is None:
        return {}
    if not isinstance(data, dict):
        logger.warning(
            "skipping %s: expected a YAML mapping at the document root, got %s",
            path, type(data).__name__,
        )
        return None
    return data


def _str(value: object) -> str:
    # A field like `honesty:` or `routing_hint:` is expected to be a plain
    # scalar string in every real persona/v0.1 file, but a malformed edit
    # could turn it into a list or nested mapping — treat anything that
    # isn't actually a string as absent rather than crashing on .strip().
    return value.strip() if isinstance(value, str) else ""


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


def load(persona_dir: Path | None = None) -> Persona:
    """Best-effort load. A missing or malformed persona directory returns an
    empty Persona (all fields "", no subagents) rather than raising — the
    same nil-safe-degradation the rest of Yummi's own code follows, so a
    brain/ checkout that doesn't sit next to persona/ (e.g. copied
    standalone) still runs, just without persona-flavored prompts.
    """
    d = persona_dir or _persona_dir()

    # core_identity.yaml and agent.yaml are loaded independently — a syntax
    # error in one must not discard a perfectly good other, or good
    # subagents/*.yaml files below.
    core = _load_yaml_dict(d / "core_identity.yaml") or {}
    agent = _load_yaml_dict(d / "agent.yaml") or {}

    character = core.get("character")
    character = character if isinstance(character, dict) else {}
    disposition = character.get("disposition")
    disposition = disposition if isinstance(disposition, dict) else {}

    subagents: dict[str, SubAgentPersona] = {}
    subagents_dir = d / "subagents"
    if subagents_dir.is_dir():
        for path in sorted(subagents_dir.glob("*.yaml")):
            sub = _load_yaml_dict(path)
            if sub is None:
                continue
            role = _str(sub.get("role")) or path.stem
            subagents[role] = SubAgentPersona(
                role=role,
                routing_hint=_str(sub.get("routing_hint")),
                identity=_str(sub.get("identity")),
                principles=_str(sub.get("principles")),
                planning=_str(sub.get("planning")),
            )

    return Persona(
        character_role=_str(character.get("role")),
        honesty=_str(disposition.get("honesty")),
        economy=_str(disposition.get("economy")),
        language_parity=_str(disposition.get("language_parity")),
        agent_identity=_str(agent.get("identity")),
        agent_principles=_str(agent.get("principles")),
        agent_planning=_str(agent.get("planning")),
        subagents=subagents,
    )
