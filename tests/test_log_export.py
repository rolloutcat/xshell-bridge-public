"""Native HostReverse export -> Windows cscript -> verified helper + fake writer."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from contextlib import contextmanager

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / 'dist/_test-artifacts/log-export'


@contextmanager
def retained_temp(prefix):
    yield tempfile.mkdtemp(prefix=prefix, dir=ARTIFACTS)


def compile_native(source, output, extra=(), winexe=False):
    csc = Path(os.environ['WINDIR']) / 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
    output.parent.mkdir(parents=True, exist_ok=True)
    result = subprocess.run([str(csc), '/nologo', '/optimize+', '/langversion:5', '/platform:anycpu',
                             '/target:' + ('winexe' if winexe else 'exe'), '/r:System.Web.Extensions.dll',
                             '/out:' + str(output), str(source), *map(str, extra)], capture_output=True, timeout=30)
    if result.returncode:
        raise AssertionError((result.stdout + result.stderr).decode(errors='replace'))


@unittest.skipUnless(os.name == 'nt', 'Windows native export required')
class LogExportTests(unittest.TestCase):
    def test_actual_native_export_and_wscript_exec_winexe_pipes(self):
        ARTIFACTS.mkdir(parents=True, exist_ok=True)
        with retained_temp('xsh-reverse-smoke-中文 ') as temp:
            root = Path(temp)
            runtime, bundle, catalog, vm = [root / x for x in ('runtime', 'bundle', 'catalog', 'vm')]
            for p in (runtime, bundle, catalog, vm, vm / 'local'):
                p.mkdir()
            compile_native(ROOT / 'agent/LogCapture.cs', bundle / 'XshLogCapture.exe', winexe=True)
            compile_native(ROOT / 'tests/LogWriter.cs', root / 'LogWriter.exe')
            compile_native(ROOT / 'desktop/ReverseExportDriver.cs', root / 'Export.exe', [ROOT / 'desktop/HostReverse.cs'])
            for name in ('xshell_agent.js', 'remote_bootstrap.js', 'log_parser.js'):
                shutil.copyfile(ROOT / 'agent' / name, bundle / name)
            for role in ('agent', 'client'):
                (runtime / (role + '.json')).write_text(json.dumps(dict(endpoint='http://127.0.0.1:18888', token='fixture-' + role + '-' * 40)), encoding='utf-8')
            exported = vm / 'export.js'
            result = subprocess.run([str(root / 'Export.exe'), str(runtime), str(bundle), str(catalog), str(exported), '18888'], capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            source = exported.read_text(encoding='utf-8-sig')
            metadata = json.loads(source.splitlines()[0][len('var XSH_REMOTE_BUNDLE = '):-1])
            self.assertEqual(metadata['helper_size'], (bundle / 'XshLogCapture.exe').stat().st_size)
            body = source + '\nvar WRITER=' + json.dumps(str(root / 'LogWriter.exe')) + ';var VM_ROOT=' + json.dumps(str(vm)) + ';\n' + (ROOT / 'tests/log_export_cases.js').read_text(encoding='utf-8')
            fixture = vm / 'test.js'
            fixture.write_text(body, encoding='utf-16')
            env = dict(os.environ, LOCALAPPDATA=str(vm / 'local'))
            for bits in ('System32', 'SysWOW64'):
                with self.subTest(engine=bits):
                    result = subprocess.run([str(Path(os.environ['WINDIR']) / bits / 'cscript.exe'), '//nologo', str(fixture)], capture_output=True, timeout=45, env=env)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(result.stderr, b'')
                    self.assertIn(b'XSH_NATIVE_EXPORT_COMPLETED', result.stdout, result.stdout)

            failure_body = source + '\nvar WRITER=' + json.dumps(str(root / 'LogWriter.exe')) + ';\n' + (ROOT / 'tests/log_failure_native_cases.js').read_text(encoding='utf-8')
            failure_fixture = vm / 'failure-test.js'; failure_fixture.write_text(failure_body, encoding='utf-16')
            for bits in ('System32', 'SysWOW64'):
                with self.subTest(failure_engine=bits):
                    result = subprocess.run([str(Path(os.environ['WINDIR']) / bits / 'cscript.exe'), '//nologo', str(failure_fixture)], capture_output=True, timeout=45, env=env)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(result.stderr, b'')
                    self.assertIn(b'XSH_NATIVE_FAILURES_COMPLETED', result.stdout)

            events = []
            token = 'synthetic-winhttp-token-' + 'x' * 40
            class Handler(BaseHTTPRequestHandler):
                def log_message(self, *args): pass
                def do_POST(self):
                    body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                    events.append((self.path, self.headers.get('Authorization'), body))
                    response = {'id': 'fixture', 'generation': 'fixture'} if self.path.endswith('/register') else {}
                    data = json.dumps(response).encode()
                    self.send_response(200); self.send_header('Content-Type', 'application/json'); self.send_header('Content-Length', str(len(data))); self.end_headers(); self.wfile.write(data)
            server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
            try:
                http_source = source + '\nvar WRITER=' + json.dumps(str(root / 'LogWriter.exe')) + ';\n'
                http_source += (ROOT / 'tests/log_session_fixture.js').read_text(encoding='utf-8')
                http_source += '\nXSH_REMOTE_BUNDLE.config.endpoint=' + json.dumps('http://127.0.0.1:%d' % server.server_port) + ';XSH_REMOTE_BUNDLE.config.token=' + json.dumps(token) + ';\n'
                http_source += r'''
var job={id:'abc',command:'fixture only',timeout_ms:10000};
var f=makeLogFixture(function(){return job;},'WinHTTP fixture\r\n',7);
var boot=remoteBootstrap(f.xsh,XSH_REMOTE_BUNDLE);
try { new Agent(f.xsh,boot.config).execute(job); if(f.sends()!==1)throw Error('Send count'); WScript.Echo('XSH_WINHTTP_NATIVE_COMPLETED'); }
finally { boot.close(); }
'''
                http_fixture = vm / 'http-test.js'; http_fixture.write_text(http_source, encoding='utf-16')
                for bits in ('System32', 'SysWOW64'):
                    with self.subTest(winhttp_engine=bits):
                        before = len(events)
                        result = subprocess.run([str(Path(os.environ['WINDIR']) / bits / 'cscript.exe'), '//nologo', str(http_fixture)], capture_output=True, timeout=45, env=env)
                        self.assertEqual(result.returncode, 0, result.stderr)
                        self.assertEqual(result.stderr, b'')
                        self.assertIn(b'XSH_WINHTTP_NATIVE_COMPLETED', result.stdout)
                        seen = events[before:]
                        self.assertTrue(all(auth == 'Bearer ' + token for _, auth, _ in seen))
                        self.assertEqual(sum(body.get('status') == 'completed' for _, _, body in seen), 1)
                        self.assertTrue(any('WinHTTP fixture' in body.get('output', '') for _, _, body in seen))
            finally:
                server.shutdown(); server.server_close(); thread.join(timeout=3)
