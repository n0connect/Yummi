"""Run inside yummi-sandbox only. Rigorous real-world edge-case bug hunting suite."""
import json
import os
import pathlib
import sys
import time

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from browser_research_qc import MCP


def test_multi_tab_workflow(client):
    print("TEST: Multi-Tab Lifecycle...", flush=True)
    client.tool("browser_session", {"action": "reset"})
    client.tool("browser_open", {
        "url": "http://127.0.0.1:18888/edge-cases.html",
        "headless": True,
        "session": "tabs-qc"
    })
    # Click popup link with target=_blank
    client.tool("browser_interact", {"action": "click", "selector": "#popup-link"})
    time.sleep(1)

    tabs_raw = client.tool("browser_tabs", {"action": "list"})
    assert "Tab 1:" in tabs_raw and "Tab 2:" in tabs_raw, f"Expected 2 tabs after popup click, got: {tabs_raw}"

    # Determine popup tab ID dynamically
    popup_tab_id = 1 if "tab=popup" in tabs_raw.split("Tab 2:")[0] else 2
    client.tool("browser_tabs", {"action": "switch", "tab_id": popup_tab_id})
    time.sleep(0.5)

    snapshot = client.tool("browser_snapshot", {"interactive_only": False})
    assert "Secret Admin Portal" in snapshot or "SECRETAUTH999" in snapshot, f"Popup content missing: {snapshot[:300]}"

    # Close popup tab
    client.tool("browser_tabs", {"action": "close", "tab_id": popup_tab_id})
    time.sleep(0.5)

    remaining_tabs = client.tool("browser_tabs", {"action": "list"})
    assert "Tab 1:" in remaining_tabs and "Tab 2:" not in remaining_tabs, f"Expected 1 tab after close, got: {remaining_tabs}"
    print("PASS: Multi-Tab Lifecycle", flush=True)


def test_session_role_isolation(client):
    print("TEST: Session Role & Cookie Isolation...", flush=True)
    client.tool("browser_session", {"action": "reset"})

    # Role 1: Admin session
    client.tool("browser_open", {
        "url": "http://127.0.0.1:18888/edge-cases.html",
        "headless": True,
        "session": "admin-session"
    })
    client.tool("browser_storage", {
        "action": "set_cookie",
        "name": "ROLE_AUTH",
        "value": "super_admin_jwt_999",
        "domain": "127.0.0.1"
    })
    admin_cookies = client.tool("browser_storage", {"action": "get_cookies"})
    assert "super_admin_jwt_999" in admin_cookies, f"Admin cookie not set: {admin_cookies}"

    # Role 2: Guest session (must be completely isolated!)
    client.tool("browser_open", {
        "url": "http://127.0.0.1:18888/edge-cases.html",
        "headless": True,
        "session": "guest-session"
    })
    guest_cookies = client.tool("browser_storage", {"action": "get_cookies"})
    assert "super_admin_jwt_999" not in guest_cookies, f"SECURITY LEAK: Admin cookie leaked into guest session: {guest_cookies}"

    # Return to Admin session -> cookie must still be there
    client.tool("browser_open", {
        "url": "http://127.0.0.1:18888/edge-cases.html",
        "headless": True,
        "session": "admin-session"
    })
    recheck_cookies = client.tool("browser_storage", {"action": "get_cookies"})
    assert "super_admin_jwt_999" in recheck_cookies, f"Admin cookie lost across session switch: {recheck_cookies}"
    print("PASS: Session Role & Cookie Isolation", flush=True)


def test_dialog_auto_dismiss(client):
    print("TEST: JavaScript Dialog Handling...", flush=True)
    client.tool("browser_session", {"action": "reset"})
    client.tool("browser_open", {
        "url": "http://127.0.0.1:18888/edge-cases.html",
        "headless": True,
        "session": "dialog-qc"
    })

    # Trigger alert - must not hang CDP
    client.tool("browser_interact", {"action": "click", "selector": "#btn-alert"})
    time.sleep(0.5)

    # Trigger confirm - must auto accept/dismiss and not freeze
    client.tool("browser_interact", {"action": "click", "selector": "#btn-confirm"})
    time.sleep(0.5)

    logs = client.tool("browser_logs", {"filter": "dialog"})
    assert "dialog.alert" in logs or "Danger Alert" in logs or "dialog.confirm" in logs, f"Dialog events missing: {logs}"
    print("PASS: JavaScript Dialog Handling", flush=True)


