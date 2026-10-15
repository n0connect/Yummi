"""Inspect deployed UI with real Chromium in the disposable operator QA image."""
import argparse
import json
import pathlib
import time

from browser_research_qc import MCP
from operator_context import UI_URL, require_operator_ui



def main():
    require_operator_ui()
    parser = argparse.ArgumentParser()
    parser.add_argument("session_id")
    args = parser.parse_args()
    client = MCP()
    try:
        client.tool("browser_open", {"url": UI_URL + "/", "headless": True, "session": "ui-qc"})
        client.tool("browser_interact", {"action": "wait", "duration": 1})
        client.tool("browser_eval", {"script": "sessionStorage.setItem('yummi:chat:sessionId', " + json.dumps(args.session_id) + ")"})
        # browser_open intentionally creates a new tab; sessionStorage is
        # tab-local, so reload this actual tab instead of replacing it.
        client.tool("browser_eval", {"script": "setTimeout(() => location.reload(), 100); true"})
        client.tool("browser_interact", {"action": "wait", "duration": 1})
        deadline = time.monotonic() + 30
        cards = []
        while time.monotonic() < deadline:
            client.tool("browser_eval", {"script": "document.querySelectorAll('.msg-execution[aria-expanded=false] .execution-summary').forEach(b => b.click())"})
            cards = json.loads(client.tool("browser_eval", {"script": "Array.from(document.querySelectorAll('.research-source')).map(a => ({url:a.querySelector('a').href,text:a.innerText,rel:a.querySelector('a').rel}))"}))
            if cards:
                break
            time.sleep(1)
        if not cards:
            print(client.tool("browser_eval", {"script": "({sid:sessionStorage.getItem('yummi:chat:sessionId'),executions:document.querySelectorAll('.msg-execution').length,text:document.body.innerText.slice(-5000)})"}))
            print(client.tool("browser_logs", {"filter": "error"})[:3000])
        assert cards, "Real persisted research output did not render source cards"
        assert all(c["url"].startswith("https://") and "noopener" in c["rel"] and "SHA-256" in c["text"] for c in cards)
        client.tool("browser_screenshot", {"output_file": "/home/pentester/work/research-ui-desktop.png"})
        client.tool("browser_interact", {"action": "set_viewport", "width": 375, "height": 812})
        client.tool("browser_screenshot", {"output_file": "/home/pentester/work/research-ui-mobile.png"})
        report = {"status": "passed", "session_id": args.session_id, "cards": cards,
                  "screenshots": ["research-ui-desktop.png", "research-ui-mobile.png"]}
        pathlib.Path("/home/pentester/work/research-ui-qc.json").write_text(json.dumps(report, indent=2))
        print(json.dumps({"status": "passed", "source_cards": len(cards)}))
    finally:
        client.close()


if __name__ == "__main__":
    main()
