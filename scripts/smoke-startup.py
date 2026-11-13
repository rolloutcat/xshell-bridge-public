"""Windows launcher regression: real CLI/PS5.1, isolated fixtures, no Xshell.

The job object owns every fixture child, including detached launcher children.
Cleanup terminates and drains that job, never a PID read from a lock.
"""
import argparse
import base64
import ctypes
from ctypes import wintypes
import json
import os
from pathlib import Path
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import time
from urllib import request


def require(value, message):
    if not value:
        raise AssertionError(message)


class Children:
    def __init__(self):
        self.processes = []
        self.handle = None
        self.api = ctypes.WinDLL('kernel32', use_last_error=True)
        self.api.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
        self.api.CreateJobObjectW.restype = wintypes.HANDLE
        self.api.SetInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
        self.api.SetInformationJobObject.restype = wintypes.BOOL
        self.api.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
        self.api.AssignProcessToJobObject.restype = wintypes.BOOL
        self.api.TerminateJobObject.argtypes = [wintypes.HANDLE, wintypes.UINT]
        self.api.TerminateJobObject.restype = wintypes.BOOL
        self.api.QueryInformationJobObject.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.POINTER(wintypes.DWORD)]
        self.api.QueryInformationJobObject.restype = wintypes.BOOL
        self.api.CloseHandle.argtypes = [wintypes.HANDLE]
        self.api.CloseHandle.restype = wintypes.BOOL
        self.handle = self.api.CreateJobObjectW(None, None)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        # JOBOBJECT_EXTENDED_LIMIT_INFORMATION, Windows x64 ABI.
        limits = ctypes.create_string_buffer(144)
        struct.pack_into('<I', limits, 16, 0x2000)  # KILL_ON_JOB_CLOSE
        if not self.api.SetInformationJobObject(self.handle, 9, limits, len(limits)):
            error = ctypes.get_last_error()
            self.close()
            raise ctypes.WinError(error)

    def add(self, process):
        self.processes.append(process)
        if not self.api.AssignProcessToJobObject(self.handle, int(process._handle)):
            error = ctypes.get_last_error()
            # This is our direct, still-gated fixture, never a lock-file PID.
            process.kill()
            process.wait(timeout=10)
            raise ctypes.WinError(error)

    def close(self):
        error = None
        if self.handle:
            try:
                if not self.api.TerminateJobObject(self.handle, 1):
                    raise ctypes.WinError(ctypes.get_last_error())
                deadline = time.monotonic() + 10
                # JOBOBJECT_BASIC_ACCOUNTING_INFORMATION is 48 bytes on
                # Windows x64; its DWORD ActiveProcesses is at offset 40.
                accounting = ctypes.create_string_buffer(48)
                while True:
                    if not self.api.QueryInformationJobObject(
                            self.handle, 1, accounting, len(accounting), None):
                        raise ctypes.WinError(ctypes.get_last_error())
                    if struct.unpack_from('<I', accounting, 40)[0] == 0:
                        break
                    if time.monotonic() >= deadline:
                        raise TimeoutError('fixture job did not drain within 10 seconds')
                    time.sleep(.05)
            except Exception as exc:
                error = exc
            finally:
                # Keep the handle open throughout termination and accounting:
                # closing it alone does not synchronously release executables.
                if not self.api.CloseHandle(self.handle) and error is None:
                    error = ctypes.WinError(ctypes.get_last_error())
                self.handle = None
        for process in self.processes:
            try:
                if process.stdin is not None:
                    try:
                        process.stdin.close()
                    except BrokenPipeError:
                        pass
                    finally:
                        process.stdin = None
                process.wait(timeout=10)
            except Exception as exc:
                if error is None:
                    error = exc
        if error is not None:
            raise error


def psquote(value):
    return "'" + str(value).replace("'", "''") + "'"


