"""Isolated real Go Bridge + Windows JScript/COM. Never accesses a real Xshell."""
import argparse
import base64
import hashlib
import hmac
import sys
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
from contextlib import contextmanager
from urllib import request, error

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "tests"))
from test_log_export import compile_native

@contextmanager
def retained_temp(artifacts):
    yield tempfile.mkdtemp(prefix="xsh-js-smoke-", dir=artifacts)


def run(binary):
    if os.name != "nt":
        raise SystemExit("Windows JScript/COM required")
    artifacts = ROOT / "dist/_test-artifacts/js-smoke"
    artifacts.mkdir(parents=True, exist_ok=True)
    with retained_temp(artifacts) as temp:
        root = Path(temp)
        subprocess.run([binary, "init", "--dir", temp], check=True, capture_output=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            endpoint = "http://127.0.0.1:%d" % sock.getsockname()[1]
        cfg = json.loads((root / "bridge.json").read_text())
        cfg["listen"] = endpoint.split("//")[1]
        (root / "bridge.json").write_text(json.dumps(cfg))
        agent_cfg = json.loads((root / "agent.json").read_text(encoding="utf-8"))
        agent_cfg.update(endpoint=endpoint, environment="isolated", binding_mode="manual_terminal", source_id="a" * 32)
        # Match native export: a SourceID must authenticate with its scoped agent token.
        source_id = agent_cfg["source_id"]
        proof = hmac.new(agent_cfg["token"].encode(), ("xshell-bridge-source-v1:" + source_id).encode(), hashlib.sha256).hexdigest()
        agent_cfg["token"] = "xsh-agent-v1." + source_id + "." + proof
        (root / "agent.json").write_text(json.dumps(agent_cfg), encoding="utf-8-sig")

        def api(path, body=None, role="client"):
            req = request.Request(endpoint + path, data=None if body is None else json.dumps(body).encode(),
                                  headers={"Authorization": "Bearer " + cfg["keys"][role], "Content-Type": "application/json"})
            with request.build_opener(request.ProxyHandler({})).open(req, timeout=5) as response:
                return json.load(response)

        engine = Path(os.environ["WINDIR"]) / "SysWOW64/cscript.exe"
        if not engine.exists():
            engine = Path(os.environ["WINDIR"]) / "System32/cscript.exe"
        compile_native(ROOT / "agent/LogCapture.cs", root / "XshLogCapture.exe", winexe=True)
        compile_native(ROOT / "tests/LogWriter.cs", root / "LogWriter.exe")
        helper = (root / "XshLogCapture.exe").read_bytes()
        bundle = dict(config=agent_cfg, helper_base64=base64.b64encode(helper).decode(), helper_size=len(helper), helper_sha256=hashlib.sha256(helper).hexdigest())
        source = "var XSH_REMOTE_BUNDLE=" + json.dumps(bundle) + ";\nvar WRITER=" + json.dumps(str(root / "LogWriter.exe")) + ";\n"
        source += "\n".join((ROOT / "agent" / name).read_text(encoding="utf-8") for name in ("remote_bootstrap.js", "log_parser.js", "xshell_agent.js"))
        source += "\n" + (ROOT / "tests/log_session_fixture.js").read_text(encoding="utf-8")
        fixture = r'''
var root=WScript.Arguments(0), job=null;
var f=makeLogFixture(function(){return job;},'hello \u4e2d\u6587\r\n',7),fake=f.xsh;
var boot=remoteBootstrap(fake,XSH_REMOTE_BUNDLE),agent=new Agent(fake,boot.config);
try {
WScript.Echo('REGISTERED');
var deadline = new Date().getTime()+10000, job=null;
while(new Date().getTime()<deadline){var response=agent.poll();if(response.job){job=response.job;break;}WScript.Sleep(50);}
if(!job){throw new Error('no approved job');}
agent.execute(job);
if(f.sends()!==1 || fake.Screen.Synchronous!==false){throw new Error('execution contract failed');}
if(agent.poll().job){throw new Error('job replayed');}
WScript.Echo('COMPLETED');
} finally { boot.close(); }
'''
        script = root / "fixture.js"
        script.write_text(source + "\n" + fixture, encoding="utf-16")
        server = subprocess.Popen([binary, "serve", "--dir", temp], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        agent = None
        try:
            for _ in range(100):
                try:
                    api("/v1/health")
                    break
                except error.URLError:
                    time.sleep(0.05)
            else:
                raise AssertionError("Bridge failed to start")
            (root / "local").mkdir()
            agent = subprocess.Popen([str(engine), "//nologo", str(script), temp], stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=dict(os.environ, LOCALAPPDATA=str(root / "local")))
            # Allow bounded initialization before measuring the approved command timeout.
            for _ in range(600):
                sessions = api("/v1/sessions")
                if sessions:
                    break
                if agent.poll() is not None:
                    raise AssertionError(agent.communicate())
                time.sleep(0.05)
            else:
                raise AssertionError("JScript registration failed")
            session = sessions[0]
            assert session["state"] == "pending"
            api("/v1/admin/activate", {"id": session["id"], "generation": session["generation"]}, "admin")
            job = api("/v1/jobs", {"session_id": session["id"], "generation": session["generation"],
                      "request_id": "js-smoke", "command": "printf hello; exit 7", "cwd": "/", "timeout_ms": 5000})
            time.sleep(0.15)
            assert api("/v1/job?id=" + job["id"])["status"] == "pending"
            api("/v1/admin/decide", {"id": job["id"], "allow": True}, "admin")
            out, err = agent.communicate(timeout=15)
            assert agent.returncode == 0 and not err and b"COMPLETED" in out, (out, err)
            final = api("/v1/job?id=" + job["id"])
            assert final["status"] == "completed" and final["exit_code"] == 7, final
            assert "hello 中文" in api("/v1/output?id=" + job["id"])["output"]
            print("PASS: JScript + native log helper + real WinHTTP + Go Bridge, pending/approval, Unicode output, exit code, no replay")
            print("NOT TESTED: real Xshell Screen/Session API or target commands")
        finally:
            if agent and agent.poll() is None:
                agent.terminate()
                agent.communicate(timeout=5)
            server.terminate()
            server.communicate(timeout=5)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True)
    run(str(Path(parser.parse_args().binary).resolve()))
