#!/usr/bin/env python3
"""Verify the real Docker control boundary without deploying the live stack.

Prerequisites: Python 3.10+, a local Docker Unix-socket context, and current
images yummi/orchestrator:local, yummi/docker-proxy:local, yummi/ui:local and
 yummi/sandbox-mcp-test:local. Build these from the source being verified.
Run from the repository: python3 qa/control_boundary_qc.py --output REPORT.json
Optionally pass --source CHECKOUT when verifying another checkout's QA scripts.

The runner refuses overlapping 172.21/22/23.0.0/24 networks. It creates uniquely
named containers, internal control/sandbox networks, a UI-only ingress network,
and two ephemeral loopback host ports. It never uses live ports 3000/9080.
Only its constrained broker mounts the Docker socket. No model/provider
credentials are supplied. Real Chromium runs in an explicit operator QA
container sharing only the disposable UI network namespace. The toggle check
temporarily changes/restores only the disposable sandbox's research state.
Owned containers/networks/QA images are removed on success, failure, SIGINT or
SIGTERM; production images and existing resources remain. The report preserves
image IDs, host ports, actual nginx peers, test outputs and container logs.
"""
import argparse
import base64
import ipaddress
import json
import os
import pathlib
import socket
import signal
import re
import struct
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--source', default=pathlib.Path(__file__).resolve().parents[1], type=pathlib.Path)
    parser.add_argument('--output', required=True, type=pathlib.Path)
    args = parser.parse_args()
    args.source=args.source.resolve()
    args.output=args.output.resolve()
    def interrupted(signum, _frame):
        raise SystemExit(128+signum)
    signal.signal(signal.SIGTERM,interrupted)
    token = 'yummi-boundary-' + uuid.uuid4().hex[:10]
    control, private, ingress = token + '-control', token + '-sandbox-net', token + '-ingress'
    names = {k: token + '-' + k for k in ['sandbox', 'broker', 'orchestrator', 'ui']}
    created, networks = [], []
    report = {'token': token, 'checks': [], 'containers': names, 'images': {}}

    def docker(*argv, check=True, timeout=120):
        result = subprocess.run(['docker', *argv], text=True, capture_output=True, timeout=timeout)
        if check and result.returncode:
            raise RuntimeError('docker ' + ' '.join(argv[:4]) + ': ' + result.stderr[-3000:] + result.stdout[-1000:])
        return result

    def request(port, path, method='GET', body=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(f'http://127.0.0.1:{port}' + path, data=data,
                                     headers={'Content-Type': 'application/json'}, method=method)
        try:
            with urllib.request.urlopen(req, timeout=20) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()

    def checked(label, test):
        assert test, label
        report['checks'].append(label)
        print('PASS ' + label, flush=True)

    def run(name, image, extra, command=(), secondary=None):
        # Track before create so a start failure is also cleaned up.
        created.append(name)
        print('START '+name,flush=True)
        restrictions=[] if name==names['ui'] else ['--cap-drop=ALL','--security-opt=no-new-privileges:true']
        docker('create', '--name', name, '--label', 'yummi.control-boundary-qc='+token, *restrictions, '--no-healthcheck', *extra, image, *command)
        if secondary:
            docker('network', 'connect', '--ip', secondary[1], secondary[0], name)
        docker('start', name)

    try:
        assert (args.source / 'orchestrator/internal/dockerproxy/broker.go').is_file(), 'Use the committed control-boundary source'
        existing = docker('network', 'ls', '-q').stdout.split()
        for identity in existing:
            network = json.loads(docker('network', 'inspect', identity).stdout)[0]
            for config in network.get('IPAM', {}).get('Config', []) or []:
                subnet = config.get('Subnet')
                if not subnet:
                    continue
                parsed = ipaddress.ip_network(subnet)
                if parsed.version == 4:
                    assert not any(parsed.overlaps(ipaddress.ip_network(wanted)) for wanted in ['172.21.0.0/24', '172.22.0.0/24', '172.23.0.0/24']), 'Existing Docker subnet conflict: ' + subnet
        for image in ['yummi/orchestrator:local', 'yummi/docker-proxy:local', 'yummi/ui:local', 'yummi/sandbox-mcp-test:local']:
            report['images'][image] = docker('image', 'inspect', '--format', '{{.Id}}', image).stdout.strip()
        socket_info = os.environ.get('DOCKER_HOST') or docker('context', 'inspect', '--format', '{{.Endpoints.docker.Host}}').stdout.strip()
        assert socket_info.startswith('unix://'), 'Real local Unix Docker socket required'
        uds = socket_info.removeprefix('unix://')
        for network, subnet in [(control, '172.21.0.0/24'), (private, '172.23.0.0/24'), (ingress, '172.22.0.0/24')]:
            docker('network', 'create', *(['--internal'] if network!=ingress else []), '--subnet', subnet, '--label', 'yummi.control-boundary-qc='+token, network)
            networks.append(network)
        run(names['sandbox'], 'yummi/sandbox-mcp-test:local', ['--network', private, '--ip', '172.23.0.10', '--user', '1000:1000', '--entrypoint', '/bin/sleep', '--env', 'YUMMI_OLLAMA_URL='], ['infinity'])
        run(names['broker'], 'yummi/docker-proxy:local', ['--network', control, '--ip', '172.21.0.5', '--network-alias', 'docker-proxy', '--read-only', '--mount', 'type=bind,src=/var/run/docker.sock,dst=/var/run/docker.sock,readonly', '--env', 'YUMMI_DOCKER_PROXY_ADDR=:2375', '--env', 'YUMMI_DOCKER_SOCKET=/var/run/docker.sock', '--env', 'YUMMI_SANDBOX_CONTAINER='+names['sandbox'], '--env', 'YUMMI_DOCKER_PROXY_PEERS=172.21.0.3/32,127.0.0.1/32,::1/128'])
        environment = {
            'YUMMI_ORCHESTRATOR_HOST':'0.0.0.0', 'YUMMI_ORCHESTRATOR_PORT':'9080',
            'YUMMI_OPERATOR_ALLOWED_PEERS':'172.21.0.4/32,127.0.0.1/32,::1/128',
            'YUMMI_SANDBOX_POLICY_ADDR':'172.23.0.3:9081',
            'YUMMI_SANDBOX_CONTAINER':names['sandbox'], 'YUMMI_DB_PATH':'/tmp/orchestrator.db',
            'YUMMI_HOME':'/tmp/yummi', 'YUMMI_AUTO_SKILL_DIR':'/tmp/skills', 'YUMMI_PERSONA_DIR':'/persona/v0.1',
            'YUMMI_OLLAMA_URL':'', 'YUMMI_EINO_MODEL_BASE_URL':'', 'YUMMI_EINO_MODEL_NAME':'',
            'YUMMI_EINO_MODEL_API_KEY':'', 'YUMMI_EINO_PROVIDER':'', 'YUMMI_AGENT_BACKEND':'graph',
            'DOCKER_HOST':'tcp://docker-proxy:2375',
        }
        env_args = [piece for key,value in environment.items() for piece in ['--env', key+'='+value]]
        run(names['orchestrator'], 'yummi/orchestrator:local', ['--network', control, '--ip', '172.21.0.3', '--network-alias', 'orchestrator', *env_args], secondary=(private,'172.23.0.3'))
        def free_port():
            with socket.socket() as probe:
                probe.bind(('127.0.0.1',0))
                return probe.getsockname()[1]
        ports=[free_port(),free_port()]
        assert len(set(ports))==2 and not set(ports)&{3000,9080}
        report['host_ports']=ports
        run(names['ui'], 'yummi/ui:local', ['--network', control, '--ip', '172.21.0.4', '--network-alias', 'yummi-ui', '-p', f'127.0.0.1:{ports[0]}:80', '-p', f'127.0.0.1:{ports[1]}:80'],secondary=(ingress,'172.22.0.4'))
        report['ui_inspect']=json.loads(docker('inspect',names['ui']).stdout)[0]
        deadline=time.monotonic()+10
        last=None
        while time.monotonic()<deadline:
            try:
                last=request(ports[0],'/healthz')
                if last[0]==200: break
            except (OSError,urllib.error.URLError) as error: last=str(error)
            time.sleep(1)
        host_ready=isinstance(last,tuple) and last[0]==200
        report['host_ingress']= 'passed' if host_ready else 'failed'
        if not host_ready:
            print('FAIL host published ports unavailable; continuing isolated namespace checks',flush=True)
        else:
            checked('host published healthz on UI port',isinstance(last,tuple) and last[0]==200)
            checked('host published healthz on API compatibility port',request(ports[1],'/healthz')[0]==200)
            status,body=request(ports[0],'/api/v1/sessions','POST',{'name':token})
            checked('host creates session through UI ingress',status==201)
            session=json.loads(body);sid=session['id']
            checked('second published port reads same session',request(ports[1],'/api/v1/sessions/'+sid)[0]==200)
            status,_=request(ports[1],'/api/v1/sessions/'+sid,'PATCH',{'name':token+' renamed'})
            checked('session rename through compatibility ingress',status==200)
            checked('renamed session visible through UI ingress',json.loads(request(ports[0],'/api/v1/sessions/'+sid)[1])['name']==token+' renamed')
            # Validate a real websocket handshake and the first event, without
            # introducing a host browser or external WebSocket dependency.
            with socket.create_connection(('127.0.0.1',ports[0]),timeout=10) as ws:
                key=base64.b64encode(os.urandom(16)).decode()
                ws.sendall((f'GET /ws/events?session_id={sid} HTTP/1.1\r\nHost: 127.0.0.1:{ports[0]}\r\nOrigin: http://127.0.0.1:{ports[0]}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n').encode())
                def exact(n):
                    data=b''
                    while len(data)<n:
                        part=ws.recv(n-len(data));assert part,'websocket closed';data+=part
                    return data
                headers=b''
                while b'\r\n\r\n' not in headers:
                    headers+=exact(1);assert len(headers)<16384
                checked('host WS upgrade through UI ingress',headers.startswith(b'HTTP/1.1 101'))
                first=exact(2);length=first[1]&127
                if length==126:length=struct.unpack('!H',exact(2))[0]
                elif length==127:length=struct.unpack('!Q',exact(8))[0]
                assert length<1<<20
                event=json.loads(exact(length))
                checked('real WS control.hello event',event['type']=='control.hello')
            checked('session deletion through UI ingress',request(ports[0],'/api/v1/sessions/'+sid,'DELETE')[0]==204)
            listed=json.loads(request(ports[1],'/api/v1/sessions')[1])
            if isinstance(listed,dict):listed=listed.get('sessions',[])
            checked('session deletion closes persisted session through compatibility port',any(item.get('id')==sid and item.get('status')=='closed' for item in listed))
        sandbox_probe = r"""
import json,socket,urllib.request,urllib.error
result={}
def check(url,headers=None,method='GET',data=None):
 try:
  with urllib.request.urlopen(urllib.request.Request(url,headers=headers or {},method=method,data=data),timeout=2) as response:return response.status
 except urllib.error.HTTPError as error:return error.code
 except (urllib.error.URLError,OSError):return 'unreachable'
# A valid operator Host isolates peer authorization from DNS-rebinding checks.
result['main_api']=check('http://172.23.0.3:9080/api/v1/sessions',{'Host':'127.0.0.1'})
result['spoofed_main_api']=check('http://172.23.0.3:9080/api/v1/sessions',{'Host':'127.0.0.1','X-Forwarded-For':'172.21.0.4','Origin':'http://localhost:3000'})
result['spoofed_exec']=check('http://172.23.0.3:9080/api/v1/sandbox/exec',{'Host':'127.0.0.1','X-Forwarded-For':'127.0.0.1','Origin':'http://localhost:3000','Content-Type':'application/json'},'POST',b'{"command":"id"}')
result['hostname_policy']=check('http://172.23.0.3:9081/v1/hostname-policy')
result['policy_operator_route']=check('http://172.23.0.3:9081/api/v1/sessions')
result['policy_mutation_route']=check('http://172.23.0.3:9081/api/v1/gateway/rules',method='POST',data=b'{}')
result['policy_egress_fail_closed']=check('http://172.23.0.3:9081/v1/egress/check',{'Content-Type':'application/json'},'POST',b'{"ip":"203.0.113.10"}')
for label,url in [('broker_name','http://docker-proxy:2375/_ping'),('broker_ip','http://172.21.0.5:2375/_ping'),('ui_name','http://yummi-ui:80/'),('ui_ip','http://172.21.0.4:80/'),('ui_ingress_ip','http://172.22.0.4:80/')]:result[label]=check(url,{'Host':'127.0.0.1'})
print(json.dumps(result))
"""
        observed=json.loads(docker('exec','--user','1000:1000',names['sandbox'],'python3','-c',sandbox_probe,timeout=45).stdout)
        report['sandbox_observations']=observed
        expected={'main_api':403,'spoofed_main_api':403,'spoofed_exec':403,'hostname_policy':200,'policy_operator_route':404,'policy_mutation_route':404,'policy_egress_fail_closed':503,'broker_name':'unreachable','broker_ip':'unreachable','ui_name':'unreachable','ui_ip':'unreachable','ui_ingress_ip':'unreachable'}
        for key,value in expected.items():checked('sandbox '+key,observed.get(key)==value)
        exec_status,exec_body=request(ports[0],'/api/v1/sandbox/exec','POST',{'command':'id -u'})
        report['actual_operator_exec']={'status':exec_status,'result':json.loads(exec_body)}
        checked('actual orchestrator Docker CLI executes UID1000 through broker',exec_status==200 and json.loads(exec_body).get('exit_code')==0 and json.loads(exec_body).get('stdout','').strip()=='1000')
        qa_image=token+'-operator-qa'
        report['qa_image']=qa_image
        docker('build','--label','yummi.control-boundary-qc='+token,'-f',str(args.source/'qa/Dockerfile.operator-ui'),'--build-arg','SANDBOX_IMAGE=yummi/sandbox-mcp-test:local','-t',qa_image,str(args.source),timeout=180)
        for script in ['session_live_qc.py','web_search_toggle_qc.py']:
            qa_name=token+'-'+script.removesuffix('.py').replace('_','-')
            created.append(qa_name)
            result=docker('run','--name',qa_name,'--label','yummi.control-boundary-qc='+token,'--network','container:'+names['ui'],'--cap-drop=ALL','--security-opt=no-new-privileges:true','--read-only','--tmpfs','/tmp:rw,nosuid,nodev','--tmpfs','/home/pentester:rw,nosuid,nodev,mode=1777',qa_image,script,check=False,timeout=180)
            report.setdefault('operator_qa',{})[script]={'exit_code':result.returncode,'stdout':result.stdout[-15000:],'stderr':result.stderr[-15000:]}
            checked('operator '+script,result.returncode==0)
        if not host_ready:raise AssertionError('host published ingress failed')
        report['status']='passed'

    except BaseException as error:
        report['status']='failed'
        report['error']=repr(error)
        raise
    finally:
        def owned(kind, name):
            inspected=docker(kind,'inspect',name,check=False)
            if inspected.returncode:return False
            item=json.loads(inspected.stdout)[0]
            labels=item.get('Config',{}).get('Labels',{}) if kind in ('container','image') else item.get('Labels',{})
            return (labels or {}).get('yummi.control-boundary-qc')==token
        for name in reversed(created):
            if not owned('container',name):continue
            log_result=docker('logs', '--tail', '120', name, check=False)
            report.setdefault('logs', {})[name] = (log_result.stdout+log_result.stderr)[-30000:]
            docker('rm', '-f', name, check=False)
        for network in reversed(networks):
            if owned('network',network):docker('network', 'rm', network, check=False)
        if report.get('qa_image') and owned('image',report['qa_image']):docker('image','rm',report['qa_image'],check=False)
        ui_logs=report.get('logs',{}).get(names['ui'],'')
        report['observed_nginx_peers']=sorted(set(re.findall(r'^([0-9.]+) .*"(?:GET|POST|PATCH|DELETE) ',ui_logs,re.M)))
        args.output.write_text(json.dumps(report, indent=2))



if __name__ == '__main__':
    main()
