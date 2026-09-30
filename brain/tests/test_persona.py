"""Negative-path coverage for persona.load(). persona/v0.1 is hand-edited
YAML — an operator typo (bad indentation, a stray dash turning a mapping
into a list, a half-saved file) is a routine, expected event, not a
hypothetical. load()'s own docstring promises "a missing or malformed
persona directory returns an empty Persona... rather than raising" — these
tests hold that promise to account. They were written FIRST, against the
old code, and failed (AttributeError / yaml.YAMLError propagating out of
load()) before the corresponding fix in persona.py landed.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from yummi_brain import persona


def _write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def test_missing_directory_degrades_to_empty(tmp_path: Path) -> None:
    pz = persona.load(tmp_path / "does-not-exist")
    assert pz.loaded is False
    assert pz.subagents == {}


def test_core_identity_root_is_a_list_not_a_mapping(tmp_path: Path) -> None:
    # A plausible operator typo: an extra leading "- " turns the intended
    # mapping into a YAML sequence. persona.py used to call .get() on
    # whatever _load_yaml() returned, unconditionally.
    _write(tmp_path / "core_identity.yaml", "- a\n- b\n")
    _write(tmp_path / "agent.yaml", "identity: fine\n")
    pz = persona.load(tmp_path)
    assert pz.character_role == ""


def test_agent_yaml_root_is_a_bare_scalar(tmp_path: Path) -> None:
    _write(tmp_path / "core_identity.yaml", "character:\n  role: fine\n")
    _write(tmp_path / "agent.yaml", "just a bare string\n")
    pz = persona.load(tmp_path)
    assert pz.character_role == "fine"
    assert pz.agent_identity == ""


def test_malformed_yaml_syntax_in_core_identity(tmp_path: Path) -> None:
    # Genuine syntax error (unterminated quoted scalar), not just an
    # unexpected-but-valid root type — this raises yaml.YAMLError, a
    # different exception class than the AttributeError cases above.
    _write(tmp_path / "core_identity.yaml", 'character:\n  role: "unterminated\n  x: {broken\n')
    _write(tmp_path / "agent.yaml", "identity: fine\n")
    pz = persona.load(tmp_path)
    assert pz.character_role == ""
    assert pz.agent_identity == "fine"


def test_one_bad_subagent_file_does_not_take_down_the_rest(tmp_path: Path) -> None:
    _write(tmp_path / "core_identity.yaml", "character:\n  role: fine\n")
    _write(tmp_path / "agent.yaml", "identity: fine\n")
    _write(tmp_path / "subagents" / "recon.yaml", "role: recon\nidentity: good\n")
    _write(tmp_path / "subagents" / "web.yaml", "not a mapping, just a string\n")
    _write(tmp_path / "subagents" / "osint.yaml", 'role: osint\nidentity: "unterminated\n')

    pz = persona.load(tmp_path)

    assert pz.character_role == "fine"
    assert "recon" in pz.subagents
    assert pz.subagents["recon"].identity == "good"
    # web.yaml and osint.yaml were individually malformed — they must be
    # skipped, not silently present with garbage content, and must not
    # have prevented recon.yaml (a sibling, perfectly valid file) from
    # loading.
    assert "web" not in pz.subagents
    assert "osint" not in pz.subagents


def test_empty_files_degrade_cleanly(tmp_path: Path) -> None:
    _write(tmp_path / "core_identity.yaml", "")
    _write(tmp_path / "agent.yaml", "")
    _write(tmp_path / "subagents" / "recon.yaml", "")
    pz = persona.load(tmp_path)
    assert pz.character_role == ""
    assert pz.subagents["recon"].identity == ""


def test_real_persona_directory_still_loads_correctly() -> None:
    # Regression guard the other way: the fix must not have broken parsing
    # of Yummi's own real, well-formed persona/v0.1.
    real_dir = Path(__file__).resolve().parents[2] / "persona" / "v0.1"
    if not real_dir.is_dir():
        pytest.skip("persona/v0.1 not present in this checkout")
    pz = persona.load(real_dir)
    assert pz.loaded is True
    assert "recon" in pz.subagents
    assert pz.language_parity != ""
