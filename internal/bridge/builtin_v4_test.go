package bridge

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuiltinV4ReadMatrix(t *testing.T) {
	allowed := []string{
		"cat /etc/passwd", "cat -nbsA -- '../中文 目录/-file'", "cat --number --show-ends /tmp/a /tmp/b",
		"grep -nivE -m10 -A 2 -B3 -C1 -e 'ERROR|WARN' -e '中文(超时)' /tmp/a",
		"grep --fixed-strings --line-number --max-count=5 -- '-pattern' /tmp/a",
		"grep -e '-pattern' -- -filename", "grep -r -n ERROR", "grep -r -n ERROR .", "head -n200 /tmp/a", "head --bytes=-20 /tmp/a", "tail -n +20 '/data/app logs/app.log'", "tail -n 10 -- -f",
		"cat /var/log/app.log | grep -n -E 'ERROR|WARN' | head -n 100",
		"tail -n 200 '/data/app logs/app.log' | grep -F 'timeout'",
		"ss -ltn | grep -E 'tcp|udp' | head -n 10",
		"grep -F 'a; b && c <d>|e' /tmp/a", "grep -F '$(literal)`text`' /tmp/a",
		`grep -F "a\"b" '/tmp/中 文'`, `cat /tmp/a' b'"c"`, `cat /tmp/a\ b`, `cat /tmp/a\;reboot`, `grep -F 'a\(b\)' /tmp/a`,
		"journalctl --no-pager -n 50 -u nginx.service --since '2026-01-01 00:00:00' --output=short-iso",
		"journalctl --no-pager --lines=100 --boot=-1 --until '2026-01-02'",
		"cat " + strings.Repeat("a ", builtinMaxFiles),
	}
	for _, command := range allowed {
		if id, reason, _ := builtinCommand(Submit{Command: command, Cwd: "/", TimeoutMS: 60000}); id == "" {
			t.Errorf("expected automatic %q: %s", command, reason)
		}
	}
}

func TestBuiltinV4ShellLiteralArgv(t *testing.T) {
	bash := os.Getenv("XSH_TEST_BASH")
	if bash == "" {
		t.Skip("XSH_TEST_BASH not set")
	}
	// Benign literals only; no file contents or dangerous commands are executed.
	for _, args := range []string{`'/tmp/中 文'`, `/tmp/a\ b`, `/tmp/\x41`, `/tmp/a' b'"c"`, `"a\"b"`, `'a|b; c<d>'`, `"a\z"`, `""`, `-- '-name'`} {
		parsed, reason := builtinParse("cat " + args)
		if reason != "" {
			t.Fatalf("%q: %s", args, reason)
		}
		out, err := exec.Command(bash, "--noprofile", "--norc", "-c", "printf '%s\\0' "+args).Output()
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		if !reflect.DeepEqual(parsed[0][0][1:], got) {
			t.Fatalf("%q: parsed %q shell %q", args, parsed[0][0][1:], got)
		}
	}
}

func TestBuiltinV4SubmissionLifecycle(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.Command = "cat '/tmp/中文 日志' | grep -F 'a|b' | head -n 10"
	r.Cwd, r.TimeoutMS = "/", 15000
	e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS})
	must(t, err)
	j, err := s.Submit(r)
	must(t, err)
	if e.Decision != "builtin" || j.Status != "queued" || j.Command != r.Command || j.BuiltinRuleID != e.MatchedID || !strings.HasPrefix(j.BuiltinRuleID, BuiltinPackID+"/") {
		t.Fatal(e, j)
	}
	again, err := s.Submit(r)
	must(t, err)
	if again.ID != j.ID {
		t.Fatal(again)
	}
	p, err := s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, err)
	if p.Job == nil || p.Job.Command != r.Command {
		t.Fatal(p)
	}
	must(t, s.Event(Event{SessionID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint, JobID: j.ID, Seq: 1, Status: "unknown", Reason: "synthetic"}))
	again, err = s.Submit(r)
	must(t, err)
	if again.Status != "unknown" || again.BuiltinRuleID != j.BuiltinRuleID {
		t.Fatal(again)
	}
	p, err = s.Poll(x.ID, x.Generation, x.Fingerprint)
	if err == nil || p.Job != nil {
		t.Fatal("unknown task replayed")
	}
	reopened, err := Open(s.path)
	must(t, err)
	again, err = reopened.GetJob(j.ID)
	must(t, err)
	if again.BuiltinRuleID != j.BuiltinRuleID || again.Command != r.Command {
		t.Fatal(again)
	}
	s, x = setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r.SessionID, r.Generation = x.ID, x.Generation

	for _, tc := range []struct{ command, reason string }{{"tail -f /tmp/a", "builtin_arguments"}, {"cat /tmp/a > /tmp/b", "builtin_syntax"}} {
		e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: tc.command, Cwd: "/", TimeoutMS: 15000})
		must(t, err)
		if e.Decision != "manual" || e.Checks[0].ReasonCode != tc.reason {
			t.Fatal(e)
		}
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	r.RequestID += "-disabled"
	pending, err := s.Submit(r)
	must(t, err)
	if pending.Status != "pending" {
		t.Fatal(pending)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	again, err = s.Submit(r)
	must(t, err)
	if again.Status != "pending" || again.ID != pending.ID {
		t.Fatal(again)
	}
	newReq := r
	newReq.RequestID += "-new"
	queued, err := s.Submit(newReq)
	must(t, err)
	if queued.Status != "queued" {
		t.Fatal(queued)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	queued, err = s.GetJob(queued.ID)
	must(t, err)
	if queued.Status != "pending" || queued.ApprovalHistory[0].ID != queued.BuiltinRuleID {
		t.Fatal(queued)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	again, err = s.Submit(newReq)
	must(t, err)
	if again.Status != "pending" {
		t.Fatal(again)
	}
	stale := r
	stale.Generation = "old-generation"
	stale.RequestID += "-stale"
	if _, err = s.Submit(stale); err == nil {
		t.Fatal("old generation accepted")
	}
	s.state.Sessions[x.ID].LastSeen = time.Now().Add(-time.Minute)
	e, err = s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS})
	must(t, err)
	if e.Decision != "blocked" {
		t.Fatal(e)
	}
}

