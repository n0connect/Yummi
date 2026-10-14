"""Run inside yummi-sandbox only. Real MCP, provider, API and agent, no doubles."""
import argparse
import datetime
import json
import os
import pathlib
import select
import struct
import subprocess
import time
import urllib.request
import urllib.parse

BASE = "http://172.20.0.3:9080/api/v1"


def displayed_tool(command):
    parts = command.split()
    if not parts:
        return ""
    if parts[0] != "browser" or len(parts) < 2:
        return parts[0]
    if parts[1] in {"open", "snapshot", "screenshot", "eval", "storage", "tabs", "logs"}:
        return "browser_" + parts[1]
    if parts[1] in {"fill", "click", "press", "set_viewport", "wait", "type", "scroll"}:
        return "browser_interact"
    return ""


def verify_case(name, task, records):
    failures = []
    completed = [r for r in records if r.get("status") == "completed"]
    reports = []
    for record in completed:
        try:
            value = json.loads(record.get("output", ""))
            if isinstance(value, dict):
                reports.append(value)
        except (ValueError, TypeError):
            pass
    if name in {"cited-search-original", "deep-multi-source"}:
        for kind in ("web_search", "web_fetch"):
            if not any(r.get("kind") == kind and r.get("sources") for r in reports):
                failures.append("Missing actual cited " + kind + " evidence")
        search_args = []
        for record in completed:
            if record.get("mcp_tool") == "web_search":
                args = record.get("mcp_args", {})
                search_args.append(json.loads(args) if isinstance(args, str) else args)
        if not any(a.get("max_results") == 3 and "openssh.com" in a.get("include_domains", []) for a in search_args):
            failures.append("Requested real search source/result filters not observed")
        summary = task.get("summary", "")
        if name == "cited-search-original" and not all(term in summary for term in ["8.5", "9.7", "9.8", "https://"]):
            failures.append("Verified vendor versions and citation missing from final summary")
        if name == "deep-multi-source":
            if not any(a.get("depth") == "deep" for a in search_args):
                failures.append("No actual deep search call")
            hosts = {urllib.parse.urlparse(s["source_url"]).hostname for r in reports for s in r.get("sources", [])}
            if not any(h and (h.endswith("openssh.com") or h.endswith("openssh.org")) for h in hosts) or not any(h and h.endswith("qualys.com") for h in hosts):
                failures.append("Independent OpenSSH/Qualys sources not observed")
            if "9.8" in summary and not any(word in summary.lower() for word in ["fix", "patch", "düzelt"]):
                failures.append("Fixed release appears without a fix/patch distinction; review against original vendor evidence")
    else:
        actions = []
        for record in completed:
            if record.get('mcp_tool') == 'browser_interact':
                value = record.get('mcp_args', {})
                actions.append(json.loads(value) if isinstance(value, str) else value)
        if not any(a.get('action') == 'click' and a.get('selector') in ('#submit', '@e2') for a in actions):
            failures.append('Actual form submission click missing; input values or mutated DOM are not proof')
        modes = [r for r in reports if r.get("running") and r.get("environment") == "sandbox"]
        expected = name == "browser-headless"
        if not modes or modes[-1].get("headless") is not expected:
            failures.append("Actual sandbox browser mode not confirmed")
        filename = "quality-mobile.png" if expected else "quality-desktop.png"
        path = pathlib.Path("/home/pentester/work") / filename
        if not path.is_file() or path.stat().st_mtime < task["created_at"] or path.read_bytes()[:8] != b"\x89PNG\r\n\x1a\n":
            failures.append("Real PNG artifact missing")
        elif expected and struct.unpack(">II", path.read_bytes()[16:24]) != (375, 812):
            failures.append("Actual mobile PNG dimensions differ from 375x812")
        if not any("Research: OpenSSH" in r.get("output", "") for r in completed):
            failures.append("Observed local form result missing from browser evidence")
    return failures


def api(path, body=None):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(BASE + path, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=180) as response:
        return json.load(response)


