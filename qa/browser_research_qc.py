"""Direct checks run in sandbox; agent/audit control runs on the operator side.

No provider calls or browser processes run on the operator machine.
"""
import argparse
import datetime
import json
import os
import pathlib
import select
import subprocess
import time
import urllib.request

from qc_evidence import (REQUIRED, artifact_metadata, completed_records,
                         https_source, missing_tools, verify_case)

from qc_authorization import pending_failure, pending_identity

BASE = os.environ.get("YUMMI_QC_API", "http://127.0.0.1:9080/api/v1")



def api(path, body=None):
    if pathlib.Path('/.dockerenv').exists() and os.geteuid() == 1000:
        from operator_context import require_operator_ui
        require_operator_ui()
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(BASE + path, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=180) as response:
        return json.load(response)


def session_binding_failures(task):
    tasks = api('/sessions/' + task['session_id'] + '/tasks')
    if isinstance(tasks, dict):
        tasks = tasks.get('tasks', [])
    if len(tasks) != 1 or tasks[0].get('id') != task['id']:
        return ['QC requires a dedicated session containing exactly this task']
    return []


def assert_qc_model():
    status = api('/system/status')
    conductor = next((module for module in status.get('modules', []) if module.get('name') == 'conductor'), {})
    metadata = conductor.get('meta', {})
    assert metadata.get('backend') == 'cloud' and metadata.get('model') == 'minimax/minimax-m2.1', 'QC requires actual OpenRouter/MiniMax runtime; start through make start. Current model: ' + str(metadata.get('model'))


class MCP:
    def __init__(self):
        if os.environ.get('YUMMI_QC_CONTEXT') == 'operator-ui':
            from operator_context import require_operator_ui
            require_operator_ui()
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

    def tool(self, name, arguments, expect_error=False, error_contains=None):
        if os.environ.get('YUMMI_QC_CONTEXT') == 'operator-ui' and not name.startswith('browser_'):
            raise RuntimeError('Operator UI QA MCP permits browser tools only; research stays in actual sandbox')
        result = self.call("tools/call", {"name": name, "arguments": arguments})
        text = "\n".join(c.get("text", "") for c in result.get("content", []) if c.get("type") == "text")
        assert bool(result.get("isError", False)) == expect_error, (name, text[:600])
        if error_contains is not None:
            assert error_contains in text, (name, text[:600])
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
        client.tool("web_fetch", {"url": "https://127.0.0.1"}, True, "research refuses non-public addresses")
        client.tool("web_fetch", {"url": "https://169.254.169.254"}, True, "research refuses non-public addresses")
        client.tool("web_search", {"query": "OpenSSH release", "max_results": 99}, True, "max_results must be 1-10")
        search = json.loads(client.tool("web_search", {"query": "OpenSSH CVE-2024-6387 fixed release vendor advisory", "max_results": 3,
                                                       "include_domains": ["openssh.com"]}))
        assert search["sources"] and all(https_source(s["source_url"], ("openssh.com",)) for s in search["sources"])
        source = json.loads(client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "max_chars": 1000}))
        assert source["next_offset"] > 0 and source["sources"][0]["sha256"]
        continuation = json.loads(client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "offset": source["next_offset"], "max_chars": 1000}))
        assert continuation["sources"][0]["excerpt"] != source["sources"][0]["excerpt"]
        found = json.loads(client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "find": "OpenSSH"}))
        assert "OpenSSH" in found["sources"][0]["excerpt"]
        client.tool("web_fetch", {"url": "https://www.openssh.com/releasenotes.html", "find": "YUMMI_NO_SUCH_SOURCE_PASSAGE"}, True, "Literal text not found in source")
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
    # Dedicated session paths cannot be overwritten by a later QC run.
    goal = goal.replace("/home/pentester/work/quality-mobile.png", f"/home/pentester/work/qc/{sid}/screenshot.png")
    goal = goal.replace("/home/pentester/work/quality-desktop.png", f"/home/pentester/work/qc/{sid}/screenshot.png")
    subprocess.run(["docker", "exec", "--user", "1000:1000",
                    os.environ.get("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox"),
                    "mkdir", "-p", f"/home/pentester/work/qc/{sid}"], check=True, timeout=30)
    task = api("/tasks", {"session_id": sid, "goal": goal})
    task = task.get("task", task)
    tid = task["id"]
    api("/tasks/" + tid + "/start", {})
    deadline = time.monotonic() + 600
    approvals = []
    rejected = []
    while time.monotonic() < deadline:
        task = api("/tasks/" + tid)
        task = task.get("task", task)
        if task["status"] == "awaiting_approval":
            command = task.get("pending_command", "")
            prior = api('/sessions/' + sid + '/executions?limit=100')
            prior = prior.get('executions', []) if isinstance(prior, dict) else prior
            refusal = pending_failure(name, task, prior, tid, sid)
            authorized_identity = pending_identity(task)
            if not refusal:
                fresh = api('/tasks/' + tid)
                fresh = fresh.get('task', fresh)
                refusal = pending_failure(name, fresh, prior, tid, sid, authorized_identity)
                task = fresh
            if refusal:
                rejected.append({'command': command, 'reason': refusal, 'risk': task.get('pending_risk')})
                api('/tasks/' + tid + '/stop', {})
                task = api('/tasks/' + tid)
                break
            api("/tasks/" + tid + "/approve", {
                "proposal_id": task["pending_proposal_id"], "approved": True,
                "reason": "Operator-authorized QC; exact structured tool, case arguments and prior evidence verified."})
            approvals.append({"command": command, "risk": task.get("pending_risk")})
            time.sleep(1)
            continue
        if task["status"] not in ["running", "planning", "planned", "pending", "queued"]:
            break
        time.sleep(2)
    else:
        api("/tasks/" + tid + "/stop", {})
        task = api("/tasks/" + tid)
    task = task.get("task", task)
    records = api("/sessions/" + sid + "/executions?limit=100")
    if isinstance(records, dict):
        records = records.get("executions", [])
    completed = completed_records(task, records)
    commands = [r.get("command", "") for r in completed]
    missing = missing_tools(name, task, records)
    artifacts = []
    for record in completed:
        if record.get('mcp_tool') == 'browser_screenshot':
            try:
                artifacts.append(artifact_metadata(task, record))
            except (OSError, ValueError, subprocess.SubprocessError):
                pass
    evidence_failures = verify_case(name, task, records, artifacts)
    evidence_failures.extend(session_binding_failures(task))
    status = "passed" if task["status"] == "completed" and not missing and not evidence_failures else "failed"
    return {"name": name, "status": status, "session_id": sid, "task_id": tid,
            "task_status": task["status"], "steps": task.get("steps_used"), "missing_tools": missing,
            "approved_qc_calls": approvals,
            "rejected_qc_calls": rejected,
            "evidence_failures": evidence_failures, "artifacts": artifacts,
            "commands": commands, "errors": [{"command": r.get("command"), "error": r.get("error")} for r in records if r.get("status") == "failed"],
            "summary": task.get("summary", "")[:8000]}


