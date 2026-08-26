"""Adversarial-peer coverage for rest_client.YummiClient. Uses a real local
HTTP server (stdlib http.server, a genuine socket peer — not a mock of
httpx or of YummiClient itself) to exercise the failure modes an
unreachable, slow, or misbehaving orchestrator can actually produce:
connection refused, a peer that accepts the connection and then never
responds, a truncated response, and a 200 OK whose body isn't valid JSON.

Every one of these must surface as YummiAPIError — that's the one
exception type every yummi_* MCP tool catches (mcp_server.py) to turn a
failure into a normal "REJECTED: ..." string the model can reason about,
instead of an uncaught traceback.
"""

from __future__ import annotations

import http.server
import socket
import threading
import time
from collections.abc import Callable, Iterator
from contextlib import closing

import pytest

from yummi_brain.rest_client import YummiAPIError, YummiClient


def _free_port() -> int:
    with closing(socket.socket(socket.AF_INET, socket.SOCK_STREAM)) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class _HangingHandler(http.server.BaseHTTPRequestHandler):
    """Accepts the TCP connection, reads the request, then never writes a
    response — the "target accepts the connection but stays silent"
    scenario. The client's own read timeout is the only thing that can
    end this.
    """

    def do_GET(self) -> None:  # noqa: N802 - stdlib naming convention
        time.sleep(5)  # longer than the short client timeout the test uses

    def log_message(self, format: str, *args: object) -> None:  # noqa: A002
        pass


class _MalformedJSONHandler(http.server.BaseHTTPRequestHandler):
    """Returns HTTP 200 with a body that isn't valid JSON."""

    def do_GET(self) -> None:  # noqa: N802
        body = b"this is not json {["
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format: str, *args: object) -> None:  # noqa: A002
        pass


class _TruncatedHandler(http.server.BaseHTTPRequestHandler):
    """Declares a Content-Length larger than the body it actually sends,
    then closes the connection — a peer that starts responding and cuts
    off mid-stream.
    """

    def do_GET(self) -> None:  # noqa: N802
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", "10000")
        self.end_headers()
        self.wfile.write(b'{"partial"')
        self.connection.close()

    def log_message(self, format: str, *args: object) -> None:  # noqa: A002
        pass


def _run_server(handler_cls: type[http.server.BaseHTTPRequestHandler], port: int) -> http.server.HTTPServer:
    server = http.server.HTTPServer(("127.0.0.1", port), handler_cls)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


@pytest.fixture
def adversarial_server() -> Iterator[Callable[[type], str]]:
    servers: list[http.server.HTTPServer] = []

    def start(handler_cls: type[http.server.BaseHTTPRequestHandler]) -> str:
        port = _free_port()
        servers.append(_run_server(handler_cls, port))
        return f"http://127.0.0.1:{port}"

    yield start
    for s in servers:
        s.shutdown()


@pytest.mark.asyncio
async def test_connection_refused_surfaces_as_yummi_api_error() -> None:
    # Nothing listens on this port at all.
    port = _free_port()
    client = YummiClient(base_url=f"http://127.0.0.1:{port}", timeout_secs=2.0)
    try:
        with pytest.raises(YummiAPIError):
            await client.system_status()
    finally:
        await client.aclose()


@pytest.mark.asyncio
async def test_hanging_peer_times_out_as_yummi_api_error(adversarial_server) -> None:
    base_url = adversarial_server(_HangingHandler)
    client = YummiClient(base_url=base_url, timeout_secs=1.0)
    try:
        start = time.monotonic()
        with pytest.raises(YummiAPIError):
            await client.system_status()
        elapsed = time.monotonic() - start
        # Must actually respect the configured timeout, not the handler's
        # 5-second sleep — this is the whole point of the test.
        assert elapsed < 4.0
    finally:
        await client.aclose()


@pytest.mark.asyncio
async def test_malformed_json_200_response_surfaces_as_yummi_api_error(adversarial_server) -> None:
    base_url = adversarial_server(_MalformedJSONHandler)
    client = YummiClient(base_url=base_url, timeout_secs=2.0)
    try:
        with pytest.raises(YummiAPIError):
            await client.system_status()
    finally:
        await client.aclose()


@pytest.mark.asyncio
async def test_truncated_response_surfaces_as_yummi_api_error(adversarial_server) -> None:
    base_url = adversarial_server(_TruncatedHandler)
    client = YummiClient(base_url=base_url, timeout_secs=2.0)
    try:
        with pytest.raises(YummiAPIError):
            await client.system_status()
    finally:
        await client.aclose()