def assert_qc_model():
    status = api('/system/status')
    conductor = next((module for module in status.get('modules', []) if module.get('name') == 'conductor'), {})
    metadata = conductor.get('meta', {})
    assert metadata.get('backend') == 'cloud' and metadata.get('model') == 'minimax/minimax-m2.1', 'QC requires actual OpenRouter/MiniMax runtime; start through make start. Current model: ' + str(metadata.get('model'))


class MCP:
    def __init__(self):
        self.process = subprocess.Popen(["/usr/local/bin/yummi-sandbox-mcp"], stdin=subprocess.PIPE,
                                        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, bufsize=1)
        self.sequence = 0
        self.call("initialize", {"protocolVersion": "2025-03-26", "capabilities": {},
                                "clientInfo": {"name": "yummi-real-qc", "version": "1"}})
        self.process.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n")
        self.process.stdin.flush()

    def call(self, method, params):
        self.sequence += 1
        self.process.stdin.write(json.dumps({"jsonrpc": "2.0", "id": self.sequence,
                                            "method": method, "params": params}) + "\n")
        self.process.stdin.flush()
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            if not select.select([self.process.stdout], [], [], 1)[0]:
                continue
            line = self.process.stdout.readline()
            if not line:
                raise RuntimeError("MCP transport closed")
            message = json.loads(line)
            if message.get("id") == self.sequence:
                if "error" in message:
                    raise RuntimeError("MCP protocol error: " + str(message["error"].get("code")))
                return message["result"]
        raise TimeoutError("MCP response timeout")

    def tool(self, name, arguments, expect_error=False):
        result = self.call("tools/call", {"name": name, "arguments": arguments})
        text = "\n".join(c.get("text", "") for c in result.get("content", []) if c.get("type") == "text")
        assert bool(result.get("isError", False)) == expect_error, (name, text[:600])
        return text

    def close(self):
        self.process.stdin.close()
        try:
            self.process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.process.terminate()
            self.process.wait(timeout=5)


def direct_checks():
    client = MCP()
    try:
        names = {t["name"] for t in client.call("tools/list", {})["tools"]}
        assert {"web_search", "web_fetch", "browser_session"}.issubset(names)
        client.tool("web_fetch", {"url": "https://127.0.0.1"}, True)
        client.tool("web_fetch", {"url": "https://169.254.169.254"}, True)
        client.tool("web_search", {"query": "OpenSSH release", "max_results": 99}, True)
        search = json.loads(client.tool("web_search", {"query": "OpenSSH CVE-2024-6387 fixed release vendor advisory", "max_results": 3,
                                                       "include_domains": ["openssh.com"]}))
        assert search["sources"] and all("openssh.com" in s["source_url"] for s in search["sources"])
        source = json.loads(client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "max_chars": 1000}))
        assert source["next_offset"] > 0 and source["sources"][0]["sha256"]
        continuation = json.loads(client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "offset": source["next_offset"], "max_chars": 1000}))
        assert continuation["sources"][0]["excerpt"] != source["sources"][0]["excerpt"]
        found = json.loads(client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "find": "OpenSSH"}))
        assert "OpenSSH" in found["sources"][0]["excerpt"]
        client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "find": "YUMMI_NO_SUCH_SOURCE_PASSAGE"}, True)
        navigation = client.tool("browser_open", {"url": "https://www.openssh.com", "headless": True, "session": "public-source"})
        assert "HTTP status: 200" in navigation, navigation[:300]
        article = client.tool("browser_snapshot", {"interactive_only": False})
        assert "OpenSSH" in article
        client.tool("browser_session", {"action": "reset"})
        return {"status": "passed", "checks": ["real MCP registration", "private destination rejection", "metadata IP rejection", "query limits",
                "real OpenRouter/Exa citations", "original HTTPS source", "pagination", "literal source find", "not-found reporting", "real public browser navigation"],
                "sources": [{"url": s["source_url"], "title": s["title"]} for s in search["sources"]]}
    finally:
        client.close()


