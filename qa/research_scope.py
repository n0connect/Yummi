"""Read-only operator scope/DNS diagnostic. Never creates rules or changes mode."""
import argparse
import ipaddress
import json
import os
import pathlib
import subprocess
import urllib.request

API = os.environ.get('YUMMI_QC_API', 'http://127.0.0.1:9080/api/v1')
HOSTS = ('openrouter.ai', 'www.openssh.com', 'www.qualys.com', 'blog.qualys.com')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--operator-context', action='store_true', required=True)
    args = parser.parse_args()
    if pathlib.Path('/.dockerenv').exists():
        parser.error('Run this diagnostic on the operator host; DNS runs only in actual sandbox via Docker')
    def get(path):
        with urllib.request.urlopen(API + path, timeout=15) as response:
            return json.load(response)
    report = {'mode': get('/gateway/egress-mode'), 'rules': get('/gateway/rules'), 'hosts': []}
    for host in HOSTS:
        script = "import json,socket,sys; print(json.dumps(sorted({r[4][0] for r in socket.getaddrinfo(sys.argv[1],443,type=socket.SOCK_STREAM)})))"
        process = subprocess.run(['docker', 'exec', '--user', '1000:1000',
                                  os.environ.get('YUMMI_SANDBOX_CONTAINER', 'yummi-sandbox'),
                                  'python3', '-c', script, host], capture_output=True, text=True, timeout=30)
        if process.returncode:
            report['hosts'].append({'host': host, 'status': 'unresolved', 'error': process.stderr[-1500:]})
            continue
        ips = json.loads(process.stdout)
        report['hosts'].append({'host': host, 'ips': ips,
                                'all_public': all(ipaddress.ip_address(ip).is_global for ip in ips)})
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
