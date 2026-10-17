"""Narrow QC approvals from authoritative pending MCP metadata, never display text."""
import json
import re
import uuid

from qc_evidence import APP, args, artifact_path, completed_records, bound_records

OPENSSH_SECURITY = 'https://www.openssh.com/security.html'
OPENSSH_RELEASE = 'https://www.openssh.com/txt/release-9.8'
QUALYS_ADVISORY = 'https://www.qualys.com/2024/07/01/cve-2024-6387/regresshion.txt'


def strict_args(encoded):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('Duplicate pending argument')
            result[key] = value
        return result
    def invalid_constant(value):
        raise ValueError('Non-JSON pending numeric constant: ' + value)
    value = json.loads(encoded, object_pairs_hook=unique, parse_constant=invalid_constant)
    if not isinstance(value, dict):
        raise ValueError('Pending MCP arguments must be an object')
    return value


def pending_identity(task):
    return tuple(task.get(key) for key in ('id', 'session_id', 'status', 'pending_approval_kind',
                                          'pending_proposal_id', 'pending_mcp_tool', 'pending_mcp_args'))


def pending_failure(name, task, records, task_id, session_id, expected_pending=None):
    if task.get('id') != task_id or task.get('session_id') != session_id or task.get('status') != 'awaiting_approval':
        return 'Pending task/session/status identity mismatch'
    if task.get('pending_approval_kind') != 'command':
        return 'Only explicit command approvals are authorized; threat or unknown holds require the operator'
    if expected_pending is not None and pending_identity(task) != expected_pending:
        return 'Pending proposal or structured authority changed before approval'
    try:
        uuid.UUID(task['pending_proposal_id'])
        tool = task['pending_mcp_tool']
        arguments = strict_args(task['pending_mcp_args'])
    except (KeyError, ValueError, TypeError, AttributeError, RecursionError):
        return 'Authoritative proposal ID and structured MCP tool/arguments are required'
    bound = bound_records(task, records)
    if len(bound) != len(records) or len({r.get('id') for r in records}) != len(records):
        return 'Prior execution identity does not match this dedicated task/session'
    prefix = []
    for record in bound:
        if record.get('status') != 'completed':
            return 'A prior QC action failed or remains unfinished; do not broaden approval to recover'
        refusal = argument_failure(name, record.get('mcp_tool'), args(record), task, prefix)
        if refusal:
            return 'Prior QC action was outside its exact authorization: ' + refusal
        prefix.append(record)
    return argument_failure(name, tool, arguments, task, prefix)