def main():
    parser = argparse.ArgumentParser()
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument("--direct-only", action="store_true")
    modes.add_argument("--agents-only", action="store_true")
    modes.add_argument("--audit-report", help="Recheck trusted persisted task/execution/artifact evidence; no new model calls")
    parser.add_argument("--case", choices=sorted(REQUIRED))
    parser.add_argument("--output", required=True, help="New report path; existing files are never overwritten")
    args = parser.parse_args()
    sandbox = pathlib.Path('/.dockerenv').exists() and os.geteuid() == 1000
    if args.direct_only and (not sandbox or pathlib.Path('/etc/yummi-operator-ui-qc').exists()):
        parser.error('Direct MCP/provider/browser checks require sandbox Docker UID1000')
    if not args.direct_only and sandbox:
        parser.error('Agent/audit control requires the operator-side API; sandbox cannot manage tasks')
    # Claim this output path exclusively before any side effects/provider calls.
    pathlib.Path(args.output).parent.mkdir(parents=True, exist_ok=True)
    with open(args.output, 'x') as output:
        output.write('{}\n')
    report = {"at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "results": []}
    if not args.direct_only:
        report['backend'] = api('/system/status')
    try:
        if args.audit_report:
            source_report = json.loads(pathlib.Path(args.audit_report).read_text())
            report["results"] = source_report["results"]
            report["audit_source"] = str(pathlib.Path(args.audit_report).resolve())
            if not report['results']:
                raise ValueError('Audit report contains no task cases')
            identities = [(r.get('name'), r.get('task_id')) for r in report['results']]
            if len(identities) != len(set(identities)):
                raise ValueError('Duplicate audit task cases')
            for result in report["results"]:
                task = api("/tasks/" + result["task_id"])
                records = api("/sessions/" + result["session_id"] + "/executions?limit=100")
                records = records.get("executions", []) if isinstance(records, dict) else records
                task = task.get("task", task)
                if task.get("id") != result["task_id"] or task.get("session_id") != result["session_id"]:
                    raise ValueError("Audit task/session identity mismatch")
                result["missing_tools"] = missing_tools(result["name"], task, records)
                result["evidence_failures"] = verify_case(result["name"], task, records, result.get("artifacts"))
                result["evidence_failures"].extend(session_binding_failures(task))
                result["task_status"] = task["status"]
                result["summary"] = task.get("summary", "")
                result['commands'] = [r.get('command', '') for r in completed_records(task, records)]
                result['errors'] = [{'command': r.get('command'), 'error': r.get('error')} for r in records if r.get('status') == 'failed']
                result["status"] = "passed" if task["status"] == "completed" and not result["missing_tools"] and not result["evidence_failures"] else "failed"
            if any(result["status"] != "passed" for result in report["results"]):
                raise SystemExit(1)
            return
        if not args.agents_only:
            report["results"].append(direct_checks())
        if not args.direct_only:
            cases = [
                ("cited-search-original", "Quality check: make ONE web_search call for CVE-2024-6387 with include_domains=[openssh.com] and max_results=3. Then use web_fetch on https://www.openssh.com/security.html with find='July 1, 2024' and max_chars=3000; if a release citation is needed, fetch https://www.openssh.com/txt/release-9.8 with max_chars=3000. In the final summary report the affected versions, fixed release and exact original source URLs starting with https://, distinguishing vendor evidence from inference. No scanning, shell commands or further search calls. Finish after these sources are verified.", ["web_search", "web_fetch"]),
                ("deep-multi-source", "Quality check: make ONE web_search call with depth=deep max_results=3 include_domains=[openssh.com,qualys.com] for CVE-2024-6387. Then web_fetch https://www.qualys.com/2024/07/01/cve-2024-6387/regresshion.txt with max_chars=6000 and https://www.openssh.com/security.html with find='July 1, 2024' max_chars=3000, plus https://www.openssh.com/txt/release-9.8 with max_chars=3000 for the explicit fixed-release evidence. Give a short comparison citing the exact fetched original HTTPS URLs starting with https:// and clearly identifying the affected versions and the fixed release (fixed/patch/upgrade in 9.8p1), then finish. No additional search, shell commands or scanning.", ["web_search", "web_fetch"]),
                ("browser-headless", "Execute this browser API quality check in order, one action after seeing the preceding result. Only authorized app: http://127.0.0.1:18888/browser-qa.html. 1. browser_session action=reset. 2. browser_open url=http://127.0.0.1:18888/browser-qa.html headless=true session=quality. 3. browser_snapshot. 4. browser_interact action=fill selector=#query value=OpenSSH. 5. browser_interact action=click selector=#submit (first click #submit to submit the form). 6. browser_snapshot interactive_only=false (take this snapshot immediately after click to observe the resulting Research: OpenSSH). 7. browser_interact action=set_viewport width=375 height=812. 8. browser_screenshot output_file=/home/pentester/work/quality-mobile.png full_page=false. 9. browser_session action=status. Then finish alone with actual mode, observed form result and PNG dimensions. Do not substitute shell commands, extra navigation or extra research for any listed API call.", ["browser_session", "browser_open", "browser_snapshot", "browser_interact", "browser_screenshot"]),
                ("browser-full", "Execute this browser API quality check in order, one action after seeing the preceding result. Only authorized app: http://127.0.0.1:18888/browser-qa.html. 1. browser_session action=reset. 2. browser_open url=http://127.0.0.1:18888/browser-qa.html headless=false session=desktop-quality. 3. browser_snapshot. 4. browser_interact action=fill selector=#query value=OpenSSH. 5. browser_interact action=click selector=#submit (first click #submit to submit the form). 6. browser_snapshot interactive_only=false (take this snapshot immediately after click to observe the resulting Research: OpenSSH). 7. browser_interact action=set_viewport width=1280 height=800. 8. browser_session action=status. 9. browser_screenshot output_file=/home/pentester/work/quality-desktop.png full_page=false. Then finish alone with actual headless flag, form result and screenshot path. No shell commands, external navigation, scanning or other work.", ["browser_session", "browser_open", "browser_snapshot", "browser_interact", "browser_screenshot"]),
            ]
            for name, goal, tools in cases:
                if args.case and args.case != name:
                    continue
                if name.startswith('browser-'):
                    for step in range(1, 10):
                        goal = goal.replace(' ' + str(step) + '.', '\n' + str(step) + '.')
                    goal += '\nNever fabricate the expected result by modifying the DOM. You MUST perform the actual browser_interact click on #submit after filling the query; do NOT skip it. Verify the resulting application status. Use browser_screenshot output_file directly; no shell file copying.'
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
