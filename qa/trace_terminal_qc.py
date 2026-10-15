"""Real deployed UI and tmux connections, sandbox only, no doubles."""
import argparse
import json
import time

from browser_research_qc import MCP
from operator_context import UI_URL, require_operator_ui, sandbox_python


def clients():
    output = sandbox_python("import subprocess; p=subprocess.run(['tmux','list-clients','-t','operator','-F','#{client_pid}'],capture_output=True,text=True); print(p.stdout,end='')")
    return set(output.split())


def main():
    require_operator_ui()
    parser = argparse.ArgumentParser()
    parser.add_argument('--terminal', action='store_true')
    parser.add_argument('--errors', action='store_true')
    args = parser.parse_args()
    client = MCP()
    try:
        client.tool('browser_open', {'url': UI_URL + '/trace', 'headless': True})
        client.tool('browser_interact', {'action': 'wait', 'duration': 2})
        if args.errors:
            client.tool('browser_eval', {'script': 'sessionStorage.setItem("yummi:chat:sessionId", "invalid/session"); setTimeout(() => location.reload(), 100); true'})
            client.tool('browser_interact', {'action': 'wait', 'duration': 2})
            result = json.loads(client.tool('browser_eval', {'script': '({error:!!document.querySelector(".trace-load-error"),cards:document.querySelectorAll(".trace-section").length,busy:document.querySelector(".trace-page").getAttribute("aria-busy")})'}))
            assert result['error'] and result['cards'] >= 10 and result['busy'] == 'false', result
        elif args.terminal:
            before = clients()
            shell_pid = sandbox_python("import subprocess; print(subprocess.check_output(['tmux','display-message','-p','-t','operator','#{pane_pid}']).decode(),end='')").strip()
            client.tool('browser_eval', {'script': "document.querySelector('a[href=\"/terminal\"]').click(); true"})
            client.tool('browser_interact', {'action': 'wait', 'duration': 2})
            assert clients() - before, 'Bash never attached to real operator tmux'
            initial = json.loads(client.tool('browser_eval', {'script': 'document.querySelector(".workbench-terminal").getBoundingClientRect().width'}))
            client.tool('browser_interact', {'action': 'click', 'selector': '.workbench-session-strip button'})
            result = json.loads(client.tool('browser_eval', {'script': '({hidden:document.querySelector("#operator-briefing").hidden,width:document.querySelector(".workbench-terminal").getBoundingClientRect().width,label:document.querySelector(".workbench-session-context").innerText})'}))
            assert result['hidden'] and result['width'] > initial and 'operator' in result['label'], result
            client.tool('browser_eval', {'script': "document.querySelector('a[href=\"/trace\"]').click(); true"})
            client.tool('browser_interact', {'action': 'wait', 'duration': 4})
            assert not (clients() - before), 'Unmounted Bash reattached a ghost terminal client'
            assert sandbox_python("import subprocess; print(subprocess.check_output(['tmux','display-message','-p','-t','operator','#{pane_pid}']).decode(),end='')").strip() == shell_pid, 'Persistent operator shell was destroyed'
        else:
            for width, height in [(1280, 800), (375, 812)]:
                client.tool('browser_interact', {'action': 'set_viewport', 'width': width, 'height': height})
                result = json.loads(client.tool('browser_eval', {'script': '''Array.from(document.querySelectorAll('.trace-grid > .trace-section')).map(card => ({height:card.getBoundingClientRect().height, body:!!card.querySelector('.trace-section-body'), keyboard:card.querySelector('.trace-section-body')?.tabIndex===0}))'''}))
                assert result and all(250 <= c['height'] <= 420 and c['body'] and c['keyboard'] for c in result), result
                assert max(c['height'] for c in result) - min(c['height'] for c in result) < 2, result
                scroll = json.loads(client.tool('browser_eval', {'script': '''(() => {const body=Array.from(document.querySelectorAll('.trace-section-body')).find(b=>b.scrollHeight>b.clientHeight+20); if(!body)return {scrolled:false}; const card=body.closest('.trace-section'), height=card.getBoundingClientRect().height; body.scrollTop=120; return {scrolled:body.scrollTop>0,stable:Math.abs(card.getBoundingClientRect().height-height)<1};})()'''}))
                assert scroll['scrolled'] and scroll['stable'], scroll
                client.tool('browser_screenshot', {'output_file': '/home/pentester/work/trace-cards-' + str(width) + '.png'})
        print(json.dumps({'status': 'passed', 'case': 'trace-load-errors' if args.errors else 'terminal-cleanup-and-briefing' if args.terminal else 'trace-card-layout'}), flush=True)
    finally:
        client.close()


if __name__ == '__main__':
    main()