def argument_failure(name, tool, a, task, records):
    """Validate a proposed call against this case and prior actual executions."""
    if not isinstance(tool, str) or not isinstance(a, dict):
        return 'Structured tool name and argument object are required'
    done = completed_records(task, records)
    if name == 'operator-status':
        return '' if tool == 'browser_session' and a == {'action': 'status'} and not records else 'Only one actual browser_session status call is authorized'
    if name in {'cited-search-original', 'deep-multi-source'}:
        deep = name == 'deep-multi-source'
        if tool == 'web_search':
            if set(a) - {'query', 'include_domains', 'max_results', 'depth'}:
                return 'Unexpected research search arguments'
            domains = ['openssh.com', 'qualys.com'] if deep else ['openssh.com']
            query = a.get('query')
            if (not isinstance(query, str) or len(query) > 300 or 'CVE-2024-6387' not in query.upper()
                    or not isinstance(a.get('include_domains'), list) or not all(isinstance(d, str) for d in a['include_domains']) or len(a['include_domains']) != len(domains) or set(a['include_domains']) != set(domains) or type(a.get('max_results')) is not int or a['max_results'] != 3
                    or a.get('depth', 'standard') != ('deep' if deep else 'standard')):
                return 'Search differs from the authorized CVE/source/depth/result constraints'
            if any(r.get('mcp_tool') == 'web_search' for r in records):
                return 'The case authorizes only one search call, including failed calls'
            return ''
        if tool != 'web_fetch' or set(a) - {'url', 'find', 'max_chars', 'offset'}:
            return 'Only the exact original-source fetches are authorized'
        expected = {OPENSSH_SECURITY: (3000, 'July 1, 2024'), OPENSSH_RELEASE: (3000, '')}
        if deep:
            expected[QUALYS_ADVISORY] = (6000, '')
        if not isinstance(a.get('url'), str) or a['url'] not in expected:
            return 'Fetch URL is outside the exact authorized original documents'
        limit, find = expected[a['url']]
        if type(a.get('max_chars')) is not int or a['max_chars'] != limit or a.get('find', '') != find or type(a.get('offset', 0)) is not int or a.get('offset', 0) != 0:
            return 'Fetch window/find differs from the authorized original-source call'
        if not any(r.get('mcp_tool') == 'web_search' for r in done):
            return 'Original-source fetch must follow the actual successful search'
        return ''

    if name not in {'browser-headless', 'browser-full'}:
        return 'Unknown authorized QC case'
    headless = name == 'browser-headless'
    session = 'quality' if headless else 'desktop-quality'
    dimensions = (375, 812) if headless else (1280, 800)
    by_tool = lambda name: [r for r in done if r.get('mcp_tool') == name]
    interactions = by_tool('browser_interact')
    filled = [r for r in interactions if args(r).get('action') == 'fill']
    clicked = [r for r in interactions if args(r).get('action') == 'click']
    snapshots = by_tool('browser_snapshot')
    post = [r for r in snapshots if clicked and r['created_at'] > clicked[-1]['updated_at']
            and args(r).get('interactive_only') is False and re.search(r'\[status\][^\n]*"Research: OpenSSH"', r.get('output', ''))]
    if tool == 'browser_session':
        if a == {'action': 'reset'} and not records:
            return ''
        if a == {'action': 'status'} and post:
            return ''
        return 'Only initial reset and post-result status are authorized session actions'
    if tool == 'browser_open':
        expected = dict(url=APP, headless=headless, session=session)
        if a == expected and type(a.get('headless')) is bool and not by_tool('browser_open') and len(done) == 1 and args(done[0]) == {'action': 'reset'}:
            return ''
        return 'Only one authorized app navigation in the required mode/session is allowed'
    if tool == 'browser_snapshot':
        if set(a) - {'interactive_only'} or ('interactive_only' in a and type(a['interactive_only']) is not bool):
            return 'Unexpected snapshot arguments'
        if by_tool('browser_open') and not filled and not snapshots:
            return ''
        if clicked and not post and a == {'interactive_only': False}:
            return ''
        return 'Snapshot is outside the initial or immediate post-click observation'
    if tool == 'browser_screenshot':
        if (set(a) <= {'output_file', 'full_page'} and a.get('output_file') == str(artifact_path(task))
                and a.get('full_page', False) is False and post and not by_tool('browser_screenshot')
                and any(args(r).get('action') == 'set_viewport' and (args(r).get('width'), args(r).get('height')) == dimensions for r in interactions)):
            return ''
        return 'Screenshot must follow the actual result snapshot/viewport and use its unique case path'
    if tool != 'browser_interact':
        return 'Extra browser, tab, log, script or shell calls are not authorized'
    action = a.get('action')
    if action == 'set_viewport':
        if a == dict(action='set_viewport', width=dimensions[0], height=dimensions[1]) and type(a.get('width')) is int and type(a.get('height')) is int and post and not any(args(r).get('action') == action for r in interactions):
            return ''
        return 'Only the requested viewport after the result snapshot is authorized'
    if (set(a) - {'action', 'selector', 'ref', 'value'} or bool(a.get('selector')) == bool(a.get('ref'))
            or any(key in a and not isinstance(a[key], str) for key in ('selector', 'ref'))):
        return 'Interaction needs exactly one observed selector/ref and its expected arguments'
    latest = next((r for r in reversed(done) if r.get('mcp_tool') in {'browser_snapshot', 'browser_open'}), None)
    refs = {} if latest is None else dict(re.findall(r'(@e\d+)\s+\[[^\]]+\][^\n]*\bid="([^"]+)"', latest.get('output', '')))
    selector = a.get('selector') or a.get('ref')
    target = selector if selector in {'#query', '#submit'} else '#' + refs.get(selector, '')
    observed = target.lstrip('#') in refs.values()
    if action == 'fill' and target == '#query' and a.get('value') == 'OpenSSH' and observed and snapshots and not filled:
        return ''
    if action == 'click' and target == '#submit' and 'value' not in a and observed and filled and not clicked:
        return ''
    return 'Only the observed query fill followed by the resolved submit click is authorized'
