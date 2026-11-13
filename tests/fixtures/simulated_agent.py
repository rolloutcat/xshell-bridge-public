# -*- coding: utf-8 -*-
"""Synthetic Bridge protocol fixture only, NOT production capture or Xshell evidence."""
import json
import os
import re
import time
import uuid


class CaptureError(Exception):
    pass


def quote(value):
    return "'" + value.replace("'", "'\"'\"'") + "'"


def command_text(command, cwd, nonce):
    # Split markers so echoed command text cannot be mistaken for execution.
    script = "printf '\\n%s%s\\n' '__XSH_BEGIN_' " + quote(nonce) + "\n(\n"
    if cwd:
        script += "cd -- " + quote(cwd) + " || exit 125\n"
    script += command + "\n) < /dev/null\n__xsh_rc=$?\n"
    script += "printf '\\n%s%s:%s\\n' '__XSH_END_' " + quote(nonce) + ' "$__xsh_rc"\n'
    script += "printf '%s%s\\n' '__XSH_DONE_' " + quote(nonce) + "\n"
    return "sh -c " + quote(script) + "\r"


class Frames:
    def __init__(self, nonce):
        self.begin = "__XSH_BEGIN_" + nonce
        self.end = "__XSH_END_" + nonce + ":"
        self.started = False

    def feed(self, line):
        if not self.started:
            if line == self.begin:
                self.started = True
            return "", None
        if line.startswith(self.end):
            value = line[len(self.end):]
            if not re.match(r"^[0-9]{1,3}$", value) or int(value) > 255:
                raise CaptureError("invalid completion marker")
            return "", int(value)
        return line + "\n", None


def chunks(text):
    # 4000 Unicode code points fit the API's 32768-byte event cap even for emoji.
    for start in range(0, len(text), 4000):
        yield text[start:start + 4000]


class API:
    def __init__(self, config):
        try:
            from urllib import error, parse, request
        except ImportError:
            raise CaptureError("Xshell embedded Python lacks network modules; run xshell_agent.js from the same runtime folder instead")
        class NoRedirect(request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):
                return None
        self.request = request
        self.error = error
        self.endpoint = config["endpoint"].rstrip("/")
        self.token = config["token"]
        u = parse.urlsplit(self.endpoint)
        if (u.scheme != "http" or u.hostname not in ("127.0.0.1", "::1")
                or u.username or u.password or u.path or u.query or u.fragment):
            raise ValueError("agent endpoint must be VM numeric loopback HTTP")
        if len(self.token) < 32:
            raise ValueError("invalid agent token")
        self.opener = request.build_opener(request.ProxyHandler({}), NoRedirect())

    def post(self, path, data):
        req = self.request.Request(self.endpoint + path,
                              data=json.dumps(data, ensure_ascii=False).encode("utf-8"),
                              headers={"Content-Type": "application/json", "Authorization": "Bearer " + self.token})
        try:
            with self.opener.open(req, timeout=5) as response:
                return json.loads(response.read(2 * 1024 * 1024).decode("utf-8"))
        except self.error.HTTPError as exc:
            raise CaptureError("bridge rejected request (HTTP %s); reattach may be required" % exc.code)
        except self.error.URLError:
            raise CaptureError("bridge unreachable; task will not be replayed")


def fingerprint(xsh):
    if not xsh.Session.Connected:
        raise CaptureError("Xshell disconnected")
    values = {name: str(getattr(xsh.Session, name)) for name in
              ("Path", "SessionName", "TabText", "RemoteAddress", "RemotePort", "UserName")}
    # These identify the Xshell endpoint, not an independently verified final hop.
    return json.dumps(values, sort_keys=True, ensure_ascii=False, separators=(",", ":"))


class Agent:
    def __init__(self, xsh, config):
        if not config.get("name") or not config.get("environment") or "请填写" in config["name"] + config["environment"]:
            raise CaptureError("set actual target name and target/environment in agent.json")
        self.xsh = xsh
        self.api = API(config)
        self.bound = fingerprint(xsh)
        if xsh.Screen.IsAltScreen():
            raise CaptureError("leave full-screen applications before attaching")
        self.session = self.api.post("/v1/agent/register", {
            "instance": uuid.uuid4().hex, "name": config["name"],
            "environment": config["environment"], "fingerprint": self.bound})

    def check(self):
        current = fingerprint(self.xsh)
        if current != self.bound:
            # Notify Bridge of a changed target, but never send anything to it.
            self.api.post("/v1/agent/poll", {"id": self.session["id"],
                          "generation": self.session["generation"], "fingerprint": current})
            raise CaptureError("terminal fingerprint changed")
        return current

    def poll(self):
        return self.api.post("/v1/agent/poll", {"id": self.session["id"],
                             "generation": self.session["generation"], "fingerprint": self.check()})

    def execute(self, job):
        sequence = [0]

        def event(status, output="", code=None, reason=""):
            sequence[0] += 1
            return self.api.post("/v1/agent/event", {
                "session_id": self.session["id"], "generation": self.session["generation"],
                "fingerprint": self.check(), "job_id": job["id"], "seq": sequence[0],
                "output": output, "status": status, "exit_code": code, "reason": reason})

        screen = self.xsh.Screen
        previous_sync = screen.Synchronous
        try:
            self.check()
            if screen.IsAltScreen():
                raise CaptureError("terminal entered a full-screen application")
            nonce = job["id"]
            frames = Frames(nonce)
            screen.Synchronous = True
            event("running")
            self.check()
            screen.Send(command_text(job["command"], job.get("cwd", ""), nonce))
            deadline = time.monotonic() + job["timeout_ms"] / 1000.0
            last_contact = time.monotonic()
            pending = ""
            while True:
                self.check()
                if screen.IsAltScreen():
                    raise CaptureError("terminal entered a full-screen application")
                if time.monotonic() > deadline:
                    # Do not send extra input into an unknown terminal state.
                    raise CaptureError("deadline exceeded; remote command may still run")
                found = screen.WaitForStrings(["__XSH_DONE_" + nonce], 200)
                if found == 1:
                    line = self.xsh.Session.synthetic_log.decode("utf-8", errors="strict")
                    # Wrapped rows may contain several visual lines.
                    for visual in line.split("\n"):
                        output, code = frames.feed(visual)
                        pending += output
                        if code is not None:
                            for part in chunks(pending):
                                event("running", part)
                            event("completed", code=code)
                            return
                now = time.monotonic()
                if len(pending) >= 4000 or now - last_contact >= 0.5:
                    for part in chunks(pending):
                        event("running", part)
                    pending = ""
                    response = self.poll()
                    if response.get("interrupt"):
                        self.check()
                        screen.Send("\x03")
                        raise CaptureError("interrupt sent; termination is not confirmed")
                    last_contact = now
        except Exception as exc:
            # Once dispatch has occurred, failure is ambiguous. Never retry the command.
            try:
                event("unknown", reason=str(exc)[:300])
            except Exception:
                pass
            raise
        finally:
            screen.Synchronous = previous_sync

    def run(self):
        while True:
            result = self.poll()
            if result.get("job"):
                self.execute(result["job"])
            self.xsh.Session.Sleep(250)
