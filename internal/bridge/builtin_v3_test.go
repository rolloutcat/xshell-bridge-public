package bridge

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestBuiltinV3ArgumentMatrix(t *testing.T) {
	allowed := []string{
		"ss -lntp", "ss -lntup", "ss -lnutp",
		"systemctl status --no-pager nginx.service", "systemctl status nginx.service --no-pager",
		"systemctl is-active nginx.service --no-pager", "systemctl --no-pager is-enabled nginx.service",
		"systemctl show nginx.service --no-pager -p ActiveState,SubState",
		"systemctl --no-pager show -p ActiveState nginx.service",
		"systemctl show --property ActiveState nginx.service --no-pager",
		"systemctl show nginx.service --property=MainPID --no-pager",
		"systemctl list-units --failed --type=service --no-legend --no-pager",
	}
	rejected := []string{
		"ss -lntpp", "ss -lntup --kill", "ss -lntp; reboot",
		"systemctl status --no-pager --no-pager nginx.service", "systemctl status nginx.service other.service --no-pager",
		"systemctl status nginx.service --host=other --no-pager", "systemctl restart nginx.service --no-pager",
		"systemctl nginx.service status --no-pager", "systemctl status *.service --no-pager", "systemctl status nginx.service --no-pager && reboot",
		"systemctl show nginx.service --no-pager -p Environment", "systemctl show nginx.service --no-pager -p ExecStart",
		"systemctl show nginx.service --no-pager -p ActiveState,ActiveState", "systemctl show nginx.service --no-pager -p ActiveState -p SubState",
		"systemctl list-units --failed --type=service --no-legend --no-pager --failed",
	}
	for _, tc := range []struct {
		commands []string
		allowed  bool
	}{{allowed, true}, {rejected, false}} {
		for _, command := range tc.commands {
			id, reason, _ := builtinCommand(Submit{Command: command, Cwd: "/", TimeoutMS: 60000})
			if (id != "") != tc.allowed {
				t.Errorf("%q allowed=%v id=%q reason=%q", command, tc.allowed, id, reason)
			}
		}
	}
}

func TestCommandPolicyExamplesAndSnapshot(t *testing.T) {
	s, _ := setup(t)
	before := s.BuiltinPolicy()
	catalog := s.CommandPolicy()
	if catalog.HardDeny.ID != HardDenyPolicyID || !catalog.HardDeny.Enabled || len(catalog.HardDeny.Categories) == 0 {
		t.Fatal(catalog.HardDeny)
	}
	if catalog.ReadLimits != (ReadLimits{builtinMaxCommandBytes, builtinMaxWords, builtinMaxSegments, builtinMaxStages, builtinMaxFiles, builtinMaxCount, builtinMaxReadBytes}) {
		t.Fatal(catalog.ReadLimits)
	}
	if len(catalog.ServiceProperties) != len(builtinProperties) {
		t.Fatal("catalogue omits property choices")
	}
	for _, property := range catalog.ServiceProperties {
		id, _, _ := builtinCommand(Submit{Command: "systemctl show nginx.service --no-pager -p " + property, Cwd: "/", TimeoutMS: 60000})
		if id == "" {
			t.Fatalf("catalogue property %q is not supported", property)
		}
	}
	if catalog.Policy != before || len(catalog.Categories) != 5 || len(catalog.Constraints) == 0 || catalog.Notice == "" {
		t.Fatal(catalog)
	}
	for _, category := range catalog.Categories {
		if len(category.Examples) == 0 {
			t.Fatal(category)
		}
		for _, example := range category.Examples {
			if id, reason, _ := builtinCommand(Submit{Command: example, Cwd: "/", TimeoutMS: 60000}); id == "" {
				t.Errorf("catalogue example %q: %s", example, reason)
			}
		}
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: before, Enabled: false}))
	if !s.CommandPolicy().HardDeny.Enabled {
		t.Fatal("read-only toggle disabled hard deny")
	}
	if s.CommandPolicy().Policy.Enabled || s.CommandPolicy().Policy.Revision != before.Revision+1 {
		t.Fatal(s.CommandPolicy().Policy)
	}
}

func TestBuiltinV3SkillRecipesMatch(t *testing.T) {
	b, err := os.ReadFile("../../skills/xshctl/references/inspection.md")
	must(t, err)
	pattern := regexp.MustCompile("`([^`]+)`")
	count := 0
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		for _, match := range pattern.FindAllStringSubmatch(line, -1) {
			command := strings.ReplaceAll(match[1], `\|`, `|`)
			id, reason, _ := builtinCommand(Submit{Command: command, Cwd: "/", TimeoutMS: 60000})
			if id == "" {
				t.Errorf("recipe %q: %s", command, reason)
			}
			count++
		}
	}
	if count < 6 {
		t.Fatal("missing recipe examples", count)
	}
}

func TestBuiltinV3SwitchAndIdempotency(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: false}))
	r := req(x)
	r.Command = "systemctl status nginx.service --no-pager"
	r.Cwd = "/"
	j, err := s.Submit(r)
	must(t, err)
	if j.Status != "pending" {
		t.Fatal(j)
	}
	must(t, s.SetBuiltinPolicy(PolicyChange{Expected: s.BuiltinPolicy(), Enabled: true}))
	again, err := s.Submit(r)
	must(t, err)
	if again.ID != j.ID || again.Status != "pending" {
		t.Fatal(again)
	}
	conflict := r
	conflict.Command = "ss -lntp"
	if _, err = s.Submit(conflict); err == nil {
		t.Fatal("changed command reused request ID")
	}
	newReq := r
	newReq.RequestID += "-new"
	queued, err := s.Submit(newReq)
	must(t, err)
	if queued.Status != "queued" {
		t.Fatal(queued)
	}
	stale := r
	stale.RequestID += "-stale"
	stale.Generation = "stale-generation"
	if _, err = s.Submit(stale); err == nil {
		t.Fatal("stale generation accepted")
	}
	reopened, err := Open(s.path)
	must(t, err)
	old, err := reopened.GetJob(queued.ID)
	must(t, err)
	if old.Status == "queued" || old.BuiltinRuleID != queued.BuiltinRuleID {
		t.Fatal("restart replay or provenance changed", old)
	}
}
