package bridge

import (
	"strings"
	"testing"
)

func TestBuiltinV5ReadOnlyScope(t *testing.T) {
	for _, tc := range []struct {
		command   string
		timeout   int
		automatic bool
	}{
		{"ls", 15000, true}, {"ls -lah /tmp", 15000, true}, {"ls -lh /var/log", 15000, true},
		{"ls -d -- '/tmp/中文 目录' -fixture", 15000, true}, {"ls --color=never --all /tmp | head -n 20", 15000, true},
		{"ls -R /tmp", 15000, false}, {"ls --recursive /tmp", 15000, false}, {"ls --color=always /tmp", 15000, false},
		{"ls --unknown /tmp", 15000, false}, {"ls /tmp/*", 15000, false}, {"ls " + strings.Repeat("/tmp ", 33), 15000, false},
		{"tail -c 16777216 /tmp/fixture.log | grep -F -n -B 8 -A 55 fixture", 20000, true},
		{"head -c67108864 /tmp/fixture.log", 20000, true}, {"tail --bytes=-67108864 /tmp/fixture.log", 20000, true},
		{"tail --bytes +67108864 /tmp/fixture.log", 20000, true}, {"head -c 67108865 /tmp/fixture.log", 20000, false},
		{"tail -c 64M /tmp/fixture.log", 20000, false}, {"tail -n 1000000 /tmp/fixture.log", 20000, true},
		{"tail -n 1000001 /tmp/fixture.log", 20000, false}, {"grep -m 1000001 fixture /tmp/fixture.log", 20000, false},
		{"journalctl --no-pager -n 1000001", 20000, false}, {"tail -f -c 16777216 /tmp/fixture.log", 20000, false},
		{"timeout -k 2s 25s grep -F -n -m 1 -B 8 -A 65 fixture /tmp/fixture.log", 35000, true},
		{"timeout 5 ls -lh /tmp", 15000, true}, {"timeout --kill-after=2s 25s tail -c 16777216 /tmp/fixture.log", 35000, true},
		{"timeout --kill-after 2 -- 25 grep -F fixture /tmp/fixture.log", 35000, true},
		{"ls /tmp | timeout -k 1s 5s grep -F fixture", 15000, true},
		{"timeout -k 2s 25s grep -F fixture /tmp/fixture.log", 26000, false},
		{"timeout 0 grep fixture /tmp/fixture.log", 15000, false}, {"timeout 1m grep fixture /tmp/fixture.log", 60000, false},
		{"timeout 1.5s grep fixture /tmp/fixture.log", 15000, false}, {"timeout -k 0 5s grep fixture /tmp/fixture.log", 15000, false},
		{"timeout -k 11s 5s grep fixture /tmp/fixture.log", 30000, false}, {"timeout 61s grep fixture /tmp/fixture.log", 60000, false},
		{"timeout --signal=KILL 5s grep fixture /tmp/fixture.log", 15000, false}, {"timeout --foreground 5s grep fixture /tmp/fixture.log", 15000, false},
		{"timeout 5s bash -c 'cat /tmp/fixture.log'", 15000, false}, {"timeout 5s python3 -c 'print(1)'", 15000, false},
		{"timeout 5s timeout 3s cat /tmp/fixture.log", 15000, false}, {"timeout 5s env cat /tmp/fixture.log", 15000, false},
		{"timeout 5s cat /tmp/fixture.log > /tmp/out", 15000, false}, {"timeout 5s cat /tmp/fixture.log | sh", 15000, false},
		{"timeout -k 1s -k 2s 5s cat /tmp/fixture.log", 15000, false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			s, x := setup(t)
			must(t, s.Activate(x.ID, x.Generation))
			r := req(x)
			r.Command, r.Cwd, r.TimeoutMS = tc.command, "/", tc.timeout
			e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS})
			must(t, err)
			j, err := s.Submit(r)
			must(t, err)
			if (j.Status == "queued") != tc.automatic || (e.Decision == "builtin") != tc.automatic {
				t.Fatalf("automatic=%v job=%+v explanation=%+v", tc.automatic, j, e)
			}
			if tc.automatic {
				if j.RequiresExactApproval || j.ApprovalSource != "builtin" || e.MatchedID != j.BuiltinRuleID {
					t.Fatal(j, e)
				}
				p, err := s.Poll(x.ID, x.Generation, x.Fingerprint)
				must(t, err)
				if p.Job == nil || p.Job.Command != tc.command {
					t.Fatal("dispatch lost original command", p)
				}
			}
		})
	}
}

func TestBuiltinV5TimeoutNeverOverridesDeny(t *testing.T) {
	for _, command := range []string{"timeout -k 2s 25s rm -rf /tmp/fixture", "ls /tmp; rm -rf /tmp/fixture", "timeout 5s mysql -e 'DROP TABLE fixture'"} {
		s, x := setup(t)
		must(t, s.Activate(x.ID, x.Generation))
		r := req(x)
		r.Command, r.Cwd, r.TimeoutMS = command, "/", 35000
		if _, err := s.Submit(r); err == nil || !strings.Contains(err.Error(), "COMMAND_DENIED") {
			t.Fatal(command, err)
		}
	}
}

func TestBuiltinV5TimeoutOptOutAndOldApproval(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.Command, r.Cwd, r.TimeoutMS = "timeout -k 2s 25s grep -F fixture /tmp/fixture.log", "/", 35000
	j, err := s.Submit(r)
	must(t, err)
	if j.Status != "queued" {
		t.Fatal(j)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	j, err = s.GetJob(j.ID)
	must(t, err)
	if j.Status != "pending" || !j.RequiresExactApproval {
		t.Fatal(j)
	}
	r.RequestID += "-off"
	pending, err := s.Submit(r)
	must(t, err)
	if pending.Status != "pending" || !pending.RequiresExactApproval {
		t.Fatal(pending)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	again, err := s.Submit(r)
	must(t, err)
	if again.Status != "pending" || again.ID != pending.ID {
		t.Fatal("retroactive approval", again)
	}
	r.RequestID += "-new"
	queued, err := s.Submit(r)
	must(t, err)
	s.state.Jobs[queued.ID].BuiltinRuleID = "builtin-readonly-v4/timeout-grep"
	p, err := s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, err)
	if p.Job != nil {
		t.Fatal("old pack dispatched", p)
	}
	old, err := s.GetJob(queued.ID)
	must(t, err)
	if old.Status != "pending" || !old.RequiresExactApproval {
		t.Fatal(old)
	}
}