def agent_case(name, goal, required):
    assert_qc_model()
    session = api("/sessions", {"name": "Browser Search QC - " + name})
    sid = session.get("id") or session.get("session_id")
    task = api("/tasks", {"session_id": sid, "goal": goal})
    task = task.get("task", task)
    tid = task["id"]
    api("/tasks/" + tid + "/start", {})
    deadline = time.monotonic() + 600
    approvals = []
    rejected = []
    allowed = set(required)
    if name.startswith("browser-"):
        allowed.update({"browser_eval", "browser_tabs", "browser_logs"})
    while time.monotonic() < deadline:
        task = api("/tasks/" + tid)
        task = task.get("task", task)
        if task["status"] == "awaiting_approval":
            command = task.get("pending_command", "")
            tool = displayed_tool(command)
            # Operator authorized these real QC scenarios, not arbitrary shell
            # commands or a change to the system-wide approval/egress mode.
            if tool not in allowed:
                rejected.append({"command": command, "risk": task.get("pending_risk")})
                api("/tasks/" + tid + "/stop", {})
                task = api("/tasks/" + tid)
                break
            api("/tasks/" + tid + "/approve", {
                "proposal_id": task["pending_proposal_id"], "approved": True,
                "reason": "Operator-authorized browser/research QC; expected typed tool only."})
            approvals.append({"command": command, "risk": task.get("pending_risk")})
            time.sleep(1)
            continue
        if task["status"] not in ["running", "planning", "planned", "pending", "queued"]:
            break
        time.sleep(2)
    else:
        api("/tasks/" + tid + "/stop", {})
        task = api("/tasks/" + tid)
    records = api("/sessions/" + sid + "/executions?limit=100")
    if isinstance(records, dict):
        records = records.get("executions", [])
    completed = [r for r in records if r.get("status") == "completed"]
    commands = [r.get("command", "") for r in completed]
    missing = [tool for tool in required if not any(r.get("mcp_tool") == tool for r in completed)]
    evidence_failures = verify_case(name, task, records)
    status = "passed" if task["status"] == "completed" and not missing and not evidence_failures else "failed"
    return {"name": name, "status": status, "session_id": sid, "task_id": tid,
            "task_status": task["status"], "steps": task.get("steps_used"), "missing_tools": missing,
            "approved_qc_calls": approvals,
            "rejected_qc_calls": rejected,
            "evidence_failures": evidence_failures,
            "commands": commands, "errors": [{"command": r.get("command"), "error": r.get("error")} for r in records if r.get("status") == "failed"],
            "summary": task.get("summary", "")[:8000]}


