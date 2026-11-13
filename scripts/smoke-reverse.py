"""Isolated Windows reverse-export smoke test; never a live-terminal acceptance test.

Run from a checkout (64-bit Windows, Python 3, .NET Framework compiler installed):
    python scripts/smoke-reverse.py --binary C:\\build\\xshell-bridge.exe
    python scripts/smoke-reverse.py --binary C:\\build\\xshell-bridge.exe --csc C:\\path\\csc.exe

Compiles the CURRENT desktop/HostReverse.cs, without rewriting or patching it.
Compiles the real log helper and synthetic writer; exports a single JS fixture.
Both System32 and SysWOW64 cscript are required. No Xshell, SSH, command server,
real keys, existing runtime, user configuration or non-loopback endpoint is used.
Only the initialized fixture Bridge is contacted. All terminal APIs are synthetic;
Python supplies task events rather than executing shell commands. Success proves
export/transport/authorization/state routing, not live terminal execution.
"""
import argparse
import hashlib
import hmac
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import time
import sys
from urllib import error, parse, request

ROOT = Path(__file__).resolve().parents[1]
MARKER = 'XSH_REVERSE_SMOKE_COMPLETED'
sys.path.insert(0, str(ROOT / 'tests'))
from test_log_export import compile_native

JS_TEST = r'''
function insist(value, message) { if (!value) { throw new Error(message); } }
var fs = new ActiveXObject('Scripting.FileSystemObject');
insist(!fs.FileExists(VM_ROOT + '\\agent.json'), 'unexpected VM agent.json');
insist(typeof Main === 'function', 'export did not define Main');
var session = {
    Connected:true, Path:'fixture-only', RemoteAddress:'192.0.2.1',
    RemotePort:'22', SessionName:'Synthetic fixture', TabText:'Synthetic fixture',
    UserName:'fixture', ScriptFolderPath:VM_ROOT,
    Sleep:function(ms) { WScript.Sleep(ms); }
};
var screen = {
    Synchronous:false,
    IsAltScreen:function() { return false; },
    WaitForStrings:function(values, timeout) {
        insist(values instanceof Array, 'official API array expected');
        WScript.Sleep(timeout); return 0;
    },
    Send:function() { throw new Error('NO COMMAND MAY BE SENT'); },
    Get:function() { throw new Error('NO TERMINAL CAPTURE EXPECTED'); }
};
var fakeXsh = {Session:session, Screen:screen};
var boot = null;
try {
    boot = remoteBootstrap(fakeXsh, XSH_REMOTE_BUNDLE);
    insist(boot.config.binding_mode === 'manual_terminal', 'manual mode required');
    var nativeJob={id:'abc',command:'SYNTHETIC ONLY',timeout_ms:10000},nativeOutput='',nativeStatus='';
    var log=makeLogFixture(function(){return nativeJob;},'native UTF8 \u4e2d\u6587\r\n',7);
    var localAPI={post:function(path,data){if(path==='/v1/agent/register'){return {id:'fixture',generation:'fixture'};}if(path==='/v1/agent/event'){nativeOutput+=data.output;nativeStatus=data.status;}return {};}};
    new Agent(log.xsh,boot.config,localAPI).execute(nativeJob);
    insist(log.sends()===1 && nativeStatus==='completed' && nativeOutput==='native UTF8 \u4e2d\u6587\n','native log capture contract');
    // Manual registration using a separate synthetic terminal.
    var config = {
        endpoint:boot.config.endpoint, token:boot.config.token,
        source_id:boot.config.source_id, environment:boot.config.environment,
        binding_mode:'manual_terminal'
    };
    var agent = new Agent(fakeXsh, config);
    insist(agent.session.state === 'pending', 'registration must be pending');
    insist(parseJSON(agent.bound).SourceID === config.token.split('.')[1], 'token/source mismatch');
    insist(!agent.poll().job, 'pending/new registration received replay');
    WScript.Echo('XSH_REVERSE_SESSION ' + stringifyJSON(agent.session).replace(new RegExp('[^' + String.fromCharCode(0) + '-' + String.fromCharCode(127) + ']', 'g'), function(ch) { return String.fromCharCode(92) + 'u' + ('0000' + ch.charCodeAt(0).toString(16)).slice(-4); }));
} finally {
    if (boot) { boot.close(); }
}
insist(!fs.FileExists(VM_ROOT + '\\agent.json'), 'VM configuration created');
WScript.Echo('XSH_REVERSE_SMOKE_COMPLETED');
'''


