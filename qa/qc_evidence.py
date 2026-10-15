"""Strict, read-only checks over real persisted QC task/execution evidence.

This checks consistency of trusted API records, not authenticity of a report supplied
by an untrusted party. Historical screenshots without a capture manifest remain
unverified; an audit never creates a missing manifest retroactively.
"""
import hashlib
import json
import pathlib
import os
import subprocess
import re
import struct
import urllib.parse

REQUIRED = {
    'cited-search-original': {'web_search', 'web_fetch'},
    'deep-multi-source': {'web_search', 'web_fetch'},
    'browser-headless': {'browser_session', 'browser_open', 'browser_snapshot', 'browser_interact', 'browser_screenshot'},
    'browser-full': {'browser_session', 'browser_open', 'browser_snapshot', 'browser_interact', 'browser_screenshot'},
}
APP = 'http://127.0.0.1:18888/browser-qa.html'


def object_value(value):
    try:
        obj = json.loads(value) if isinstance(value, str) else value
        return obj if isinstance(obj, dict) else {}
    except (ValueError, TypeError):
        return {}


def args(record):
    return object_value(record.get('mcp_args', {}))


def bound_records(task, records):
    # Execution storage predates task_id. Dedicated QC sessions plus the exact
    # task lifetime are required; callers additionally reject shared sessions.
    return sorted((r for r in records if r.get('session_id') == task.get('session_id')
                   and r.get('mode') == 'agent_task'
                   and task.get('created_at', 0) <= r.get('created_at', -1)
                   <= r.get('updated_at', -1) <= task.get('updated_at', 0)),
                  key=lambda r: (r['created_at'], r['updated_at'], r.get('id', '')))


def completed_records(task, records):
    return [r for r in bound_records(task, records) if r.get('status') == 'completed']


def missing_tools(name, task, records):
    return sorted(REQUIRED[name] - {r.get('mcp_tool') for r in completed_records(task, records)})


def https_source(url, domains):
    try:
        u = urllib.parse.urlsplit(url)
        return (u.scheme == 'https' and not u.username and not u.password and u.port in (None, 443)
                and any(u.hostname == d or (u.hostname or '').endswith('.' + d) for d in domains))
    except (ValueError, TypeError):
        return False


def artifact_path(task):
    return pathlib.Path('/home/pentester/work/qc') / task['session_id'] / 'screenshot.png'


def artifact_metadata(task, record):
    path = pathlib.Path(args(record).get('output_file', ''))
    if path != artifact_path(task):
        raise ValueError('Screenshot must use its dedicated session artifact path')
    if pathlib.Path('/.dockerenv').exists() and os.geteuid() == 1000:
        info = local_artifact_info(path)
    else:
        # Operator controller reads sandbox artifact metadata through the narrow
        # UID1000 Docker path. No host research or browser execution occurs.
        script = """import hashlib,json,pathlib,struct,sys
p=pathlib.Path(sys.argv[1])
if p.is_symlink() or not p.is_file(): raise ValueError('Not a regular PNG')
b=p.read_bytes(); s=p.stat()
if len(b)<33 or b[:8]!=bytes([137,80,78,71,13,10,26,10]) or b[12:16]!=b'IHDR': raise ValueError('Invalid PNG')
w,h=struct.unpack('>II',b[16:24])
print(json.dumps(dict(sha256=hashlib.sha256(b).hexdigest(),size=len(b),width=w,height=h,mtime_ns=s.st_mtime_ns)))
"""
        process = subprocess.run(['docker', 'exec', '--user', '1000:1000',
                                  os.environ.get('YUMMI_SANDBOX_CONTAINER', 'yummi-sandbox'),
                                  'python3', '-c', script, str(path)],
                                 capture_output=True, text=True, timeout=30, check=True)
        info = json.loads(process.stdout)
    if not record['created_at'] <= info['mtime_ns'] / 1e9 < record['updated_at'] + 1:
        raise ValueError('Screenshot mtime is outside its execution interval')
    return dict(task_id=task['id'], session_id=task['session_id'], execution_id=record['id'],
                path=str(path), **info)


