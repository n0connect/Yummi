"""Real external session CRUD -> live deployed sidebar, without page reload."""
import json
import pathlib
import time
import urllib.request

from browser_research_qc import BASE, MCP, api
from operator_context import UI_URL, require_operator_ui


def main():
    require_operator_ui()
    client = MCP()
    sid = None
    try:
        client.tool('browser_open', {'url': UI_URL, 'headless': True})
        client.tool('browser_eval', {'script': 'localStorage.setItem("yummi:chat:railOpen", "1"); setTimeout(() => location.reload(), 100); true'})
        client.tool('browser_interact', {'action': 'wait', 'duration': 2})
        origin = client.tool('browser_eval', {'script': 'performance.timeOrigin'})
        name = 'Live Sidebar QC ' + str(time.time_ns())
        sid = api('/sessions', {'name': name})['id']

        def assert_name(expected, present=True):
            deadline = time.monotonic() + 6
            while time.monotonic() < deadline:
                found = json.loads(client.tool('browser_eval', {'script': 'Array.from(document.querySelectorAll(".rail-item-name")).some(b => b.innerText === ' + json.dumps(expected) + ')'}))
                if found == present:
                    assert client.tool('browser_eval', {'script': 'performance.timeOrigin'}) == origin, 'UI reloaded instead of updating live'
                    return
                time.sleep(.25)
            raise AssertionError('Live sidebar did not update: ' + expected)

        assert_name(name)
        renamed = name + ' renamed'
        api_request = urllib.request.Request(BASE + '/sessions/' + sid, json.dumps({'name': renamed}).encode(), {'Content-Type': 'application/json'}, method='PATCH')
        with urllib.request.urlopen(api_request) as response:
            assert response.status == 200
        assert_name(renamed)
        with urllib.request.urlopen(urllib.request.Request(BASE + '/sessions/' + sid, method='DELETE')) as response:
            assert response.status == 204
        assert_name(renamed, False)
        sid = None
        report = {'status': 'passed', 'checks': ['external session creation', 'external rename', 'external deletion', 'no page reload']}
        pathlib.Path('/home/pentester/work/session-live-qc.json').write_text(json.dumps(report, indent=2))
        print(json.dumps(report), flush=True)
    finally:
        if sid:
            with urllib.request.urlopen(urllib.request.Request(BASE + '/sessions/' + sid, method='DELETE')):
                pass
        client.close()


if __name__ == '__main__':
    main()
