"""Actual Windows winexe pipes + file handles. Only unique synthetic fixtures."""
import ctypes
from ctypes import wintypes
import base64
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / 'dist/_test-artifacts/log-helper'


@unittest.skipUnless(os.name == 'nt', 'Windows handles required')
class NativeLogTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        ARTIFACTS.mkdir(parents=True, exist_ok=True)
        subprocess.run([str(Path(os.environ['WINDIR']) / 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'), '/nologo', '/optimize+', '/target:winexe', '/platform:anycpu', '/r:System.Web.Extensions.dll', '/out:' + str(ARTIFACTS / 'XshLogCapture.exe'), str(ROOT / 'agent/LogCapture.cs')], check=True, capture_output=True)

    def setUp(self):
        self.root = Path(tempfile.mkdtemp(prefix='native-', dir=ARTIFACTS))
        self.env = dict(os.environ, LOCALAPPDATA=str(self.root))
        private = self.root / 'XshellBridge-agent' / 'helper-test'
        private.mkdir(parents=True)
        self.exe = private / 'XshLogCapture.exe'
        shutil.copyfile(ARTIFACTS / 'XshLogCapture.exe', self.exe)
        self.processes = []
        self.seq = {}

    def tearDown(self):
        for p in self.processes:
            if p.poll() is None:
                p.kill()
            p.communicate(timeout=4)

    def start(self):
        p = subprocess.Popen([str(self.exe), '--stdio-v1'], env=self.env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NO_WINDOW)
        self.processes.append(p)
        self.seq[p] = 0
        return p

    def call(self, p, op, **data):
        self.seq[p] += 1
        p.stdin.write((json.dumps(dict(seq=self.seq[p], op=op, **data)) + '\r\n').encode('ascii'))
        p.stdin.flush()
        line = p.stdout.readline(24577)
        self.assertLessEqual(len(line), 24576)
        if not line:
            self.fail('helper closed stdout: ' + p.stderr.read().decode(errors='replace'))
        r = json.loads(line)
        self.assertEqual(r['seq'], self.seq[p])
        return r

    def prepared(self):
        p = self.start()
        r = self.call(p, 'prepare', nonce='fixture')
        self.assertTrue(r['ok'], r)
        self.assertNotIn('identity', r)
        path = Path(r['path'])
        self.assertTrue(path.is_relative_to(self.root))
        return p, path

    def create_and_bind(self, p, path):
        self.assertFalse(path.exists())
        self.assertTrue(self.call(p, 'prepared')['ok'])
        path.touch(exist_ok=False)
        result = self.call(p, 'bind')
        self.assertTrue(result['ok'], result)

    def test_prepare_absent_writer_create_new_and_bind(self):
        p, path = self.prepared()
        self.assertFalse(path.exists(), 'prepare must leave the Xshell destination absent')
        self.assertNotIn('identity', self.call(p, 'prepared'))
        # Models Xshell's avoid-overwrite choice: an existing target selects (2).
        actual = path if not path.exists() else path.with_name('capture(2).log')
        with open(actual, 'xb', buffering=0) as writer:
            self.assertEqual(actual, path)
            bound = self.call(p, 'bind')
            self.assertTrue(bound['ok'], bound)
            self.assertRegex(bound['identity'], r'^[0-9a-f]{24}$')
            writer.write(b'fixture')
            self.assertEqual(base64.b64decode(self.call(p, 'read')['data']), b'fixture')
        self.assertTrue(self.call(p, 'close', keep=False)['ok'])

    def test_read_before_bind_and_duplicate_bind_fail(self):
        p, path = self.prepared()
        self.assertFalse(self.call(p, 'read')['ok'])
        p.wait(timeout=3)
        p, path = self.prepared()
        self.create_and_bind(p, path)
        self.assertFalse(self.call(p, 'bind')['ok'])
        p.wait(timeout=3)

    def test_injected_existing_file_and_unsafe_bind_are_refused(self):
        p, path = self.prepared()
        path.write_bytes(b'injected')
        self.assertFalse(self.call(p, 'prepared')['ok'])
        p.wait(timeout=3)
        self.assertEqual(path.read_bytes(), b'injected')
        p, path = self.prepared()
        self.assertTrue(self.call(p, 'prepared')['ok'])
        foreign = self.root / 'foreign'
        foreign.write_bytes(b'foreign')
        os.link(foreign, path)
        result = self.call(p, 'bind')
        self.assertFalse(result['ok'])
        self.assertIn('hardlink', result['error'])
        p.wait(timeout=3)
        self.assertEqual(foreign.read_bytes(), b'foreign')
        import _winapi
        p, path = self.prepared()
        self.assertTrue(self.call(p, 'prepared')['ok'])
        target = self.root / 'junction-target'
        target.mkdir()
        _winapi.CreateJunction(str(target), str(path))
        result = self.call(p, 'bind')
        self.assertFalse(result['ok'])
        p.wait(timeout=3)

    def test_directory_lease_prevents_path_swap(self):
        p, path = self.prepared()
        try:
            path.parent.rename(path.parent.with_name('swapped'))
        except OSError:
            self.assertTrue(self.call(p, 'prepared')['ok'])
            self.assertTrue(self.call(p, 'close', keep=True)['ok'])
        else:
            # Some filesystems permit renaming a held directory; the lease must
            # still detect that its original path no longer names that directory.
            self.assertFalse(self.call(p, 'prepared')['ok'])
        p.wait(timeout=3)

    def test_encoding_stream_matrix_and_final_eof(self):
        samples = [('auto','utf-8-sig'), ('auto','utf-16'), ('auto','utf-16-be'),
                   ('utf-8','utf-8'), ('utf-16le','utf-16-le'), ('utf-16be','utf-16-be'),
                   ('gbk','gbk'), ('gb18030','gb18030')]
        for choice, codec in samples:
            with self.subTest(choice=choice, codec=codec):
                p = self.start()
                r = self.call(p, 'prepare', nonce='encoding', encoding=choice)
                self.assertTrue(r['ok'], r)
                path = Path(r['path'])
                self.create_and_bind(p, path)
                text = ('中文A' if codec == 'gbk' else '中文🙂A') * 1500
                raw = text.encode(codec)
                if choice == 'auto' and codec == 'utf-16-be': raw = b'\xfe\xff' + raw
                output = bytearray()
                with open(path, 'wb', buffering=0) as writer:
                    for i in range(0, len(raw), 997):
                        writer.write(raw[i:i+997])
                        r = self.call(p, 'read')
                        self.assertTrue(r['ok'], r)
                        output.extend(base64.b64decode(r['data']))
                    while True:
                        r = self.call(p, 'read', final=True)
                        self.assertTrue(r['ok'], r)
                        part = base64.b64decode(r['data']); self.assertLessEqual(len(part), 16384)
                        output.extend(part)
                        if r['eof'] and not part: break
                self.assertEqual(output.decode('utf-8'), text)
                self.assertTrue(self.call(p, 'close', keep=False)['ok']); p.wait(timeout=3)

    def test_encoding_incomplete_and_bom_conflict_fail_at_final(self):
        for choice, raw, immediate in [('auto', b'\xef\xbb', False), ('utf-16le', b'\xff\xfeA', False),
                                      ('utf-8', b'\xff\xfeA\x00', True), ('gbk', b'\x81', False),
                                      ('auto', b'\xff\xff', True), ('utf-16le', b'\x00\xd8', False), ('gb18030', b'\x81\x30\x81', False)]:
            with self.subTest(choice=choice, raw=raw):
                p = self.start(); r = self.call(p, 'prepare', nonce='encoding', encoding=choice)
                self.assertTrue(r['ok'], r); path = Path(r['path']); self.create_and_bind(p, path); path.write_bytes(raw)
                r = self.call(p, 'read')
                if immediate: self.assertFalse(r['ok'], r)
                else:
                    self.assertTrue(r['ok'], r)
                    r = self.call(p, 'read', final=True); self.assertFalse(r['ok'], r)
                self.assertTrue(self.call(p, 'owned')['ok'])
                self.assertTrue(self.call(p, 'close', keep=True)['ok']); p.wait(timeout=3)

    def test_normalized_output_limit_and_split_bom(self):
        p = self.start(); r = self.call(p, 'prepare', nonce='limit', encoding='auto'); path = Path(r['path'])
        self.create_and_bind(p, path)
        with open(path, 'wb', buffering=0) as writer:
            for piece in (b'\xff', b'\xfe', b'\x2d', b'\x4e'):
                writer.write(piece); r = self.call(p, 'read'); self.assertTrue(r['ok'], r)
            self.assertEqual(base64.b64decode(r['data']).decode(), '中')
            writer.write('中'.encode('utf-16le') * (3 * 1024 * 1024))
            while True:
                r = self.call(p, 'read', final=True)
                if not r['ok']: break
                self.assertLessEqual(len(base64.b64decode(r['data'])), 16384)
                if r['eof']: self.fail('normalized limit accepted')
            self.assertIn('normalized', r['error'])
            self.assertTrue(self.call(p, 'owned')['ok'])
            self.assertTrue(self.call(p, 'close', keep=True)['ok']); p.wait(timeout=3)

    def test_winexe_pipes_live_writer_bounded_reads_cleanup_and_isolation(self):
        p, path = self.prepared()
        other, other_path = self.prepared()
        self.assertNotEqual(path, other_path)
        self.create_and_bind(p, path)
        self.create_and_bind(other, other_path)
        # Python's Windows file writer shares read/write. Keep it open while reading.
        with open(path, 'wb', buffering=0) as writer:
            payload = ('中文🙂\n' * 5000).encode()
            writer.write(payload)
            self.assertTrue(self.call(p, 'check')['ok'])
            result = bytearray()
            while True:
                r = self.call(p, 'read')
                self.assertTrue(r['ok'], r)
                part = base64.b64decode(r['data'])
                self.assertLessEqual(len(part), 16384)
                result.extend(part)
                if r['eof']:
                    break
            self.assertEqual(result, payload)
            self.assertEqual(self.call(other, 'read')['data'], '')
            with self.assertRaises(OSError):
                path.unlink()
            with self.assertRaises(OSError):
                path.rename(path.with_suffix('.moved'))
            with self.assertRaises(OSError):
                path.parent.rename(path.parent.with_name('moved'))
        self.assertTrue(self.call(p, 'close', keep=False)['ok'])
        p.wait(timeout=3)
        self.assertEqual(p.returncode, 0)
        self.assertFalse(path.parent.exists())
        self.assertTrue(self.call(other, 'close', keep=True)['ok'])
        other.wait(timeout=3)
        self.assertTrue(other_path.exists())
        self.assertEqual(p.stderr.read(), b'')

    def test_truncation_and_size_limit_retain(self):
        for oversize in (False, True):
            p, path = self.prepared()
            self.create_and_bind(p, path)
            with open(path, 'r+b', buffering=0) as f:
                f.write(b'confirmed')
                self.assertTrue(self.call(p, 'read')['ok'])
                f.truncate(8 * 1024 * 1024 + 1 if oversize else 0)
                # Identity remains available for stopping the owned Xshell log.
                self.assertTrue(self.call(p, 'owned')['ok'])
                r = self.call(p, 'check')
                self.assertFalse(r['ok'])
                self.assertIn('limit' if oversize else 'truncated', r['error'])
                self.assertTrue(self.call(p, 'owned')['ok'])
                self.assertTrue(self.call(p, 'close', keep=True)['ok'])
            p.wait(timeout=3)
            self.assertTrue(path.exists())

    def test_writer_requiring_exclusive_read_fails_before_send_is_possible(self):
        p, path = self.prepared()
        self.create_and_bind(p, path)
        kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
        kernel.CreateFileW.restype = wintypes.HANDLE
        handle = kernel.CreateFileW(str(path), 0x40000000, 0, None, 3, 0, None)
        self.assertEqual(handle, wintypes.HANDLE(-1).value)
        self.assertEqual(ctypes.get_last_error(), 32)
        self.assertTrue(self.call(p, 'close', keep=False)['ok'])

    def test_bind_refuses_writer_that_disallows_read_sharing(self):
        p, path = self.prepared()
        self.assertTrue(self.call(p, 'prepared')['ok'])
        kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
        kernel.CreateFileW.restype = wintypes.HANDLE
        kernel.CloseHandle.argtypes = [wintypes.HANDLE]
        handle = kernel.CreateFileW(str(path), 0x40000000, 0, None, 1, 0, None)
        self.assertNotEqual(handle, wintypes.HANDLE(-1).value)
        try:
            result = self.call(p, 'bind')
            self.assertFalse(result['ok'])
            self.assertIn('cannot open expected log', result['error'])
            p.wait(timeout=3)
        finally:
            kernel.CloseHandle(handle)
        self.assertTrue(path.exists())

    def test_reparse_ancestor_and_bad_protocol(self):
        import _winapi
        real = self.root / 'real'
        real.mkdir()
        junction = self.root / 'alias'
        _winapi.CreateJunction(str(real), str(junction))
        self.env['LOCALAPPDATA'] = str(junction)
        p = self.start()
        r = self.call(p, 'prepare', nonce='fixture')
        self.assertFalse(r['ok'])
        self.assertIn('reparse', r['error'])
        p.wait(timeout=3)
        self.env['LOCALAPPDATA'] = str(self.root)
        for raw in (b'{"seq":7,"op":"prepare"}\n', b'x' * 1025 + b'\n'):
            p = self.start()
            out, err = p.communicate(raw, timeout=4)
            self.assertEqual(p.returncode, 1)
            self.assertFalse(json.loads(out)['ok'])
            self.assertEqual(err, b'')

    def test_idle_exit_releases_handles_retains_only_owned_file(self):
        p, path = self.prepared()
        self.create_and_bind(p, path)
        p.wait(timeout=19)
        self.assertEqual(p.returncode, 3)
        self.assertTrue(path.exists())
        path.unlink()  # No abandoned handle after idle timeout.

    def test_hardlink_and_sequence_changes_never_delete_foreign_files(self):
        p, path = self.prepared()
        self.create_and_bind(p, path)
        foreign = self.root / 'unrelated.log'
        foreign.write_bytes(b'unrelated fixture')
        alias = self.root / 'alias.log'
        os.link(path, alias)
        r = self.call(p, 'read')
        self.assertFalse(r['ok'])
        self.assertIn('hardlink', r['error'])
        self.assertFalse(self.call(p, 'close', keep=False)['ok'])
        p.wait(timeout=3)
        self.assertTrue(path.exists())
        self.assertEqual(foreign.read_bytes(), b'unrelated fixture')
        p, path = self.prepared()
        self.create_and_bind(p, path)
        p.stdin.write(b'{"seq":5,"op":"read"}\n')
        p.stdin.flush()
        reply = json.loads(p.stdout.readline(24577))
        self.assertFalse(reply['ok'])
        self.assertEqual(reply['seq'], 4)
        p.wait(timeout=3)
        self.assertEqual(p.returncode, 1)
        self.assertTrue(path.exists())
