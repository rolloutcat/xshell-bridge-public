"""Isolated binary/HTTP/CLI/MCP/agent integration; never opens Xshell or SSH."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import queue
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from urllib import request, error

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("xshell_agent", ROOT / "tests/fixtures/simulated_agent.py")
agent_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(agent_module)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class FakeScreen:
    """Synthetic Bridge protocol transport. Not the production native log reader."""
    Synchronous = False

    def __init__(self, shell, session):
        self.shell, self.session, self.sent = shell, session, 0
        self.ready = False

    def IsAltScreen(self):
        return False

    def Send(self, command):
        assert command != "\x03", "unexpected interrupt"
        self.sent += 1
        result = subprocess.run([self.shell, "-c", command.rstrip("\r")],
                                capture_output=True, timeout=5)
        self.session.synthetic_log = command.encode() + b"\n" + result.stdout + result.stderr
        self.ready = True

    def WaitForStrings(self, patterns, timeout):
        if self.ready:
            self.ready = False
            return 1
        time.sleep(timeout / 1000)
        return 0


class FakeSession:
    Connected = True
    Path = "isolated-test"
    SessionName = "isolated-test"
    TabText = "isolated-test"
    RemoteAddress = "127.0.0.1"
    RemotePort = 22
    UserName = "fixture"
    @staticmethod
    def Sleep(ms):
        time.sleep(ms / 1000)


def run(binary):
    if os.name == "nt":
        shell = str(Path(shutil.which("git")).parent.parent / "bin/bash.exe")
    else:
        shell = shutil.which("bash")
    if not shell or not Path(shell).exists():
        raise RuntimeError("isolated shell fixture requires Git Bash/ bash")
    with tempfile.TemporaryDirectory(prefix="xshell-bridge-test-") as tmp:
        root = Path(tmp)
        subprocess.run([binary, "init", "--dir", tmp], check=True, capture_output=True)
        endpoint = "http://127.0.0.1:%d" % free_port()
        for name in ("bridge", "client", "admin", "agent"):
            path = root / (name + ".json")
            cfg = json.loads(path.read_text(encoding="utf-8"))
            if name == "bridge":
                cfg["listen"] = endpoint.split("//")[1]
            else:
                cfg["endpoint"] = endpoint
            path.write_text(json.dumps(cfg), encoding="utf-8")
        configs = {n: json.loads((root / (n + ".json")).read_text(encoding="utf-8")) for n in ("client", "admin", "agent")}

        def api(path, body=None, role="client"):
            req = request.Request(endpoint + path,
                data=None if body is None else json.dumps(body).encode(),
                headers={"Authorization": "Bearer " + configs[role]["token"], "Content-Type": "application/json"})
            with request.build_opener(request.ProxyHandler({})).open(req, timeout=5) as r:
                return json.load(r)

        server = subprocess.Popen([binary, "serve", "--dir", tmp], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        mcp = None
        try:
            for _ in range(100):
                try:
                    api("/v1/health")
                    break
                except error.URLError:
                    if server.poll() is not None:
                        raise RuntimeError(server.stderr.read().decode())
                    time.sleep(0.05)
            else:
                raise RuntimeError("bridge did not start")
            assert subprocess.run([binary, "console", "--dir", tmp], capture_output=True).returncode != 0
            duplicate_server = subprocess.run([binary, "serve", "--dir", tmp], capture_output=True, timeout=5)
            assert duplicate_server.returncode != 0
            fake = type("FakeXsh", (), {})()
            fake.Session = FakeSession()
            fake.Screen = FakeScreen(shell, fake.Session)
            cfg = dict(configs["agent"], name="isolated demo", environment="offline fixture")
            a = agent_module.Agent(fake, cfg)
            session = a.session
            api("/v1/admin/activate", {"id": session["id"], "generation": session["generation"]}, "admin")

            mcp = subprocess.Popen([binary, "--config", str(root / "client.json"), "mcp"],
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, encoding="utf-8", bufsize=1)
            messages = queue.Queue()
            def read_messages():
                for line in mcp.stdout:
                    messages.put(json.loads(line))
            threading.Thread(target=read_messages, daemon=True).start()
            counter = 0
            def rpc(method, params):
                nonlocal counter
                counter += 1
                mcp.stdin.write(json.dumps({"jsonrpc": "2.0", "id": counter, "method": method, "params": params}) + "\n")
                mcp.stdin.flush()
                while True:
                    reply = messages.get(timeout=10)
                    if reply.get("id") == counter:
                        assert "error" not in reply, reply
                        return reply["result"]
            version = subprocess.run([binary, "version"], check=True, capture_output=True,
                                     text=True, encoding="utf-8", timeout=5).stdout.strip()
            assert version == (ROOT / "VERSION").read_text(encoding="utf-8").strip()
            info = rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {},
                       "clientInfo": {"name": "isolated-smoke", "version": "1"}})["serverInfo"]
            assert info["name"] == "xshell-bridge" and info["version"] == version
            mcp.stdin.write('{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
            mcp.stdin.flush()
            tools = rpc("tools/list", {})["tools"]
            assert {t["name"] for t in tools} == {"list_sessions", "resolve_session", "explain_command", "exec", "get_job", "wait_job", "read_output", "interrupt_job", "detach_session"}
            explain_tool = next(t for t in tools if t['name'] == 'explain_command')
            assert explain_tool['annotations']['readOnlyHint'] is True
            required = set(explain_tool['inputSchema']['required'])
            assert required == {'session_id', 'generation', 'command'}, explain_tool['inputSchema']
            assert not any('batch' in t['name'] or 'approve' in t['name'] for t in tools)
            assert next(t for t in tools if t["name"] == "wait_job")["annotations"]["readOnlyHint"] is True
            def wait_tool(job_id, ms=250, failed=False):
                result = rpc("tools/call", {"name": "wait_job", "arguments": {"id": job_id, "wait_ms": ms}})
                assert bool(result.get("isError")) == failed, result
                return result if failed else json.loads(result["content"][0]["text"])
            def resolve_tool(name, fuzzy=False):
                result=rpc("tools/call", {"name":"resolve_session", "arguments":{"name":name,"fuzzy":fuzzy}})
                assert not result.get("isError"), result
                return json.loads(result["content"][0]["text"])
            exact=resolve_tool(session["name"])
            assert exact["status"]=="matched" and exact["candidates"][0]["id"]==session["id"]
            api("/v1/admin/session-label", {"id":session["id"],"generation":session["generation"],"name":"Renamed smoke target","environment":"isolated"}, "admin")
            assert resolve_tool(session["name"])["status"]=="not_found"
            assert resolve_tool("Renamed smoke target")["status"]=="matched"
            suggestions=resolve_tool("smoke target",True)
            assert suggestions["status"]=="suggestions" and suggestions["requires_selection"]
            submit = {"session_id": session["id"], "generation": session["generation"], "request_id": "smoke-request",
                      "command": "printf 'hello 中文'; exit 7", "cwd": "", "timeout_ms": 10000}
            result = rpc("tools/call", {"name": "exec", "arguments": submit})
            assert not result.get("isError"), result
            job = json.loads(result["content"][0]["text"])
            assert job["status"] == "pending"
            before_wait = api("/v1/job?id=" + job["id"])
            minimal_explain = {k: submit[k] for k in ('session_id', 'generation', 'command')}
            explained = rpc('tools/call', {'name': 'explain_command', 'arguments': minimal_explain})
            assert not explained.get('isError'), explained
            assert json.loads(explained['content'][0]['text'])['decision'] == 'manual'
            assert api('/v1/job?id=' + job['id']) == before_wait
            assert fake.Screen.sent == 0
            assert a.poll().get('job') is None
            scope = {k: submit[k] for k in ('session_id', 'generation', 'command', 'cwd', 'timeout_ms')}
            explained = rpc('tools/call', {'name': 'explain_command', 'arguments': scope})
            assert not explained.get('isError'), explained
            assert json.loads(explained['content'][0]['text'])['decision'] == 'manual'
            cli_explain = subprocess.run([binary, '--config', str(root / 'client.json'), 'explain', session['id'], '--generation', session['generation'], '--timeout-ms', '10000', '--', submit['command']], capture_output=True, text=True, encoding='utf-8', timeout=5)
            assert cli_explain.returncode == 0 and json.loads(cli_explain.stdout)['decision'] == 'manual'
            assert api('/v1/job?id=' + job['id']) == before_wait
            try:
                api('/v1/admin/decide-batch', {'allow': True, 'items': []})
                raise AssertionError('client self-approved batch')
            except error.HTTPError as exc:
                assert exc.code == 403
            for bound in (0, -1, 30001):
                wait_tool(job["id"], bound, failed=True)
            waited = wait_tool(job["id"])
            assert waited["timed_out"] and not waited["terminal"] and waited["job"]["status"] == "pending", waited
            assert api("/v1/job?id=" + job["id"]) == before_wait
            assert fake.Screen.sent == 0
            assert a.poll().get("job") is None
            try:
                api("/v1/admin/decide", {"id": job["id"], "allow": True})
                raise AssertionError("client self-approved")
            except error.HTTPError as exc:
                assert exc.code == 403
            api("/v1/admin/decide", {"id": job["id"], "allow": True}, "admin")
            dispatch = a.poll()["job"]
            a.execute(dispatch)
            final = api("/v1/job?id=" + job["id"])
            assert final["status"] == "completed" and final["exit_code"] == 7, final
            waited = wait_tool(job["id"])
            assert waited["terminal"] and not waited["timed_out"], waited
            assert waited["job"]["status"] == "completed" and waited["job"]["exit_code"] == 7, waited
            assert not waited["job"].get("output"), waited
            output = api("/v1/output?id=" + job["id"])
            assert "hello 中文" in output["output"], output
            duplicate = api("/v1/jobs", submit)
            assert duplicate["id"] == job["id"] and duplicate["status"] == "completed"
            assert a.poll().get("job") is None and fake.Screen.sent == 1
            cli = subprocess.run([binary, "--config", str(root / "client.json"), "job", "get", job["id"]],
                                 capture_output=True, text=True, encoding="utf-8", timeout=5)
            assert cli.returncode == 0 and json.loads(cli.stdout)["exit_code"] == 7
            cli = subprocess.run([binary, "--config", str(root / "client.json"), "job", "wait", job["id"], "--wait-ms", "250"],
                                 capture_output=True, text=True, encoding="utf-8", timeout=5)
            waited = json.loads(cli.stdout)
            assert cli.returncode == 0 and waited["terminal"] and not waited["timed_out"], (cli.returncode, waited)
            assert waited["job"]["exit_code"] == 7 and not waited["job"].get("output"), waited
            submit["request_id"] = "denied-request"
            denied = api("/v1/jobs", submit)
            api("/v1/admin/decide", {"id": denied["id"], "allow": False}, "admin")
            waited = wait_tool(denied["id"])
            assert waited["terminal"] and not waited["timed_out"] and waited["job"]["status"] == "denied", waited
            wait_tool("0" * 48, failed=True)
            assert fake.Screen.sent == 1
            assert a.poll().get("job") is None
            # Literal command has its own scope; the quoted exit-7 routing test stays unchanged.
            literal_scope = dict(scope, command='printf fixture')
            literal = api('/v1/jobs', dict(literal_scope, request_id='literal-pending'))
            assert literal['status'] == 'pending'
            rule = api('/v1/admin/rules', dict(name='literal printf fixture', session_id=session['id'], generation=session['generation'], program='printf', argument_patterns=['fixture'], cwd='', max_timeout_ms=10000, minutes=30), 'admin')
            assert api('/v1/explain', literal_scope)['decision'] == 'rule'
            assert api('/v1/job?id=' + literal['id'])['status'] == 'pending'
            matched = api('/v1/jobs', dict(literal_scope, request_id='literal-match'))
            assert matched['status'] == 'queued' and matched['rule_id'] == rule['id']
            api('/v1/admin/revoke-rule', {'id': rule['id']}, 'admin')
            api('/v1/interrupt', {'id': literal['id']})
            api('/v1/interrupt', {'id': matched['id']})
            assert fake.Screen.sent == 1
            api("/v1/detach", {"id": session["id"], "generation": session["generation"]})
            try:
                a.poll()
                raise AssertionError("detached agent could poll")
            except agent_module.CaptureError:
                pass
            print("PASS: isolated binary + HTTP role checks + CLI + official MCP SDK + simulated Xshell APIs + real local Shell framing")
            print("NOT TESTED: real Xshell, VM, VPN, SSH tunnel, target servers")
        finally:
            if mcp:
                mcp.terminate()
                mcp.communicate(timeout=5)
            server.terminate()
            _, server_errors = server.communicate(timeout=5)
            if sys.exc_info()[0] is not None:
                print(server_errors.decode("utf-8", errors="replace"), file=sys.stderr)


if __name__ == "__main__":
    p = argparse.ArgumentParser()
    p.add_argument("--binary", required=True)
    run(str(Path(p.parse_args().binary).resolve()))