def launch(children, powershell, bundle, root, mode, env):
    # Wait before invoking anything: assignment to our job must precede spawning
    # the real Bridge or the fake desktop. Use Windows PowerShell, not pwsh.
    command = (
        '[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); '
        '$null = [Console]::ReadLine(); '
        "$ErrorActionPreference = 'Stop'; try { & "
        + psquote(bundle / 'scripts/start-desktop.ps1')
        + ' -BundleRoot ' + psquote(bundle)
        + ' -DataRoot ' + psquote(root)
        + '; exit 0 } catch { [Console]::Error.WriteLine($_.ToString()); exit 1 }'
    )
    encoded = base64.b64encode(command.encode('utf-16le')).decode('ascii')
    root.mkdir(parents=True, exist_ok=True)
    # Descendants may inherit these handles. Regular files let us read the
    # launcher's output without waiting for a surviving Bridge to close a pipe.
    with tempfile.NamedTemporaryFile(mode='w+b', prefix='startup-stdout-', suffix='.log', dir=root, delete=False) as out_file, tempfile.NamedTemporaryFile(mode='w+b', prefix='startup-stderr-', suffix='.log', dir=root, delete=False) as err_file:
        process = subprocess.Popen(
            [str(powershell), '-NoLogo', '-NoProfile', '-NonInteractive',
             '-ExecutionPolicy', 'Bypass', '-EncodedCommand', encoded],
            stdin=subprocess.PIPE, stdout=out_file, stderr=err_file,
            env=env,
        )
        children.add(process)
        process.stdin.write(b'go\n')
        process.stdin.flush()
        process.stdin.close()
        process.stdin = None
        process.wait(timeout=30)
        out_file.flush()
        err_file.flush()
        out_file.seek(0)
        err_file.seek(0)
        text = (out_file.read() + err_file.read()).decode('utf-8', errors='strict')
    return process.returncode, text


def wait_for(predicate, description):
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(.05)
    raise AssertionError(description)