def require(value, message):
    if not value:
        raise AssertionError(message)


def execute(args, timeout=30, env=None):
    result = subprocess.run([str(x) for x in args], capture_output=True,
                            timeout=timeout, env=env)
    require(result.returncode == 0,
            'subprocess failed: ' + str(args[0]) + '\n' +
            (result.stdout + result.stderr).decode(errors='replace'))
    return result


def stop_owned(process):
    if process is None:
        return
    if process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()  # Only this exact owned Popen, never a process-name kill.
            process.wait(timeout=5)


def fixture_path(root, path):
    root = root.resolve(strict=True)
    path = Path(path).absolute()
    require(path != root and root in path.parents, 'fixture path escaped root')
    current = path
    while current != root:
        if current.exists():
            require(not current.is_symlink(), 'fixture symlink refused')
            attrs = getattr(current.stat(), 'st_file_attributes', 0)
            require(not attrs & 0x400, 'fixture reparse point refused')
        current = current.parent
    require(root in path.resolve().parents, 'resolved fixture path escaped root')
    return path


def run(binary, csc):
    require(os.name == 'nt', 'Windows/.NET/WSH required')
    windows = Path(os.environ['WINDIR'])
    require(not os.environ.get('PROCESSOR_ARCHITEW6432'),
            'use 64-bit Python to avoid System32 filesystem redirection')
    engines = [windows / 'System32/cscript.exe', windows / 'SysWOW64/cscript.exe']
    require(all(p.is_file() for p in engines), 'both 64-bit and 32-bit cscript required')
    with tempfile.TemporaryDirectory(prefix='xsh-reverse-smoke-') as temp:
        root = Path(temp).resolve()
        runtime = fixture_path(root, root / 'runtime')
        bundle = fixture_path(root, root / 'bundle')
        catalog = fixture_path(root, root / 'catalog')
        vm = fixture_path(root, root / 'vm')
        for path in (runtime, bundle, catalog, vm, vm / 'local', vm / 'tmp'):
            fixture_path(root, path).mkdir()
        execute([binary, 'init', '--dir', runtime], timeout=15)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        require(port >= 1024, 'unexpected privileged random port')
        endpoint = 'http://127.0.0.1:' + str(port)
        config_path = runtime / 'bridge.json'
        cfg = json.loads(config_path.read_text(encoding='utf-8-sig'))
        cfg['listen'] = '127.0.0.1:' + str(port)
        config_path.write_text(json.dumps(cfg), encoding='utf-8')
        keys = {}
        for role in ('agent', 'client', 'admin'):
            path = runtime / (role + '.json')
            data = json.loads(path.read_text(encoding='utf-8-sig'))
            keys[role] = data['token']
            data['endpoint'] = endpoint
            path.write_text(json.dumps(data), encoding='utf-8')
            require(keys[role] == cfg['keys'][role], 'init key/config disagreement')
        compile_native(ROOT / 'agent/LogCapture.cs', bundle / 'XshLogCapture.exe', winexe=True)
        compile_native(ROOT / 'tests/LogWriter.cs', root / 'LogWriter.exe')
        for name in ('xshell_agent.js', 'remote_bootstrap.js', 'log_parser.js'):
            (bundle / name).write_bytes((ROOT / 'agent' / name).read_bytes())
        driver = fixture_path(root, root / 'ReverseExportDriver.exe')
        execute([csc, '/nologo', '/target:exe', '/platform:anycpu',
                 '/r:System.Web.Extensions.dll', '/out:' + str(driver),
                 ROOT / 'desktop/HostReverse.cs', ROOT / 'desktop/ReverseExportDriver.cs'])
        opener = request.build_opener(request.ProxyHandler({}))

        def api(path, body=None, role='client', token=None, expected=200):
            require(path.startswith('/v1/'), 'unexpected fixture API path')
            req = request.Request(endpoint + path,
                data=None if body is None else json.dumps(body).encode('utf-8'),
                headers={'Authorization': 'Bearer ' + (token or keys[role]),
                         'Content-Type': 'application/json'})
            try:
                with opener.open(req, timeout=5) as response:
                    status, raw = response.status, response.read(4194305)
            except error.HTTPError as exc:
                status, raw = exc.code, exc.read(4194305)
            require(status == expected, path + ': expected HTTP ' + str(expected) +
                    ', got ' + str(status) + ': ' + raw[:1000].decode(errors='replace'))
            require(len(raw) <= 4194304, 'fixture response too large')
            return json.loads(raw) if raw.strip() else None

        server = None

        def start_server():
            process = subprocess.Popen([str(binary), 'serve', '--dir', str(runtime)],
                                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            try:
                deadline = time.monotonic() + 6
                while time.monotonic() < deadline:
                    require(process.poll() is None, 'fixture Bridge exited during startup')
                    try:
                        api('/v1/health')
                        return process
                    except error.URLError:
                        time.sleep(.04)
                raise AssertionError('fixture Bridge readiness deadline')
            except BaseException:
                stop_owned(process)
                raise

        env = os.environ.copy()
        env.update(LOCALAPPDATA=str(vm / 'local'), TEMP=str(vm / 'tmp'), TMP=str(vm / 'tmp'))

        def wsh(exported, engine, sequence):
            script = fixture_path(root, vm / ('test-' + str(sequence) + '.js'))
            # The export defines Main but does not invoke it. Never append Main().
            body = exported + '\nvar WRITER=' + json.dumps(str(root / 'LogWriter.exe')) + ';\n' + (ROOT / 'tests/log_session_fixture.js').read_text(encoding='utf-8') + '\nvar VM_ROOT = ' + json.dumps(str(vm)) + ';\n' + JS_TEST
            script.write_text(body, encoding='utf-16')
            result = execute([engine, '//nologo', script], timeout=35, env=env)
            require(result.stderr == b'', 'cscript stderr must be empty')
            text = result.stdout.decode('ascii', 'strict')
            require(MARKER in text.splitlines(), 'cscript returned zero without completion marker')
            lines = [line[len('XSH_REVERSE_SESSION '):] for line in text.splitlines()
                     if line.startswith('XSH_REVERSE_SESSION ')]
            require(len(lines) == 1, 'missing/duplicate actual Agent registration')
            require(not (vm / 'agent.json').exists(), 'VM contains agent.json')
            base = fixture_path(root, vm / 'local' / 'XshellBridge-agent')
            if base.exists():
                require(not list(base.iterdir()), 'bootstrap left helper/output/private directory behind')
            return json.loads(lines[0])

        try:
            server = start_server()
            sequence = 0
            for mode in ('manual_terminal',):
                destination = fixture_path(root, vm / (mode + '.js'))
                result = execute([driver, runtime, bundle, catalog, destination, str(port), mode])
                require(result.stderr == b'', 'native driver stderr')
                preview = json.loads(result.stdout.decode('utf-8-sig'))
                exported = destination.read_text(encoding='utf-8-sig')
                first = exported.splitlines()[0]
                require(first.startswith('var XSH_REMOTE_BUNDLE = ') and first.endswith(';'),
                        'unexpected standalone export header')
                embedded = json.loads(first[len('var XSH_REMOTE_BUNDLE = '):-1])
                require(embedded['helper_size'] == (bundle / 'XshLogCapture.exe').stat().st_size and len(embedded['helper_sha256']) == 64, 'missing native helper metadata')
                source = preview['source_id']
                token = embedded['config']['token']
                digest = hmac.new(keys['agent'].encode(),
                                  ('xshell-bridge-source-v1:' + source).encode(), hashlib.sha256).hexdigest()
                require(re.fullmatch('[0-9a-f]{32}', source), 'invalid source ID')
                require(token == 'xsh-agent-v1.' + source + '.' + digest, 'scoped token derivation')
                require(embedded['config']['source_id'] == source, 'export source mismatch')
                require(embedded['config']['endpoint'] == endpoint, 'non-fixture endpoint')
                require(all(secret not in exported for secret in keys.values()),
                        'master/client/admin credential leaked into standalone export')
                for engine in engines:
                    sequence += 1
                    session = wsh(exported, engine, sequence)
                    require(json.loads(session['fingerprint'])['SourceID'] == source, 'registration SourceID')
                    member = {k: session[k] for k in ('id', 'generation', 'fingerprint')}
                    api('/v1/agent/poll', member, token=token)
                    api('/v1/sessions', token=token, expected=403)
                    api('/v1/admin/decide', {}, token=token, expected=403)
                    api('/v1/admin/shutdown', {}, token=token, expected=403)
                    other_source = 'f' * 32 if source != 'f' * 32 else 'e' * 32
                    other_token = 'xsh-agent-v1.' + other_source + '.' + hmac.new(
                        keys['agent'].encode(), ('xshell-bridge-source-v1:' + other_source).encode(),
                        hashlib.sha256).hexdigest()
                    other = api('/v1/agent/register', {
                        'instance': 'reverse-other-fixture-' + str(sequence), 'name': 'Other synthetic source',
                        'environment': 'ISOLATED TEST ONLY', 'auto_name': True,
                        'fingerprint': json.dumps({'SourceID': other_source, 'Terminal': 'fixture'})
                    }, token=other_token)
                    other_member = {k: other[k] for k in ('id', 'generation', 'fingerprint')}
                    api('/v1/agent/poll', other_member, token=token, expected=403)
                    api('/v1/agent/detach', other_member, token=token, expected=403)
                    api('/v1/agent/detach', other_member, token=other_token)
                    api('/v1/admin/activate', {'id': session['id'], 'generation': session['generation']}, role='admin')
                    completed = []
                    for task in range(2):
                        job = api('/v1/jobs', {
                            'session_id': session['id'], 'generation': session['generation'],
                            'request_id': 'reverse-' + str(sequence) + '-' + str(task),
                            'command': 'SYNTHETIC FIXTURE ONLY - DO NOT EXECUTE',
                            'cwd': '/fixture', 'timeout_ms': 15000})
                        require(job['status'] == 'pending', 'job bypassed manual approval')
                        api('/v1/admin/decide', {'id': job['id'], 'allow': True}, role='admin')
                        dispatched = api('/v1/agent/poll', member, token=token)
                        require(dispatched['job']['id'] == job['id'], 'wrong task dispatched')
                        output = 'synthetic task ' + str(task) + ' 中文\n'
                        event = dict(session_id=session['id'], generation=session['generation'],
                                     fingerprint=session['fingerprint'], job_id=job['id'], seq=1,
                                     output=output, status='running')
                        api('/v1/agent/event', event, token=token)
                        query = parse.urlencode({'id': job['id'], 'cursor': 0})
                        routed = api('/v1/output?' + query)
                        require(routed['output'] == output, 'output routed to wrong task')
                        event.update(seq=2, output='done\n', status='completed', exit_code=task)
                        api('/v1/agent/event', event, token=token)
                        query = parse.urlencode({'id': job['id'],
                                                 'cursor': routed['next_cursor']})
                        tail = api('/v1/output?' + query)
                        require(tail['output'] == 'done\n' and tail['exit_code'] == task,
                                'output cursor/completion mismatch')
                        require(api('/v1/job?id=' + job['id'])['status'] == 'completed', 'job not completed')
                        require(not api('/v1/agent/poll', member, token=token).get('job'), 'task replay')
                        completed.append(job['id'])
                    api('/v1/agent/detach', member, token=token)
                    # Restart only our Bridge against its own persisted fixture.
                    stop_owned(server)
                    server = None
                    server = start_server()
                    sequence += 1
                    restarted = wsh(exported, engine, sequence)
                    require(restarted['state'] == 'pending', 'reattach bypassed activation')
                    new_member = {k: restarted[k] for k in ('id', 'generation', 'fingerprint')}
                    require(not api('/v1/agent/poll', new_member, token=token).get('job'), 'restart replay')
                    for job_id in completed:
                        require(api('/v1/job?id=' + job_id)['status'] == 'completed', 'restart lost completion')
                    api('/v1/agent/detach', new_member, token=token)
                    print('PASS:', mode, engine.parent.name,
                          'standalone/COM + real Bridge; synthetic terminal, NOT live acceptance')
        finally:
            stop_owned(server)
        # TemporaryDirectory removes only this invocation's exact root. Validate
        # every descendant first; never follow a junction during cleanup.
        for directory, dirs, files in os.walk(root, followlinks=False):
            for name in dirs + files:
                fixture_path(root, Path(directory) / name)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--binary', required=True, type=Path)
    parser.add_argument('--csc', type=Path, default=Path(os.environ.get('WINDIR', r'C:\Windows')) /
                        'Microsoft.NET/Framework64/v4.0.30319/csc.exe')
    args = parser.parse_args()
    run(args.binary.resolve(strict=True), args.csc.resolve(strict=True))
