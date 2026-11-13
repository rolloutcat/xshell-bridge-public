"""Real Windows JScript/COM tests; Xshell terminal APIs are isolated fakes."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
import unittest
from contextlib import contextmanager

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = Path(os.environ.get('XSH_TEST_ARTIFACTS', ROOT / 'dist/_test-artifacts/jscript'))

@contextmanager
def retained_temp():
    yield tempfile.mkdtemp(prefix='xsh-js-test-', dir=ARTIFACTS)


@unittest.skipUnless(os.name == "nt", "Windows JScript/WinHTTP required")
class JScriptTests(unittest.TestCase):
    def run_js(self, body):
        source = (ROOT / "agent/log_parser.js").read_text(encoding="utf-8-sig") + "\n" + (ROOT / "agent/xshell_agent.js").read_text(encoding="utf-8-sig")
        engines = [Path(os.environ["WINDIR"]) / "System32/cscript.exe",
                   Path(os.environ["WINDIR"]) / "SysWOW64/cscript.exe"]
        ARTIFACTS.mkdir(parents=True, exist_ok=True)
        with retained_temp() as tmp:
            script = Path(tmp) / "test.js"
            script.write_text(source + "\n" + body + '\nWScript.Echo("XSH_TEST_COMPLETED");', encoding="utf-16")
            for engine in engines:
                if not engine.exists():
                    continue
                with self.subTest(engine=str(engine)):
                    result = subprocess.run([str(engine), "//nologo", str(script)], capture_output=True, timeout=25)
                    self.assertEqual(result.returncode, 0, (result.stdout + result.stderr).decode(errors="replace"))
                    # Windows cscript can return 0 even when JScript aborts on an error.
                    self.assertEqual(result.stderr, b"", result.stderr.decode(errors="replace"))
                    self.assertIn(b"XSH_TEST_COMPLETED", result.stdout, result.stdout.decode(errors="replace"))

    def test_terminal_and_parser_invariants(self):
        self.run_js((ROOT / "tests/jscript_cases.js").read_text(encoding="utf-8"))

    def test_log_capture_contract(self):
        self.run_js((ROOT / "tests/capture_cases.js").read_text(encoding="utf-8"))

    def test_clock_failure_diagnostics_and_no_replay(self):
        cases = (ROOT / "tests/capture_cases.js").read_text(encoding="utf-8")
        definitions = cases[cases.index("(function(){") + len("(function(){"):cases.index("var r=run({})")]
        self.run_js(definitions + "\n" + (ROOT / "tests/clock_failure_cases.js").read_text(encoding="utf-8"))

    def test_deferred_path_plain_es3(self):
        cases = (ROOT / "tests/capture_cases.js").read_text(encoding="utf-8")
        definitions = cases[cases.index("(function(){") + len("(function(){"):cases.index("var r=run({})")]
        self.run_js(definitions + "\n" + (ROOT / "tests/log_path_es3_cases.js").read_text(encoding="utf-8-sig"))

    def test_log_parser(self):
        parser = (ROOT / "agent/log_parser.js").read_text(encoding="utf-8")
        cases = (ROOT / "tests/log_parser_cases.js").read_text(encoding="utf-8")
        self.run_js(parser + "\n" + cases)

    def test_optional_log_path_diagnostic_es3(self):
        source = (ROOT / "agent/log_path_diagnostic.js").read_text(encoding="utf-8")
        self.run_js(source + r'''
var s={Logging:false,LogFilePath:"original\\path.log"};
var r=diagnoseLogPath(s,"C:\\private\\fresh\\capture.log");
if(s.LogFilePath!=="original\\path.log"||r.indexOf("original configured preference unknown")<0||r.indexOf("phase=NoSend")<0){throw Error("diagnostic restore");}
s.Logging=true;r=diagnoseLogPath(s,"C:\\private\\fresh\\capture.log");
if(r.indexOf("refused")<0){throw Error("foreign logging");}
s.Logging=false;s.LogFilePath=42;r=diagnoseLogPath(s,"C:\\private\\fresh\\capture.log");
if(s.LogFilePath!==42||r.indexOf("number")<0||r.indexOf("refused")<0){throw Error("non string");}
''')

    def test_manual_terminal_pending_registration(self):
        self.run_js((ROOT / "tests/jscript_manual_cases.js").read_text(encoding="utf-8"))

    def test_real_winhttp_unicode_and_no_redirect(self):
        seen = []
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                seen.append((self.path, self.headers.get("Authorization"), body))
                if body.get("redirect"):
                    self.send_response(302)
                    self.send_header("Location", "/credential-leak")
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                response = json.dumps({"value": body["value"]}, ensure_ascii=False).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json; charset=utf-8")
                self.send_header("Content-Length", str(len(response)))
                self.end_headers()
                self.wfile.write(response)

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        token = "isolated-test-token-" + "a" * 40
        try:
            body = "var config = " + json.dumps({"endpoint": "http://127.0.0.1:%s" % server.server_port, "token": token}) + ";\n"
            body += r'''
var api = new BridgeAPI(config);
var value = "\u4e2d\u6587\ud83d\ude42";
if (api.post("/v1/agent/poll", {value:value}).value !== value) { throw new Error("Unicode mismatch"); }
var rejected = false;
try { api.post("/v1/agent/poll", {redirect:true}); } catch (e) { rejected = true; }
if (!rejected) { throw new Error("Redirect accepted"); }
'''
            self.run_js(body)
            self.assertTrue(seen)
            self.assertTrue(all(path == "/v1/agent/poll" and auth == "Bearer " + token for path, auth, _ in seen))
            self.assertTrue(any(body.get("value") == "中文🙂" for _, _, body in seen))
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=3)
