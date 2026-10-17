"""Real agent pause and UI; explicit disposable operator UI QA context only."""
import json
import argparse
import pathlib
import time

from browser_research_qc import MCP, api, assert_qc_model
from operator_context import UI_URL, require_operator_ui
from qc_authorization import pending_failure, pending_identity


def main():
    require_operator_ui()
    assert_qc_model()
    parser = argparse.ArgumentParser()
    parser.add_argument('--budget', action='store_true')
    args = parser.parse_args()
    session = api('/sessions', {'name': 'Operator action dock QC'})
    sid = session['id']
    task = api('/tasks', {'session_id': sid,
                         'goal': 'Only call browser_session action=status once. Do not execute any other tool, command or navigation. Then report the actual session status and finish.'})
    tid = task.get('task', task)['id']
    if args.budget:
        assert task.get('task', task).get('step_budget') == 1, 'Budget QC requires an actually configured one-step runtime; task creation does not accept step_budget'
    client = MCP()
    try:
        api('/tasks/' + tid + '/start', {})
        deadline = time.monotonic() + 180
        while time.monotonic() < deadline:
            task = api('/tasks/' + tid)
            if args.budget and task['status'] == 'awaiting_approval':
                prior = api('/sessions/' + sid + '/executions?limit=100')
                prior = prior.get('executions', []) if isinstance(prior, dict) else prior
                refusal = pending_failure('operator-status', task, prior, tid, sid)
                assert not refusal, refusal
                fresh = api('/tasks/' + tid)
                fresh = fresh.get('task', fresh)
                refusal = pending_failure('operator-status', fresh, prior, tid, sid, pending_identity(task))
                assert not refusal, refusal
                task = fresh
                api('/tasks/' + tid + '/approve', {'proposal_id': task['pending_proposal_id'], 'approved': True,
                    'reason': 'Exact structured read-only session status QC to reach the real step-budget pause.'})
                time.sleep(1)
                continue
            if task['status'] in ('paused', 'awaiting_approval'):
                break
            assert task['status'] not in ('completed', 'failed', 'stopped'), task
            time.sleep(1)
        assert task['status'] in ('paused', 'awaiting_approval'), task
        if args.budget:
            assert task['status'] == 'paused' and task.get('question', '').startswith('Step budget exhausted'), task
        client.tool('browser_open', {'url': UI_URL, 'headless': True})
        client.tool('browser_eval', {'script': 'sessionStorage.setItem("yummi:chat:sessionId", ' + json.dumps(sid) + '); setTimeout(() => location.reload(), 100); true'})
        client.tool('browser_interact', {'action': 'wait', 'duration': 2})
        for width, height in [(1280, 800), (375, 812)]:
            client.tool('browser_interact', {'action': 'set_viewport', 'width': width, 'height': height})
            result = json.loads(client.tool('browser_eval', {'script': '''(() => {
              const dock=document.querySelector('.operator-actions-dock');
              if(!dock) return {visible:false};
              document.querySelector('.chat-messages').scrollTop=0;
              const r=dock.getBoundingClientRect();
              const input=document.querySelector('.chat-input-area').getBoundingClientRect();
              return {visible:r.top>=0 && r.bottom<=innerHeight && r.bottom<=input.top+2,
                outsideHistory:!dock.closest('.chat-messages'),
                repeatsGoal:Boolean(dock.querySelector('.tt-goal')),
                compact:r.height<260,
                buttonsVisible:Array.from(dock.querySelectorAll('button')).every(b => b.getBoundingClientRect().bottom<=r.bottom+2),
                actions:dock.querySelectorAll('button').length,
                duplicate:document.querySelectorAll('.chat-messages .tt-gate,.chat-messages .tt-resume').length};
            })()''' }))
            assert result.get('visible') and result['outsideHistory'] and result['actions'] and not result['duplicate'] and not result['repeatsGoal'] and result['compact'] and result['buttonsVisible'], result
            client.tool('browser_screenshot', {'output_file': '/home/pentester/work/operator-actions-' + str(width) + '.png'})
        if task['status'] == 'paused' and task.get('question', '').startswith('Step budget exhausted'):
            client.tool('browser_interact', {'action': 'click', 'selector': '.operator-actions-dock .tt-btn-budget'})
        else:
            client.tool('browser_interact', {'action': 'click', 'selector': '.operator-actions-dock .tt-btn-deny'})
        time.sleep(1)
        actual = api('/tasks/' + tid)
        assert actual['status'] != task['status'] or actual.get('pending_proposal_id') != task.get('pending_proposal_id') or actual['step_budget'] > task['step_budget'], actual
        report = {'status': 'passed', 'task_id': tid, 'session_id': sid, 'checks': ['real pending task', 'visible at history top desktop/mobile', 'no duplicate history controls', 'real operator decision acknowledged']}
        pathlib.Path('/home/pentester/work/operator-actions-qc.json').write_text(json.dumps(report, indent=2))
        print(json.dumps(report), flush=True)
    finally:
        api('/tasks/' + tid + '/stop', {})
        client.close()


if __name__ == '__main__':
    main()
