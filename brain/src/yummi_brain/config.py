"""Environment configuration. Mirrors the naming style of Yummi's Go env
vars (YUMMI_* prefix) so an operator reading .yummi.conf recognizes the
pattern immediately.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

from dotenv import load_dotenv

load_dotenv()


@dataclass(frozen=True)
class Config:
    # The orchestrator's own REST API — same one the Yummi web UI talks to.
    # This process never opens a socket to the gateway or the sandbox
    # container itself; the orchestrator remains the single owner of both.
    orchestrator_url: str

    # OpenRouter (or any OpenAI-compatible endpoint) for the brain's model.
    # Deliberately independent of the Go orchestrator's own YUMMI_OLLAMA_*/
    # YUMMI_EINO_MODEL_* config — this is a fully separate agent process.
    model_base_url: str
    model_name: str
    model_api_key: str

    # Session the brain operates under — every REST call and MCP tool call
    # is scoped to this session id, matching how the Go backends key task/
    # execution/finding records.
    session_id: str

    # Where LangGraph's checkpointer persists graph state (SqliteSaver) —
    # separate file from the orchestrator's own sessions.db by design; this
    # process has no business writing into Go's database directly.
    checkpoint_db_path: str


def load() -> Config:
    orchestrator_url = os.getenv("YUMMI_ORCHESTRATOR_URL", "http://127.0.0.1:9080").rstrip("/")
    model_base_url = os.getenv("YUMMI_BRAIN_MODEL_BASE_URL", "https://openrouter.ai/api/v1")
    model_name = os.getenv("YUMMI_BRAIN_MODEL_NAME", "anthropic/claude-3.5-sonnet")
    model_api_key = os.getenv("YUMMI_BRAIN_MODEL_API_KEY", "")
    session_id = os.getenv("YUMMI_BRAIN_SESSION_ID", "brain-default")
    checkpoint_db_path = os.getenv("YUMMI_BRAIN_CHECKPOINT_DB", "yummi_brain_checkpoints.sqlite")

    if not model_api_key:
        raise RuntimeError(
            "YUMMI_BRAIN_MODEL_API_KEY is not set. This is your OpenRouter (or other "
            "OpenAI-compatible provider) API key — the brain has no local-model fallback, "
            "unlike the Go orchestrator's eino backend. See brain/.env.example."
        )

    return Config(
        orchestrator_url=orchestrator_url,
        model_base_url=model_base_url,
        model_name=model_name,
        model_api_key=model_api_key,
        session_id=session_id,
        checkpoint_db_path=checkpoint_db_path,
    )
