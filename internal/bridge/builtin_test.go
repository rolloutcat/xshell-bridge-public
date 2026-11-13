package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuiltinFailClosedAndExplain(t *testing.T) {
	allowed := []string{"hostname", "whoami", "id", "pwd", "uname -a", "uname -sr", "uptime", "free -h", "df -hT", "ps -eo pid,comm,pcpu,pmem --sort=-pcpu", "systemctl --no-pager status nginx.service", "hostname; id; pwd; uname -sr", "df -h && free -h", "hostname\nid", "cat /etc/passwd", "tail /tmp/a", strings.Repeat("id;", 9) + "id"}
	manual := []string{"hostname evil", "id -u root", "PATH=/tmp hostname", "env hostname", "hostname | id", "hostname & id", "hostname &&", ";hostname", "hostname;;id", "hostname #ok", "$(hostname)", "`hostname`", "'hostname'", "hostname\\x3bid", "hostname%3bid", "hostname > /tmp/x", "(hostname)", "bash -c id", "curl example.com", "systemctl status nginx.service", "systemctl --no-pager restart nginx.service", "systemctl --no-pager status --all", "systemctl --no-pager status *.service", "uname --help", strings.Repeat("id;", 10) + "id", strings.Repeat(" ", 4090) + "hostname", "hostname\x1b[0m", "ｈostname", "python -c id", "ssh example.com", "systemctl --no-pager status nginx.service --all"}
	for _, automatic := range []bool{true, false} {
		commands := manual
		if automatic {
			commands = allowed
		}
		for _, command := range commands {
			t.Run(command, func(t *testing.T) {
				s, x := setup(t)
				must(t, s.Activate(x.ID, x.Generation))
				r := req(x)
				r.Command = command
				r.Cwd = "/"
				r.TimeoutMS = 60000
				e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS})
				must(t, err)
				j, err := s.Submit(r)
				must(t, err)
				if (j.Status == "queued") != automatic || (e.Decision == "builtin") != automatic {
					t.Fatalf("auto=%v job=%+v explain=%+v", automatic, j, e)
				}
				if automatic && (j.BuiltinRuleID == "" || e.MatchedID != j.BuiltinRuleID || j.ApprovalSource != "builtin") {
					t.Fatal(j, e)
				}
			})
		}
	}
}