def test_spa_live_dom_mutation(client):
    print("TEST: SPA Live DOM Mutation & Form Handling...", flush=True)
    client.tool("browser_session", {"action": "reset"})
    client.tool("browser_open", {
        "url": "http://127.0.0.1:18888/edge-cases.html",
        "headless": True,
        "session": "spa-qc"
    })

    # Type query that triggers JS 'input' event to dynamically create <li>
    client.tool("browser_interact", {"action": "fill", "selector": "#spa-input", "value": "XZ Utils"})
    time.sleep(0.5)

    snapshot = client.tool("browser_snapshot", {"interactive_only": False})
    assert "CVE-2024-3094 XZ Utils" in snapshot, f"Dynamic SPA search result not in snapshot: {snapshot[:400]}"

    # Complex form submit
    client.tool("browser_interact", {"action": "fill", "selector": "#bio", "value": "Bug hunting verified"})
    client.tool("browser_interact", {"action": "click", "selector": "#form-submit"})
    time.sleep(0.5)

    post_submit = client.tool("browser_snapshot", {"interactive_only": False})
    assert "Submitted: Bug hunting verified" in post_submit, f"Form submission feedback not in snapshot: {post_submit[:400]}"
    print("PASS: SPA Live DOM Mutation & Form Handling", flush=True)


def test_web_research_edge_cases(client):
    print("TEST: Web Research Boundary & Adversarial Inputs...", flush=True)

    # 1. Regex characters in literal find must NOT cause regex compilation errors
    client.tool("web_fetch", {
        "url": "https://www.openssh.com/releasenotes.html",
        "find": "[.*+?^${}()|]"
    }, expect_error=True)

    # 2. Out-of-range offset beyond 1MB ceiling must be rejected to prevent DOS
    client.tool("web_fetch", {
        "url": "https://www.openssh.com/releasenotes.html",
        "offset": 99999999
    }, expect_error=True)

    # 3. Offset exceeding document length must be cleanly rejected
    client.tool("web_fetch", {
        "url": "https://www.openssh.com/releasenotes.html",
        "offset": 600000
    }, expect_error=True)

    # 4. Tail pagination reading the end of document cleanly returns next_offset=0 (omitted or 0)
    fetch_tail = json.loads(client.tool("web_fetch", {
        "url": "https://www.openssh.com/releasenotes.html",
        "offset": 549000
    }))
    assert fetch_tail.get("next_offset", 0) == 0, f"Expected next_offset=0 for tail pagination: {fetch_tail}"

    # 3. Search query with complex quotes, boolean operators, and XSS payload
    search_res = json.loads(client.tool("web_search", {
        "query": "\"OpenSSH\" AND 'sshd' CVE-2024-6387 <script>alert(1)</script>",
        "max_results": 2
    }))
    assert "sources" in search_res, f"Search failed on complex characters: {search_res}"
    print("PASS: Web Research Boundary & Adversarial Inputs", flush=True)


def main():
    assert os.geteuid() == 1000 and pathlib.Path("/.dockerenv").is_file(), "Must run in Docker sandbox as pentester"
    client = MCP()
    try:
        test_multi_tab_workflow(client)
        test_session_role_isolation(client)
        test_dialog_auto_dismiss(client)
        test_spa_live_dom_mutation(client)
        test_web_research_edge_cases(client)
        print(json.dumps({
            "status": "passed",
            "suite": "edge_cases_qc",
            "passed_tests": [
                "multi_tab_lifecycle",
                "session_role_isolation",
                "dialog_auto_dismiss",
                "spa_live_dom_mutation",
                "web_research_boundary_and_adversarial"
            ]
        }))
    finally:
        client.close()


if __name__ == "__main__":
    main()
