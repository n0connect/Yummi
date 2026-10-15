"""Explicit authority boundary for disposable, operator-owned UI QA containers."""
import os
import pathlib
import shlex

UI_URL = os.environ.get('YUMMI_QC_UI_URL', 'http://127.0.0.1:80')


def require_operator_ui():
    if (os.environ.get('YUMMI_QC_CONTEXT') != 'operator-ui'
            or not pathlib.Path('/etc/yummi-operator-ui-qc').is_file()
            or not pathlib.Path('/.dockerenv').is_file() or os.geteuid() != 1000
            or UI_URL != 'http://127.0.0.1:80'
            or os.environ.get('YUMMI_QC_API') != UI_URL + '/api/v1'):
        raise RuntimeError('Run only in the explicit operator UI QA image, sharing yummi-ui network namespace; never in yummi-sandbox')


def sandbox_python(script):
    """Existing operator API runs fixed maintenance code in the real sandbox.

    The disposable QA container has no Docker socket, agent credentials or
    research configuration. The normal executor/gateway path still applies.
    """
    require_operator_ui()
    from browser_research_qc import api
    result = api('/sandbox/exec', {'command': 'python3 -c ' + shlex.quote(script)})
    if result.get('exit_code') != 0:
        raise RuntimeError('Actual sandbox maintenance failed: ' + str(result.get('stderr', ''))[:1000])
    return result.get('stdout', '')