func TestBuiltinDefaultsPersistenceAndNoRetroactiveApproval(t *testing.T) {
	s, x := setup(t)
	if !s.BuiltinPolicy().Enabled {
		t.Fatal("new install disabled")
	}
	must(t, s.Activate(x.ID, x.Generation))
	p := s.BuiltinPolicy()
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: p, Enabled: false}))
	r := req(x)
	r.Command = "hostname"
	r.Cwd = "/"
	j, e := s.Submit(r)
	must(t, e)
	if j.Status != "pending" {
		t.Fatal(j)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	old, e := s.Submit(r)
	must(t, e)
	if old.ID != j.ID || old.Status != "pending" {
		t.Fatal(old)
	}
	r.RequestID = "new"
	q, e := s.Submit(r)
	must(t, e)
	if q.Status != "queued" {
		t.Fatal(q)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	q, e = s.GetJob(q.ID)
	must(t, e)
	if q.Status != "pending" || q.BuiltinRuleID == "" || len(q.ApprovalHistory) == 0 {
		t.Fatal(q)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	q, e = s.GetJob(q.ID)
	must(t, e)
	if q.Status != "pending" {
		t.Fatal("reapproved", q)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	reopened, e := Open(s.path)
	must(t, e)
	if reopened.BuiltinPolicy().Enabled {
		t.Fatal("opt out lost")
	}
	if reopened.SetBuiltinPolicy(PolicyChange{Expected: p, Enabled: true}) == nil {
		t.Fatal("stale toggle")
	}
	b, e := os.ReadFile(s.path)
	must(t, e)
	var state map[string]any
	must(t, json.Unmarshal(b, &state))
	delete(state, "builtin_policy_enabled")
	b, e = json.Marshal(state)
	must(t, e)
	path := filepath.Join(t.TempDir(), "existing.json")
	must(t, os.WriteFile(path, b, 0600))
	missing, e := Open(path)
	must(t, e)
	if !missing.BuiltinPolicy().Enabled {
		t.Fatal("absent existing config disabled")
	}
}

func TestBuiltinTargetAndScopeGuards(t *testing.T) {
	for _, mutation := range []string{"pending", "blocked", "archived", "identity", "stale", "generation", "relative", "control", "timeout"} {
		t.Run(mutation, func(t *testing.T) {
			s, x := setup(t)
			must(t, s.Activate(x.ID, x.Generation))
			r := req(x)
			r.Command = "id"
			r.Cwd = "/"
			target := s.state.Sessions[x.ID]
			switch mutation {
			case "pending", "blocked":
				target.State = mutation
			case "archived":
				target.Archived = true
			case "identity":
				target.Fingerprint = ""
			case "stale":
				target.LastSeen = time.Now().Add(-16 * time.Second)
			case "generation":
				r.Generation = "other"
			case "relative":
				r.Cwd = "tmp"
			case "control":
				r.Cwd = "/tmp/\t"
			case "timeout":
				r.TimeoutMS = 60001
			}
			j, _ := s.Submit(r)
			if j.Status == "queued" {
				t.Fatal("unsafe auto", j)
			}
		})
	}
}

func TestBuiltinNoReplayAndHistoricalProvenance(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.Command = "hostname"
	r.Cwd = "/"
	j, e := s.Submit(r)
	must(t, e)
	p, e := s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	if p.Job == nil || p.Job.ID != j.ID {
		t.Fatal(p)
	}
	must(t, s.Event(Event{SessionID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint, JobID: j.ID, Seq: 1, Status: "unknown", Reason: "fixture uncertain"}))
	old, e := s.Submit(r)
	must(t, e)
	if old.ID != j.ID || old.Status != "unknown" || old.BuiltinRuleID != j.BuiltinRuleID {
		t.Fatal(old)
	}
	p, _ = s.Poll(x.ID, x.Generation, x.Fingerprint)
	if p.Job != nil {
		t.Fatal("replayed unknown", p)
	}
	reopened, e := Open(s.path)
	must(t, e)
	old, e = reopened.GetJob(j.ID)
	must(t, e)
	if old.BuiltinRuleID != j.BuiltinRuleID || len(old.ApprovalHistory) != 1 {
		t.Fatal(old)
	}
}

func TestBuiltinToggleRollbackCustomIsolationAndAtomicRemember(t *testing.T) {
	s, x, spec := ruleFixture(t)
	r := req(x)
	j, e := s.Submit(r)
	must(t, e)
	must(t, s.DecideRemember(j.ID, true, 30))
	rule, e := s.CreateRule(spec)
	must(t, e)
	policy := s.BuiltinPolicy()
	before := stateJSON(t, s)
	original := s.path
	s.path = filepath.Join(original, "failure")
	if s.SetBuiltinPolicy(PolicyChange{Expected: policy, Enabled: false}) == nil || stateJSON(t, s) != before {
		t.Fatal("policy escaped persistence rollback")
	}
	s.path = original
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: policy, Enabled: false}))
	if len(s.Rules()) != 1 || s.Rules()[0].ID != rule.ID || len(s.Grants()) != 1 {
		t.Fatal("custom authority removed")
	}
	r.RequestID = "manual-hostname"
	r.Command = "hostname"
	r.Cwd = "/"
	j, e = s.Submit(r)
	must(t, e)
	item := BatchItem{ID: j.ID, SessionID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint, Name: x.Name, Environment: x.Environment, Command: j.Command, Cwd: j.Cwd, TimeoutMS: j.TimeoutMS}
	stale := item
	stale.Command = "id"
	if s.DecideBatch(BatchDecision{Allow: true, Items: []BatchItem{stale}, RememberMinutes: 30}) == nil {
		t.Fatal("stale command accepted")
	}
	if len(s.Grants()) != 1 {
		t.Fatal("stale remember mutated grants")
	}
	must(t, s.DecideBatch(BatchDecision{Allow: true, Items: []BatchItem{item}, RememberMinutes: 30}))
	if len(s.Grants()) != 2 {
		t.Fatal("remember missing")
	}
	if s.DecideBatch(BatchDecision{Allow: true, Items: []BatchItem{item}, RememberMinutes: 30}) == nil {
		t.Fatal("duplicate approval")
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	r.RequestID = "builtin-again"
	q, e := s.Submit(r)
	must(t, e)
	if q.ApprovalSource != "builtin" {
		t.Fatal("precedence", q)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	must(t, s.Decide(q.ID, true))
	q, e = s.GetJob(q.ID)
	must(t, e)
	if q.BuiltinRuleID == "" || q.ApprovalSource != "operator" || len(q.ApprovalHistory) != 2 || q.ApprovalHistory[0].Source != "builtin" {
		t.Fatal("history erased", q)
	}
}
