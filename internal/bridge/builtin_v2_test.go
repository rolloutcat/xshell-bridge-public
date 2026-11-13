package bridge

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuiltinV2CommandScopeAndExplainParity(t *testing.T) {
	allowed := []string{
		"id -u", "id -g", "id -G", "id -un", "id -gn",
		"date", "date -u", "date --utc", "date -Iseconds", "date +%F", "date +%T",
		"df -i", "df -ih", "df -hi", "nproc", "lscpu", "vmstat 1 1",
		"ps -eo pid,comm,pcpu,pmem --sort=-pcpu",
		"ip address show", "ip addr show", "ip -brief address show", "ip link show", "ip route show", "ip route list", "ip -6 route show",
		"ss -ltn", "ss -lntu", "ss -ltnp", "ss --numeric --listening --tcp", "ss --numeric --listening --udp",
		"systemctl --no-pager status nginx.service", "systemctl --no-pager is-active nginx.service", "systemctl --no-pager is-enabled nginx.service",
		"date -u; ip route show && ss -ltn",
	}
	manual := []string{
		"ID -u", "id -u root", "hostname -f extra", "date -s 2020", "date --set=2020", "date -f /tmp/a", "date --file=/tmp/a", "date +%F extra",
		"df -i /", "df -ih --output=file", "nproc --all", "lscpu -J", "vmstat 1 2", "ps -ef", "ps -eo pid,args", "ps -eo pid,comm,pcpu,pmem --sort=-pcpu extra",
		"ip address add 1", "ip addr flush", "ip link delete eth0", "ip route change default", "ip netns exec x id", "ip -batch /tmp/x", "ip route show table all", "IP route show",
		"ss -K", "ss -ltnK", "ss -F /tmp/a", "ss -D /tmp/a", "ss -ltn extra", "ss -ltn --kill", "SS -ltn",
		"systemctl --no-pager restart nginx.service", "systemctl --no-pager status nginx.service extra", "systemctl --host host --no-pager status nginx.service", "systemctl --no-pager status -x.service", "systemctl --no-pager status nginx.service@evil",
		"env date", "TZ=UTC date", "PATH=/tmp ip route show", "date $(id)", "date > /tmp/a", "date | id", "date && ss -K", "date; date -s 2020",
	}
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	for _, tc := range []struct {
		commands []string
		auto     bool
	}{{allowed, true}, {manual, false}} {
		for i, command := range tc.commands {
			t.Run(command, func(t *testing.T) {
				r := req(x)
				r.RequestID = command
				r.Command = command
				r.Cwd = "/"
				r.TimeoutMS = 60000
				e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS})
				must(t, err)
				j, err := s.Submit(r)
				must(t, err)
				if (j.Status == "queued") != tc.auto || (e.Decision == "builtin") != tc.auto {
					t.Fatalf("case %d auto=%v job=%s explanation=%+v", i, tc.auto, j.Status, e)
				}
				if tc.auto && (j.BuiltinRuleID != e.MatchedID || !strings.HasPrefix(j.BuiltinRuleID, BuiltinPackID+"/")) {
					t.Fatal(j.BuiltinRuleID, e.MatchedID)
				}
			})
		}
	}
}

func TestBuiltinV2BoundedServiceQueriesAndShellGrammar(t *testing.T) {
	allowed := []string{
		"systemctl --no-pager show --property=ActiveState nginx.service",
		"systemctl --no-pager show --property=ActiveState,SubState nginx.service",
		"systemctl --no-pager list-units --type=service --no-legend",
		"systemctl --no-pager list-unit-files --type=service --no-legend",
		"ps -eo pid,comm,pcpu,pmem --sort=-pcpu | head -n 20",
		"date -u\nnproc\n  \n ", "date | head -n 1", "date | head -n 200",
	}
	manual := []string{
		"systemctl --no-pager show nginx.service", "systemctl --no-pager show --property=Environment nginx.service",
		"systemctl --no-pager show --property=ActiveState,Environment nginx.service", "systemctl --no-pager show --property=ActiveState nginx.service other.service",
		"systemctl --no-pager show --property=ActiveState --host=other nginx.service", "systemctl --no-pager list-units --type=service --no-legend --host=other",
		"systemctl --no-pager list-unit-files --type=service --no-legend --output=json", "date || head -n 1", "date | head -n 1; reboot",
		"date\nreboot", "date\n\nreboot", "date;\nreboot", "date;\n ", "date\n&& id", "date\r\nid", "date $(id)\nnproc", "date > /tmp/file\nnproc", "date\nPATH=/tmp id", "date\n'whoami'", "date | head -n 1 && systemctl --no-pager restart nginx.service",
	}
	for _, command := range allowed {
		if id, reason, _ := builtinCommand(Submit{Command: command, Cwd: "/", TimeoutMS: 60000}); id == "" {
			t.Errorf("expected allowed %q: %s", command, reason)
		}
	}
	for _, command := range manual {
		if id, _, _ := builtinCommand(Submit{Command: command, Cwd: "/", TimeoutMS: 60000}); id != "" {
			t.Errorf("unexpected allowed %q: %s", command, id)
		}
	}
}