def run(binary):
    source = Path(__file__).resolve().parents[1]
    powershell = Path(os.environ['SystemRoot']) / 'System32/WindowsPowerShell/v1.0/powershell.exe'
    compiler = Path(os.environ['SystemRoot']) / 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'
    require(compiler.is_file(), '.NET Framework x64 C# compiler required')
    with tempfile.TemporaryDirectory(prefix='xsh-startup-') as temp:
        base = Path(temp)
        bundle = base / 'bundle'
        (bundle / 'scripts').mkdir(parents=True)
        shutil.copy2(binary, bundle / 'xshctl.exe')
        shutil.copy2(source / 'scripts/start-desktop.ps1', bundle / 'scripts/start-desktop.ps1')
        require((bundle / 'scripts/start-desktop.ps1').read_bytes().startswith(b'\xef\xbb\xbf'), 'launcher must have UTF-8 BOM for PS5.1')
        for name in ('xshell_agent.js', 'remote_bootstrap.js', 'log_parser.js'):
            shutil.copy2(source / 'agent' / name, bundle / name)
        subprocess.run([str(compiler), '/nologo', '/target:winexe', '/r:System.Web.Extensions.dll',
                        '/out:' + str(bundle / 'XshLogCapture.exe'), str(source / 'agent/LogCapture.cs')],
                       check=True, capture_output=True, timeout=30)
        marker = base / 'desktop-count.txt'
        cs = base / 'FakeDesktop.cs'
        cs.write_text('using System; using System.IO; class FakeDesktop { static void Main(string[] args) { File.AppendAllText(Environment.GetEnvironmentVariable("XSH_STARTUP_MARKER"), String.Join(" ", args) + Environment.NewLine); } }', encoding='utf-8-sig')
        subprocess.run([str(compiler), '/nologo', '/target:winexe', '/out:' + str(bundle / 'xshdesk.exe'), str(cs)], check=True, capture_output=True, timeout=30)
        env = dict(os.environ, XSH_STARTUP_MARKER=str(marker))
        # Defensive environment isolation too; all actual runtime paths are explicit.
        env['LOCALAPPDATA'] = str(base / 'localappdata')
        env['APPDATA'] = str(base / 'appdata')
        children = Children()
        try:
            for mode in ('Local',):
                root = base / mode
                runtime = root / 'runtime'
                subprocess.run([str(bundle / 'xshctl.exe'), 'init', '--dir', str(runtime)], check=True, capture_output=True, timeout=20, env=env)
                with socket.socket() as sock:
                    sock.bind(('127.0.0.1', 0))
                    port = sock.getsockname()[1]
                endpoint = f'http://127.0.0.1:{port}'
                bridge = runtime / 'bridge.json'
                cfg = json.loads(bridge.read_text(encoding='utf-8'))
                cfg['listen'] = f'127.0.0.1:{port}'
                bridge.write_text(json.dumps(cfg), encoding='utf-8')
                for role in ('admin', 'client', 'agent'):
                    path = runtime / (role + '.json')
                    data = json.loads(path.read_text(encoding='utf-8'))
                    data['endpoint'] = endpoint
                    path.write_text(json.dumps(data), encoding='utf-8')
                opener = request.build_opener(request.ProxyHandler({}))

                def api(path, body=None, role='client'):
                    req = request.Request(endpoint + path, data=None if body is None else json.dumps(body).encode(), headers={'Authorization': 'Bearer ' + cfg['keys'][role], 'Content-Type': 'application/json'})
                    with opener.open(req, timeout=5) as response:
                        return json.load(response)

                def desktop_count():
                    return len(marker.read_text().splitlines()) if marker.exists() else 0

                lock = runtime / 'bridge.lock'
                # Gate this short-lived fixture too: it must still be alive
                # when assigned, and cannot exit until it belongs to our job.
                exited = subprocess.Popen(
                    [sys.executable, '-c', 'import sys; sys.stdin.readline()'],
                    stdin=subprocess.PIPE, env=env,
                )
                children.add(exited)
                exited.stdin.write(b'\n')
                exited.stdin.flush()
                exited.stdin.close()
                exited.stdin = None
                require(exited.wait(timeout=10) == 0, 'exited PID fixture failed')
                stale = (str(exited.pid) + '\n').encode()
                lock.write_bytes(stale)
                count = desktop_count()
                code, text = launch(children, powershell, bundle, root, mode, env)
                require(code == 0, text)
                api('/v1/health')
                archives = list(runtime.glob('bridge.lock.stale-*'))
                require(len(archives) == 1 and archives[0].read_bytes() == stale, 'stale evidence not archived intact')
                owner = lock.read_bytes()
                require(int(owner) != exited.pid, 'old PID retained')
                wait_for(lambda: desktop_count() == count + 1, 'fake desktop not launched')
                code, text = launch(children, powershell, bundle, root, mode, env)
                require(code == 0 and '复用当前 Bridge' in text, text)
                require(lock.read_bytes() == owner, 'healthy reuse changed owner')
                wait_for(lambda: desktop_count() == count + 2, 'reuse did not open fake desktop')

                # The occupied listener is a real test-created Bridge, but the
                # launcher now has a wrong client credential. Never touch its PID.
                client = runtime / 'client.json'
                original = client.read_bytes()
                require(cfg['keys']['client'] in original.decode('utf-8'), 'client credential not found')
                client.write_bytes(original.replace(cfg['keys']['client'].encode(), b'wrong-fixture-credential'))
                code, text = launch(children, powershell, bundle, root, mode, env)
                require(code != 0 and '端口已被占用' in text and '认证健康检查未通过' in text, text)
                require(lock.read_bytes() == owner and desktop_count() == count + 2, 'conflict changed owner or launched desktop')
                api('/v1/health')
                client.write_bytes(original)
                api('/v1/admin/shutdown', {}, 'admin')
                wait_for(lambda: not lock.exists(), 'authenticated shutdown retained lock')

                for contents, expected in ((str(os.getpid()).encode() + b'\n', '原始 PID ' + str(os.getpid())), (b'not-a-pid\n', '格式异常')):
                    lock.write_bytes(contents)
                    code, text = launch(children, powershell, bundle, root, mode, env)
                    require(code != 0 and expected in text, text)
                    require(lock.read_bytes() == contents, 'refusal modified legacy marker')
                    require(desktop_count() == count + 2, 'failed startup launched desktop')
                    with socket.socket() as probe:
                        probe.settimeout(1)
                        require(probe.connect_ex(('127.0.0.1', port)) != 0, 'refused startup left listener')
                    lock.unlink()  # Only the exact fixture marker asserted above.
                print('PASS:', mode, 'PS5.1 real launcher: recovery, reuse, conflict, live/malformed refusal, authenticated shutdown')
        finally:
            # Includes a Bridge started by a launcher that timed out before its
            # marker was observed. Drain the job before temporary-file cleanup.
            # Job membership, not an untrusted PID, is proof of ownership.
            children.close()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    args = parser.parse_args()
    if sys.platform != 'win32':
        parser.exit(0, 'SKIP: startup smoke requires Windows\n')
    require(ctypes.sizeof(ctypes.c_void_p) == 8, 'Windows x64 Python required')
    run(Path(args.binary).resolve(strict=True))