func TestBuiltinV4OldQueuedPackNotDispatched(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.Command, r.Cwd = "hostname", "/"
	j, err := s.Submit(r)
	must(t, err)
	old := "builtin-readonly-v3/hostname"
	s.state.Jobs[j.ID].BuiltinRuleID = old
	s.state.Jobs[j.ID].ApprovalHistory[0].ID = old
	p, err := s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, err)
	j, err = s.GetJob(j.ID)
	must(t, err)
	if p.Job != nil || j.Status != "pending" || j.BuiltinRuleID != old || j.ApprovalHistory[0].ID != old {
		t.Fatal(p, j)
	}
}

func TestBuiltinV4QueuedRestartDoesNotReplay(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.Command, r.Cwd = "head -n 20 /tmp/synthetic", "/"
	j, err := s.Submit(r)
	must(t, err)
	if j.Status != "queued" {
		t.Fatal(j)
	}
	reopened, err := Open(s.path)
	must(t, err)
	old, err := reopened.GetJob(j.ID)
	must(t, err)
	if old.Status != "cancelled" || old.BuiltinRuleID != j.BuiltinRuleID || old.ApprovalHistory[0].ID != j.BuiltinRuleID {
		t.Fatal(old)
	}
}

func TestBuiltinV4RejectMatrix(t *testing.T) {
	rejected := []string{
		"cat", "head", "tail", "grep foo", "cat -", "grep -f /tmp/p /tmp/a", "cat --help /tmp/a",
		"cat /tmp/a --unknown", "cat -file", "head -n 1000001 /tmp/a", "tail -f /tmp/a", "tail -F /tmp/a", "tail --follow=name /tmp/a", "tail --pid=1 /tmp/a",
		"grep -pattern /tmp/a", "grep --unknown x /tmp/a", "grep -e /tmp/a", "grep -m 99999999 x /tmp/a",
		"cat /tmp/a | grep x | tee /tmp/b", "cat /tmp/a | sh", "hostname | grep x | head | python",
		"journalctl --no-pager -n 10 --vacuum-time=1d", "journalctl --no-pager -n 10 --rotate", "journalctl --no-pager -n 10 --flush", "journalctl --no-pager -n 10 --sync", "journalctl --no-pager -n 10 --setup-keys", "journalctl --no-pager -n 00", "journalctl -n 10", "journalctl --no-pager", "journalctl --no-pager -n 10 -f",
		"cat /tmp/a > /tmp/b", "cat /tmp/a 2>/tmp/b", "cat /tmp/a 2>&1", "cat /tmp/a <> /tmp/b", "cat /tmp/a << EOF", "cat /tmp/a <<< x", "cat /tmp/a || id", "cat /tmp/a & id",
		"cat $(id)", "cat `id`", `cat "$IFS"`, `cat "$(id)"`, `cat "` + "`id`" + `"`, "cat <(id)", "cat /tmp/*", "cat /tmp/a # comment", "cat /tmp/a\r\nid", "cat /tmp/a\x00", "cat /tmp/a\\\nid", "cat 'a\nb'", "cat " + string([]byte{0xff}),
		"cat /tmp/a; reboot", "cat /tmp/a | grep x | head | grep y | head | grep z | head | grep a | head",
		"cat " + strings.Repeat("x", 4096), "cat " + strings.Repeat("a ", 130), "cat " + strings.Repeat("a ", 33),
	}
	for _, command := range rejected {
		if id, _, _ := builtinCommand(Submit{Command: command, Cwd: "/", TimeoutMS: 60000}); id != "" {
			t.Errorf("unexpected automatic %q: %s", command, id)
		}
	}
}