func TestBuiltinV2DisplayedExamplesMatch(t *testing.T) {
	page, err := os.ReadFile("../../desktop/app.html")
	must(t, err)
	for _, example := range []string{"id -u", "date -u", "df -i", "free -h", "nproc", "ps -eo pid,comm,pcpu,pmem --sort=-pcpu", "ip route show", "ss -ltn", "systemctl --no-pager status nginx.service", "tail -n 200 /var/log/app.log", "grep -n -m 100 ERROR /var/log/app.log", "journalctl --no-pager -n 50 -u nginx.service"} {
		if !strings.Contains(string(page), "<code>"+example+"</code>") {
			t.Fatalf("UI example missing: %s", example)
		}
		id, reason, _ := builtinCommand(Submit{Command: example, Cwd: "/", TimeoutMS: 60000})
		if id == "" || reason != "matched" {
			t.Fatalf("UI example not allowed: %s, %s", example, reason)
		}
	}
	for _, rule := range builtinForms {
		id, reason, _ := builtinCommand(Submit{Command: rule.forms[0], Cwd: "/", TimeoutMS: 60000})
		if id == "" || reason != "matched" {
			t.Fatalf("hint not allowed: %s", rule.forms[0])
		}
	}
}

func TestBuiltinV2LegacyOptOutAndRestartNoReplay(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.Command = "hostname"
	r.Cwd = "/"
	j, err := s.Submit(r)
	must(t, err)
	s.state.Jobs[j.ID].BuiltinRuleID = "builtin-readonly-v1/hostname"
	s.state.Jobs[j.ID].ApprovalHistory[0].ID = "builtin-readonly-v1/hostname"
	// This is the v1 persistence shape: enabled and revision existed, but no
	// pack version was stored. An explicit v1 opt-out must survive upgrade.
	disabled := false
	s.state.BuiltinPolicyEnabled = &disabled
	s.state.BuiltinPolicyRevision = 7
	must(t, s.persist())
	reopened, err := Open(s.path)
	must(t, err)
	old, err := reopened.GetJob(j.ID)
	must(t, err)
	if reopened.BuiltinPolicy().Enabled || reopened.BuiltinPolicy().Revision != 7 || old.Status != "cancelled" || old.BuiltinRuleID != "builtin-readonly-v1/hostname" || old.ApprovalHistory[0].ID != old.BuiltinRuleID {
		t.Fatal(reopened.BuiltinPolicy(), old)
	}
	if reopened.SetBuiltinPolicy(PolicyChange{Expected: BuiltinPolicy{ID: "builtin-readonly-v1", Version: 1, Enabled: false, Revision: 7}, Enabled: true}) == nil {
		t.Fatal("v1 expected snapshot accepted")
	}
}

func TestBuiltinV2DisabledAndStaleNewCommand(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	r := req(x)
	r.Command, r.Cwd = "ip route show", "/"
	e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS})
	must(t, err)
	j, err := s.Submit(r)
	must(t, err)
	if e.Decision != "manual" || e.Checks[0].ReasonCode != "builtin_disabled" || j.Status != "pending" {
		t.Fatal(e, j)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	old, err := s.Submit(r)
	must(t, err)
	if old.ID != j.ID || old.Status != "pending" {
		t.Fatal(old)
	}
	s.state.Sessions[x.ID].LastSeen = time.Now().Add(-time.Minute)
	e, err = s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: r.Cwd, TimeoutMS: r.TimeoutMS})
	must(t, err)
	if e.Decision != "blocked" || e.ReasonCode != "stale" {
		t.Fatal(e)
	}
}

func TestBuiltinV2ExplainProvidesSafeProgramHint(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	e, err := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: "ip route delete SECRET", Cwd: "/", TimeoutMS: 60000})
	must(t, err)
	if e.Decision != "manual" || e.Checks[0].ReasonCode != "builtin_arguments" || !strings.Contains(e.Checks[0].Message, "ip route show") || !strings.Contains(e.Checks[0].Message, "不会") || strings.Contains(e.Checks[0].Message, "SECRET") {
		t.Fatal(e)
	}
	e, err = s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: "TZ=SECRET date", Cwd: "/", TimeoutMS: 60000})
	must(t, err)
	if e.Checks[0].ReasonCode != "builtin_environment" || strings.Contains(e.Checks[0].Message, "SECRET") {
		t.Fatal(e)
	}
}

func TestBuiltinV2OldStateAndProvenance(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.Command = "hostname"
	r.Cwd = "/"
	j, err := s.Submit(r)
	must(t, err)
	oldID := "builtin-readonly-v1/hostname"
	s.state.Jobs[j.ID].BuiltinRuleID = oldID
	s.state.Jobs[j.ID].ApprovalHistory[0].ID = oldID
	oldSnapshot := BuiltinPolicy{ID: "builtin-readonly-v1", Version: 1, Enabled: true, Revision: s.BuiltinPolicy().Revision}
	if s.SetBuiltinPolicy(PolicyChange{Expected: oldSnapshot, Enabled: false}) == nil {
		t.Fatal("old snapshot accepted")
	}
	p, err := s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, err)
	old, err := s.GetJob(j.ID)
	must(t, err)
	if p.Job != nil || old.Status != "pending" || old.BuiltinRuleID != oldID || old.ApprovalHistory[0].ID != oldID {
		t.Fatal(p, old)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	reopened, err := Open(s.path)
	must(t, err)
	old, err = reopened.GetJob(j.ID)
	must(t, err)
	if reopened.BuiltinPolicy().Enabled || reopened.BuiltinPolicy().ID != BuiltinPackID || reopened.BuiltinPolicy().Version != 5 || old.BuiltinRuleID != oldID || old.ApprovalHistory[0].ID != oldID || old.Status == "queued" {
		t.Fatal(reopened.BuiltinPolicy(), old)
	}
}
