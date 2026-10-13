"""Real API, sandbox state, Chromium UI and MCP; no substituted components."""
import json
import os
import pathlib
import socketserver
import threading
import urllib.error
import urllib.request

import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))

from browser_research_qc import BASE, MCP, api
from research_ui_qc import UIForwarder


def main():
    assert os.geteuid() == 1000 and pathlib.Path("/.dockerenv").is_file()
    path = pathlib.Path("/tmp/yummi_web_search_state")
    backup = pathlib.Path("/tmp/yummi_web_search_state.qc-backup")
    assert not backup.exists() and not path.is_dir()
    original = path.read_bytes() if path.exists() else None
    initial = api("/system/web-search")["enabled"]
    server = socketserver.ThreadingTCPServer(("127.0.0.1", 0), UIForwarder)
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    client = MCP()
    try:
        client.tool("browser_open", {"url": "http://127.0.0.1:" + str(server.server_address[1]), "headless": True})
        client.tool("browser_interact", {"action": "wait_for_selector", "selector": ".btn-web-search", "duration": 10})
        client.tool("browser_interact", {"action": "wait", "duration": 1})
        before = client.tool("browser_eval", {"script": "document.querySelector('.btn-web-search').getAttribute('aria-pressed')"})
        if original is not None:
            path.rename(backup)
        path.mkdir()
        try:
            try:
                api("/system/web-search", {"enabled": True})
                raise AssertionError("State write failed but real API reported success")
            except urllib.error.HTTPError as error:
                assert error.code == 500
            client.tool("browser_interact", {"action": "click", "selector": ".btn-web-search"})
            client.tool("browser_interact", {"action": "wait", "duration": 1})
            after = client.tool("browser_eval", {"script": "document.querySelector('.btn-web-search').getAttribute('aria-pressed')"})
            assert before == after, "UI falsely acknowledged rejected toggle"
        finally:
            path.rmdir()
            if backup.exists():
                backup.rename(path)
        api("/system/web-search", {"enabled": False})
        assert api("/system/web-search")["enabled"] is False
        client.tool("web_fetch", {"url": "https://www.openssh.com"}, expect_error=True)
        print(json.dumps({"status": "passed", "checks": ["real write failure returns HTTP500", "UI keeps acknowledged state on HTTP500", "real disabled MCP fetch fails closed"]}))
    finally:
        api("/system/web-search", {"enabled": initial})
        if original is None:
            path.unlink(missing_ok=True)
        else:
            path.write_bytes(original)
        client.close()
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
