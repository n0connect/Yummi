"""Real toggle API/UI and actual sandbox state; explicit operator QA only."""
import json
import urllib.error

from browser_research_qc import MCP, api
from operator_context import UI_URL, require_operator_ui, sandbox_python

STATE = """import pathlib
p=pathlib.Path('/tmp/yummi_web_search_state')
b=pathlib.Path('/tmp/yummi_web_search_state.qc-backup')
"""


def main():
    require_operator_ui()
    initial = api('/system/web-search')['enabled']
    original = json.loads(sandbox_python(STATE + "import base64,json; print(json.dumps(base64.b64encode(p.read_bytes()).decode() if p.exists() else None))"))
    client = MCP()
    fault_active = False
    try:
        client.tool('browser_open', {'url': UI_URL, 'headless': True})
        client.tool('browser_interact', {'action': 'wait_for_selector', 'selector': '.btn-web-search', 'duration': 10})
        client.tool('browser_interact', {'action': 'wait', 'duration': 1})
        before = client.tool('browser_eval', {'script': "document.querySelector('.btn-web-search').getAttribute('aria-pressed')"})
        # Fault the actual sandbox file through the existing operator execution
        # API, never a substitute file in this disposable UI QA container.
        sandbox_python(STATE + "assert not b.exists() and not p.is_dir(); p.rename(b) if p.exists() else None; p.mkdir()")
        fault_active = True
        try:
            try:
                api('/system/web-search', {'enabled': True})
                raise AssertionError('State write failed but real API reported success')
            except urllib.error.HTTPError as error:
                assert error.code == 500
            client.tool('browser_interact', {'action': 'click', 'selector': '.btn-web-search'})
            client.tool('browser_interact', {'action': 'wait', 'duration': 1})
            after = client.tool('browser_eval', {'script': "document.querySelector('.btn-web-search').getAttribute('aria-pressed')"})
            assert before == after, 'UI falsely acknowledged rejected toggle'
        finally:
            sandbox_python(STATE + "p.rmdir(); b.rename(p) if b.exists() else None")
            fault_active = False
        api('/system/web-search', {'enabled': False})
        assert api('/system/web-search')['enabled'] is False
        # Exercise the real installed MCP inside the engagement sandbox. The
        # operator QA MCP is browser-only and has no research provider config.
        disabled = json.loads(sandbox_python('''import json,subprocess,select,time
p=subprocess.Popen(['/usr/local/bin/yummi-sandbox-mcp'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True)
def call(i,method,params):
 p.stdin.write(json.dumps(dict(jsonrpc='2.0',id=i,method=method,params=params))+'\\n'); p.stdin.flush()
 deadline=time.monotonic()+20
 while time.monotonic()<deadline:
  if not select.select([p.stdout],[],[],1)[0]: continue
  line=p.stdout.readline()
  if not line: raise RuntimeError('Actual sandbox MCP transport closed')
  value=json.loads(line)
  if value.get('id')==i: return value
 raise TimeoutError('Actual sandbox MCP response timed out')
try:
 call(1,'initialize',dict(protocolVersion='2025-03-26',capabilities={},clientInfo=dict(name='operator-toggle-qc',version='1')))
 p.stdin.write(json.dumps(dict(jsonrpc='2.0',method='notifications/initialized'))+'\\n');p.stdin.flush()
 print(json.dumps(call(2,'tools/call',dict(name='web_fetch',arguments=dict(url='https://www.openssh.com')))))
finally:
 p.stdin.close()
 try: p.wait(timeout=5)
 except subprocess.TimeoutExpired: p.terminate();p.wait(timeout=5)
'''))
        result = disabled.get('result', {})
        text = '\n'.join(c.get('text', '') for c in result.get('content', []) if c.get('type') == 'text')
        assert result.get('isError') is True and 'disabled' in text.lower(), text
        print(json.dumps({'status': 'passed', 'checks': ['actual sandbox write failure returns HTTP500', 'UI preserves acknowledged state on HTTP500', 'actual sandbox disabled MCP fetch fails closed']}))
    finally:
        try:
            if fault_active:
                sandbox_python(STATE + "p.rmdir() if p.is_dir() else None; b.rename(p) if b.exists() else None")
        finally:
            try:
                api('/system/web-search', {'enabled': initial})
            finally:
                # Preserve absence and exact bytes even when API restoration or
                # an earlier fault-cleanup step fails. Surface any failure.
                restore = 'p.unlink(missing_ok=True)' if original is None else 'import base64; p.write_bytes(base64.b64decode(' + repr(original) + '))'
                try:
                    sandbox_python(STATE + restore)
                finally:
                    client.close()


if __name__ == '__main__':
    main()
