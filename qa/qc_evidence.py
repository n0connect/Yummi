"""Strict, read-only checks over real persisted QC task/execution evidence.

This checks consistency of trusted API records, not authenticity of a report supplied
by an untrusted party. Historical screenshots without an original task-completion artifact observation remain
unverified; an audit never creates a missing manifest retroactively.
"""
import json
import pathlib
import os
import subprocess
import re
import urllib.parse

REQUIRED = {
    'cited-search-original': {'web_search', 'web_fetch'},
    'deep-multi-source': {'web_search', 'web_fetch'},
    'browser-headless': {'browser_session', 'browser_open', 'browser_snapshot', 'browser_interact', 'browser_screenshot'},
    'browser-full': {'browser_session', 'browser_open', 'browser_snapshot', 'browser_interact', 'browser_screenshot'},
}
from qc_png import inspect_png_file

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
        # Send the same reviewed stdlib decoder over stdin to the installed
        # sandbox Python. No source HTTP parsing or browser runs on this host.
        validator = pathlib.Path(__file__).with_name('qc_png.py').read_text()
        process = subprocess.run(['docker', 'exec', '-i', '--user', '1000:1000',
                                  os.environ.get('YUMMI_SANDBOX_CONTAINER', 'yummi-sandbox'),
                                  'python3', '-', str(path)], input=validator,
                                 capture_output=True, text=True, timeout=30, check=True)
        info = json.loads(process.stdout)
    if not record['created_at'] <= info['mtime_ns'] / 1e9 < record['updated_at'] + 1:
        raise ValueError('Screenshot mtime is outside its execution interval')
    return dict(task_id=task['id'], session_id=task['session_id'], execution_id=record['id'],
                path=str(path), **info)


def local_artifact_info(path):
    return inspect_png_file(path)


def claim_clauses(text):
    """Bounded English/Turkish release-label heuristic, not semantic proof.

    Recognizes explicit labels/clauses; uncertain prose fails review. Decimal
    points are retained and a standalone heading labels only its next clause.
    """
    text = re.sub(r'[*#`]', '', text)
    heading = ''
    for fragment in re.split(r'\n+|;|(?<!\d)\.(?=\s|$)', text):
        fragment = fragment.strip(' -\t')
        if not fragment:
            continue
        if fragment.endswith(':') and not re.search(r'\b\d+\.\d+', fragment):
            heading = fragment
            continue
        clause = (heading + ' ' + fragment).strip()
        heading = ''
        # Keep different release roles separate even in one sentence.
        markers = list(re.finditer(r'\b(?:affected|affects?|vulnerable|etkilenen|fixed|fix(?:ed)?\s+release|patch(?:ed)?|upgrade(?:d)?|düzelt\w*)\b', clause, re.I))
        for index, marker in enumerate(markers):
            end = markers[index + 1].start() if index + 1 < len(markers) else len(clause)
            prefix = clause[max(0, marker.start() - 30):marker.start()]
            if re.search(r'\b(?:not|never|isn.t|wasn.t|değil)\s+(?:yet\s+)?$', prefix, re.I):
                continue
            yield marker.group().lower(), clause[marker.end():end]


def release_claim_failures(summary):
    affected = fixed = False
    for role, claim in claim_clauses(summary):
        versions = set(re.findall(r'(?<![\d.])(8\.5|9\.7|9\.8)(?:p1)?(?![0-9A-Za-z]|\.\d)', claim))
        if role.startswith(('affected', 'affect', 'vulnerable', 'etkilenen')):
            affected |= versions == {'8.5', '9.7'} and bool(re.search(r'8\.5(?:p1)?[^;\n]{0,60}(?:through|to|and|[-–—]|ile)[^;\n]{0,30}9\.7(?:p1)?', claim, re.I))
        elif role.startswith(('fix', 'patch', 'upgrade', 'düzelt')):
            fixed |= versions == {'9.8'}
    return [] if affected and fixed else ['Summary release clauses must associate affected 8.5–9.7 and fixed 9.8; uncertain prose requires review']


def fetched_release_failures(fetches):
    excerpts = [excerpt for _, _, excerpt in fetches]
    affected = any(re.search(r'8\.5p1[^;\n]{0,60}(?:through|to|and|[-–—])[^;\n]{0,30}9\.7p1', excerpt, re.I) for excerpt in excerpts)
    fixed = any((re.search(r'OpenSSH\s+9\.8(?:p1)?\s+was released', excerpt, re.I)
                 and re.search(r'This release contains fixes', excerpt, re.I))
                or re.search(r'\b(?:fixed|patched|upgraded)\s+(?:release\s+)?(?:in|to|by|with)\s+(?:OpenSSH\s+)?9\.8(?:p1)?\b', excerpt, re.I)
                for excerpt in excerpts)
    return [] if affected and fixed else ['Fetched excerpts lack explicit affected-range/fixed-release evidence; fetch the original release notes']


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
        failures.extend(release_claim_failures(summary))
        fetches = []
        citations = set()
        for citation in re.findall(r'https://[^\s<>"\[\]()]+', summary):
            citations.add(citation)
            # Prose commonly follows an exact URL with a colon or sentence
            # punctuation. Preserve the literal URL too; never normalize its
            # scheme, host, query contents or non-punctuation path suffix.
            if '?' not in citation and '#' not in citation:
                citations.add(citation.rstrip('.,;:!'))
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
        failures.extend(fetched_release_failures(fetches))
        return failures

    if any(r.get('mcp_tool') == 'browser_eval' or r.get('mcp_tool') not in REQUIRED[name] for r in bound):
        failures.append('Unexpected tool/DOM evaluation invalidates browser workflow evidence')
    expected = name == 'browser-headless'
    dimensions = (375, 812) if expected else (1280, 800)
    opens = [r for r in completed if r.get('mcp_tool') == 'browser_open'
             and args(r).get('url') == APP and args(r).get('headless') is expected
             and 'HTTP status: 200' in r.get('output', '')]
    if len([r for r in bound if r.get('mcp_tool') == 'browser_open']) != 1:
        failures.append('Browser workflow requires exactly one navigation')
    if any(r.get('mcp_tool') == 'browser_session' and args(r).get('action') not in {'reset', 'status'} for r in bound):
        failures.append('Unexpected browser session mutation invalidates controlled workflow')
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
        failures.append('Task-completion artifact hash/task/execution observation missing; legacy PNG is unverified')
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
