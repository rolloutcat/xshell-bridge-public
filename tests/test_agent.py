import importlib.util
from pathlib import Path
import unittest
from unittest import mock
import builtins

spec = importlib.util.spec_from_file_location("xshell_agent", Path(__file__).parents[1] / "tests" / "fixtures" / "simulated_agent.py")
agent = importlib.util.module_from_spec(spec)
spec.loader.exec_module(agent)


class FramingTests(unittest.TestCase):
    def test_echo_is_not_a_start_marker(self):
        nonce = "a" * 48
        wrapper = agent.command_text("printf 'hello'", "/tmp/a'b", nonce)
        self.assertNotIn("__XSH_BEGIN_" + nonce, wrapper)
        self.assertNotIn("__XSH_END_" + nonce, wrapper)
        parser = agent.Frames(nonce)
        self.assertEqual(parser.feed(wrapper), ("", None))
        self.assertEqual(parser.feed("__XSH_BEGIN_" + nonce), ("", None))
        self.assertEqual(parser.feed("中文结果"), ("中文结果\n", None))
        self.assertEqual(parser.feed("__XSH_END_" + nonce + ":7"), ("", 7))

    def test_wrong_marker_and_invalid_exit(self):
        p = agent.Frames("abc")
        p.feed("__XSH_BEGIN_abc")
        self.assertEqual(p.feed("__XSH_END_other:0"), ("__XSH_END_other:0\n", None))
        with self.assertRaises(agent.CaptureError):
            p.feed("__XSH_END_abc:999")

    def test_repeated_start_does_not_drop_output(self):
        p = agent.Frames("abc")
        p.feed("__XSH_BEGIN_abc")
        self.assertEqual(p.feed("__XSH_BEGIN_abc"), ("__XSH_BEGIN_abc\n", None))

    def test_utf8_chunks_roundtrip(self):
        text = "中文🙂" * 10000
        chunks = list(agent.chunks(text))
        self.assertEqual("".join(chunks), text)
        self.assertTrue(all(len(x.encode("utf-8")) <= 16000 for x in chunks))


class EmbeddedRuntimeTests(unittest.TestCase):
    def test_missing_network_module_gives_jscript_guidance(self):
        original = builtins.__import__
        def without_network(name, *args, **kwargs):
            if name == "urllib":
                raise ModuleNotFoundError("No module named '_socket'")
            return original(name, *args, **kwargs)
        isolated = importlib.util.module_from_spec(spec)
        with mock.patch("builtins.__import__", side_effect=without_network):
            spec.loader.exec_module(isolated)
            with self.assertRaisesRegex(isolated.CaptureError, "xshell_agent.js"):
                isolated.API({"endpoint": "http://127.0.0.1:18765", "token": "a" * 48})


if __name__ == "__main__":
    unittest.main()