def main():
    assert pathlib.Path("/.dockerenv").is_file() and os.geteuid() == 1000, "Docker sandbox UID1000 required"
    parser = argparse.ArgumentParser()
    parser.add_argument("--direct-only", action="store_true")
    parser.add_argument("--agents-only", action="store_true")
    parser.add_argument("--case")
    parser.add_argument("--audit-report", help="Recheck persisted real task/argument/artifact evidence; no new model calls")
    parser.add_argument("--output", default="/home/pentester/work/browser-research-qc.json")
    args = parser.parse_args()
    report = {"at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "backend": api("/system/status"), "results": []}
    try:
        if args.audit_report:
            report = json.loads(pathlib.Path(args.audit_report).read_text())
            for result in report["results"]:
                task = api("/tasks/" + result["task_id"])
                records = api("/sessions/" + result["session_id"] + "/executions?limit=100")
                records = records.get("executions", []) if isinstance(records, dict) else records
                result["evidence_failures"] = verify_case(result["name"], task, records)
                result["status"] = "passed" if task["status"] == "completed" and not result["missing_tools"] and not result["evidence_failures"] else "failed"
            if any(result["status"] != "passed" for result in report["results"]):
                raise SystemExit(1)
            return
        if not args.agents_only:
            report["results"].append(direct_checks())
        if not args.direct_only:
            cases = [
                ("cited-search-original", "Quality check: make ONE web_search call for CVE-2024-6387 with include_domains=[openssh.com] and max_results=3. Then use web_fetch on https://www.openssh.com/security.html with find='July 1, 2024' and max_chars=3000; if a release citation is needed, fetch https://www.openssh.com/txt/release-9.8 with max_chars=3000. Report affected versions, fixed release and exact original source URLs, distinguishing vendor evidence from inference. No scanning, shell commands or further search calls. Finish after these sources are verified.", ["web_search", "web_fetch"]),
                ("deep-multi-source", "Quality check: make ONE web_search call with depth=deep max_results=3 include_domains=[openssh.com,qualys.com] for CVE-2024-6387. Then web_fetch https://www.qualys.com/2024/07/01/cve-2024-6387/regresshion.txt with max_chars=6000 and https://www.openssh.com/security.html with find='July 1, 2024' max_chars=3000. Give a short cited comparison of these independent sources, then finish. No additional search, shell commands or scanning.", ["web_search", "web_fetch"]),
                ("browser-headless", "Execute this browser API quality check in order, one action after seeing the preceding result. Only authorized app: http://127.0.0.1:18888/browser-qa.html. 1. browser_session action=reset. 2. browser_open url=http://127.0.0.1:18888/browser-qa.html headless=true session=quality. 3. browser_snapshot. 4. browser_interact action=fill selector=#query value=OpenSSH. 5. browser_interact action=click selector=#submit. 6. browser_snapshot interactive_only=false; observe Research: OpenSSH. 7. browser_interact action=set_viewport width=375 height=812. 8. browser_screenshot output_file=/home/pentester/work/quality-mobile.png full_page=false. 9. browser_session action=status. Then finish alone with actual mode, observed form result and PNG dimensions. Do not substitute shell commands, extra navigation or extra research for any listed API call.", ["browser_session", "browser_open", "browser_snapshot", "browser_interact", "browser_screenshot"]),
                ("browser-full", "Execute this browser API quality check in order, one action after seeing the preceding result. Only authorized app: http://127.0.0.1:18888/browser-qa.html. 1. browser_session action=reset. 2. browser_open url=http://127.0.0.1:18888/browser-qa.html headless=false session=desktop-quality. 3. browser_snapshot. 4. browser_interact action=fill selector=#query value=OpenSSH. 5. browser_interact action=click selector=#submit. 6. browser_snapshot interactive_only=false; observe Research: OpenSSH. 7. browser_session action=status. 8. browser_screenshot output_file=/home/pentester/work/quality-desktop.png full_page=false. Then finish alone with actual headless flag, form result and screenshot path. No shell commands, external navigation, scanning or other work.", ["browser_session", "browser_open", "browser_snapshot", "browser_interact", "browser_screenshot"]),
            ]
            for name, goal, tools in cases:
                if args.case and args.case != name:
                    continue
                if name.startswith('browser-'):
                    for step in range(1, 10):
                        goal = goal.replace(' ' + str(step) + '.', '\n' + str(step) + '.')
                    goal += '\nNever fabricate the expected result by modifying the DOM. Verify an actual browser_interact click on #submit and the resulting application status. Use browser_screenshot output_file directly; no shell file copying.'
                print("RUN", name, flush=True)
                result = agent_case(name, goal, tools)
                report["results"].append(result)
                print("RESULT", name, result["status"], result["task_status"], flush=True)
                pathlib.Path(args.output).parent.mkdir(parents=True, exist_ok=True)
                pathlib.Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2))
    except Exception as error:
        report["results"].append({"status": "failed", "error": str(error)[:1500]})
        raise
    finally:
        pathlib.Path(args.output).parent.mkdir(parents=True, exist_ok=True)
        pathlib.Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2))
        print(json.dumps({"output": args.output, "statuses": [r["status"] for r in report["results"]]}), flush=True)
    if any(result["status"] != "passed" for result in report["results"]):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