def local_artifact_info(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError('Not a regular PNG')
    raw = path.read_bytes()
    if len(raw) < 33 or raw[:8] != b'\x89PNG\r\n\x1a\n' or raw[12:16] != b'IHDR':
        raise ValueError('Screenshot is not a PNG with an IHDR')
    stat = path.stat()
    width, height = struct.unpack('>II', raw[16:24])
    return dict(sha256=hashlib.sha256(raw).hexdigest(), size=len(raw),
                width=width, height=height, mtime_ns=stat.st_mtime_ns)


def verify_case(name, task, records, artifacts=None):
    failures = []
    if name not in REQUIRED:
        return ['Unknown QC case']
    if not task.get('id') or not task.get('session_id'):
        return ['Task/session identity missing']
    ids = [r.get('id') for r in records]
    if any(not ident for ident in ids) or len(ids) != len(set(ids)):
        failures.append('Execution IDs missing or duplicated')
    bound = bound_records(task, records)
    completed = completed_records(task, records)
    if len(bound) != len(records):
        failures.append('Executions outside the dedicated task/session lifetime')
    if name in {'cited-search-original', 'deep-multi-source'}:
        searches = [r for r in completed if r.get('mcp_tool') == 'web_search'
                    and object_value(r.get('output')).get('kind') == 'web_search'
                    and object_value(r.get('output')).get('sources')]
        if len(searches) != 1:
            failures.append('Expected exactly one successful cited search')
        wanted = {'openssh.com'} | ({'qualys.com'} if name == 'deep-multi-source' else set())
        if not searches or not any(args(r).get('max_results') == 3
                                   and set(args(r).get('include_domains', [])) == wanted
                                   and (name != 'deep-multi-source' or args(r).get('depth') == 'deep')
                                   for r in searches):
            failures.append('Requested search filters/depth missing')
        summary = task.get('summary', '')
        if not all(v in summary for v in ('8.5', '9.7', '9.8')) or not re.search(r'affect|vulnerab|etkilenen', summary, re.I) or not re.search(r'fix|patch|upgrade|düzelt', summary, re.I):
            failures.append('Summary must distinguish affected 8.5–9.7 and fixed 9.8 releases')
        fetches = []
        citations = set(re.findall(r'https://[^\s<>"\[\]()]+', summary))
        for r in completed:
            report = object_value(r.get('output'))
            requested = args(r).get('url', '')
            if r.get('mcp_tool') != 'web_fetch' or report.get('kind') != 'web_fetch' or report.get('provider') != 'direct_https':
                continue
            for source in report.get('sources', []):
                url = source.get('source_url', '')
                if (https_source(url, ('openssh.com', 'openssh.org', 'qualys.com'))
                        and https_source(requested, ('openssh.com', 'openssh.org', 'qualys.com'))
                        and re.fullmatch(r'[0-9a-f]{64}', source.get('sha256', ''))
                        and source.get('retrieved_at') and source.get('excerpt')):
                    fetches.append((requested, url, source['excerpt']))
                    if not ({requested, url} & citations):
                        failures.append('Exact HTTPS fetched source citation missing: ' + requested)
        if not any(https_source(u, ('openssh.com', 'openssh.org')) for _, u, _ in fetches):
            failures.append('Actual original OpenSSH HTTPS fetch missing')
        if name == 'deep-multi-source' and not any(https_source(u, ('qualys.com',)) for _, u, _ in fetches):
            failures.append('Actual independent original Qualys HTTPS fetch missing')
        excerpts = ' '.join(x for _, _, x in fetches)
        if not all(v in excerpts for v in ('8.5p1', '9.7p1', '9.8')):
            failures.append('Fetched excerpts lack affected/fixed release evidence')
        return failures

    if any(r.get('mcp_tool') == 'browser_eval' or r.get('mcp_tool') not in REQUIRED[name] | {'browser_tabs', 'browser_logs'} for r in bound):
        failures.append('Unexpected tool/DOM evaluation invalidates browser workflow evidence')
    expected = name == 'browser-headless'
    dimensions = (375, 812) if expected else (1280, 800)
    opens = [r for r in completed if r.get('mcp_tool') == 'browser_open'
             and args(r).get('url') == APP and args(r).get('headless') is expected
             and 'HTTP status: 200' in r.get('output', '')]
    if len([r for r in bound if r.get('mcp_tool') == 'browser_open']) != 1:
        failures.append('Browser workflow requires exactly one navigation')
    resets = [r for r in completed if r.get('mcp_tool') == 'browser_session' and args(r).get('action') == 'reset']
    if len(resets) != 1 or not opens or resets[0]['updated_at'] >= opens[0]['created_at']:
        failures.append('Actual session reset before app navigation missing')
    if not opens:
        failures.append('Expected real app open and requested browser mode missing')
    modes = [object_value(r.get('output')) for r in completed if r.get('mcp_tool') == 'browser_session' and args(r).get('action') == 'status']
    if not any(m.get('running') is True and m.get('environment') == 'sandbox' and m.get('headless') is expected and opens and m.get('active_session') == args(opens[0]).get('session') for m in modes):
        failures.append('Actual sandbox browser mode missing')
    if any(r.get('mcp_tool') == 'browser_interact' and args(r).get('action') not in {'fill', 'click', 'set_viewport'} for r in bound):
        failures.append('Unexpected browser interaction invalidates controlled form workflow')
    for action in ('fill', 'click'):
        if len([r for r in bound if r.get('mcp_tool') == 'browser_interact' and args(r).get('action') == action]) != 1:
            failures.append('Controlled workflow requires exactly one actual ' + action)
    # Ref selectors are accepted only when resolved by a preceding actual
    # snapshot, and no subsequent navigation resets that mapping.
    refs = {}
    phase = 0
    previous_end = -1
    shots = []
    for r in completed:
        tool, a, out = r.get('mcp_tool'), args(r), r.get('output', '')
        if tool == 'browser_open':
            refs = {}
        if tool in {'browser_open', 'browser_snapshot'}:
            refs = {ref: ident for ref, ident in re.findall(r'(@e\d+)\s+\[[^\]]+\][^\n]*\bid="([^"]+)"', out)}
        selector = a.get('selector') or a.get('ref')
        target = selector if selector in {'#query', '#submit'} else '#' + refs.get(selector, '')
        if r['created_at'] <= previous_end:
            continue  # equal-second/overlapping records cannot prove ordering
        if phase == 0 and tool == 'browser_interact' and a.get('action') == 'fill' and target == '#query' and a.get('value') == 'OpenSSH' and opens and r['created_at'] > opens[0]['updated_at']:
            phase = 1
        elif phase == 1 and tool == 'browser_interact' and a.get('action') == 'click' and target == '#submit' and 'clicked ' in out:
            phase = 2
        elif phase == 2 and tool == 'browser_snapshot' and a.get('interactive_only') is False and re.search(r'\[status\][^\n]*"Research: OpenSSH"', out) and APP in out:
            phase = 3
        elif phase == 3 and tool == 'browser_screenshot':
            shots.append(r)
        else:
            continue
        previous_end = r['updated_at']
    if not shots:
        failures.append('Ordered fill → resolved submit click → result snapshot → screenshot missing')
    if len([r for r in bound if r.get('mcp_tool') == 'browser_screenshot']) != 1:
        failures.append('Exactly one screenshot execution required; later captures must not overwrite evidence')
    manifests = artifacts or []
    if not manifests:
        failures.append('Capture-time artifact hash/task/execution manifest missing; legacy PNG is unverified')
    verified = False
    for r in shots:
        try:
            current = artifact_metadata(task, r)
            if current not in manifests:
                continue
            if (current['width'], current['height']) != dimensions:
                continue
            if f"Screenshot saved to: {current['path']} ({dimensions[0]}x{dimensions[1]})" not in r.get('output', ''):
                continue
            verified = True
        except (OSError, ValueError, KeyError, subprocess.SubprocessError):
            continue
    if not verified:
        failures.append('Task-specific screenshot hash, dimensions and execution binding unverified')
    return failures
