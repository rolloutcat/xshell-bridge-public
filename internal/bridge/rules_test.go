package bridge

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ruleFixture(t *testing.T) (*Store, Session, RuleSpec) {
	t.Helper()
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	return s, x, RuleSpec{Name: "test rule", SessionID: x.ID, Generation: x.Generation, Program: "printf", ArgumentPatterns: []string{"test"}, MaxTimeoutMS: 10000, Minutes: 30}
}

func TestRuleHTTPNegative(t *testing.T) {
	s, _, _ := ruleFixture(t)
	h := Handler(s, Keys{Client: "c", Agent: "a", Admin: "o"})
	for _, tc := range []struct {
		path, token string
		want        int
	}{
		{"/v1/rules", "", 401}, {"/v1/rules", "a", 403},
		{"/v1/admin/rules", "c", 403}, {"/v1/admin/rules", "a", 403},
		{"/v1/admin/revoke-rule", "c", 403}, {"/v1/admin/revoke-rule", "a", 403},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code)
		}
	}
	for _, body := range []string{`{"unknown":true}`, `{"expires":"2099-01-01T00:00:00Z"}`, `null`, `{} {}`} {
		r := httptest.NewRequest("POST", "/v1/admin/rules", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer o")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
}

func TestRuleRejectsBroadenedCommands(t *testing.T) {
	s, x, spec := ruleFixture(t)
	spec.ArgumentPatterns = []string{".*"}
	_, e := s.CreateRule(spec)
	must(t, e)
	for i, command := range []string{
		"printf", "printf test extra", "printfx test", "/bin/printf test", "printf test;id", "printf test|id", "printf test&&id", "printf 'test'", "printf \"test\"", "printf te\\st", "printf test\nid", "printf `id`", "printf $(id)", "printf $HOME", "printf *", "printf ?", "printf [a]", "printf #x", "printf >out", "printf <in", "printf (id)", "printf tést",
	} {
		r := req(x)
		r.RequestID = fmt.Sprint(i)
		r.Command = command
		j, e := s.Submit(r)
		must(t, e)
		if j.Status != "pending" || j.RuleID != "" {
			t.Fatal(command, j)
		}
	}
	r := req(x)
	r.Command = "printf test\rid"
	if _, e := s.Submit(r); e == nil {
		t.Fatal("existing CR validation lost")
	}
	for i, mutate := range []func(*Submit){
		func(r *Submit) { r.Cwd = "/tmp" }, func(r *Submit) { r.TimeoutMS++ },
		func(r *Submit) { r.Generation = "wrong" },
	} {
		r := req(x)
		r.RequestID = fmt.Sprint("scope", i)
		mutate(&r)
		j, e := s.Submit(r)
		if e == nil && (j.Status != "pending" || j.RuleID != "") {
			t.Fatal(j)
		}
	}
	other, e := s.Register("other-instance-123456", x.Name, x.Environment, "other-fingerprint")
	must(t, e)
	must(t, s.Activate(other.ID, other.Generation))
	j, e := s.Submit(req(other))
	must(t, e)
	if j.Status != "pending" {
		t.Fatal("name conferred authority")
	}
}

func TestRuleValidationAndLimits(t *testing.T) {
	s, _, spec := ruleFixture(t)
	for _, mutate := range []func(*RuleSpec){
		func(v *RuleSpec) { v.Name = "" }, func(v *RuleSpec) { v.SessionID = "missing" },
		func(v *RuleSpec) { v.Generation = "wrong" }, func(v *RuleSpec) { v.Program = "/bin/printf" },
		func(v *RuleSpec) { v.Program = "écho" }, func(v *RuleSpec) { v.Program = "-echo" },
		func(v *RuleSpec) { v.Minutes = 0 }, func(v *RuleSpec) { v.Minutes = 481 },
		func(v *RuleSpec) { v.MaxTimeoutMS = 999 }, func(v *RuleSpec) { v.MaxTimeoutMS = 3600001 },
		func(v *RuleSpec) { v.Cwd = "a\nb" },
		func(v *RuleSpec) { v.ArgumentPatterns = []string{"["} },
		func(v *RuleSpec) { v.ArgumentPatterns = []string{strings.Repeat("a", 257)} },
		func(v *RuleSpec) { v.ArgumentPatterns = make([]string, 17) },
	} {
		v := spec
		mutate(&v)
		if _, e := s.CreateRule(v); e == nil {
			t.Fatal("invalid spec accepted", v)
		}
	}
	if len(s.Rules()) != 0 {
		t.Fatal("invalid rule persisted")
	}
	for i := 0; i < 100; i++ {
		_, e := s.CreateRule(spec)
		must(t, e)
	}
	if _, e := s.CreateRule(spec); e == nil {
		t.Fatal("rule cap exceeded")
	}
}

func TestRuleExpiredOrRevokedNeverDispatches(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		s, x, spec := ruleFixture(t)
		rule, e := s.CreateRule(spec)
		must(t, e)
		j, e := s.Submit(req(x))
		must(t, e)
		if j.Status != "queued" || j.RuleID != rule.ID {
			t.Fatal(j)
		}
		if revoke {
			must(t, s.RevokeRule(rule.ID))
		} else {
			must(t, s.change(func() error { s.state.Rules[0].Expires = time.Now().Add(-time.Second); return nil }))
		}
		p, e := s.Poll(x.ID, x.Generation, x.Fingerprint)
		must(t, e)
		if p.Job != nil {
			t.Fatal("unauthorized dispatch")
		}
		j, e = s.GetJob(j.ID)
		must(t, e)
		if j.Status != "pending" || j.RuleID != "" {
			t.Fatal(j)
		}
	}
}

func TestRulesLostOnRestartDetachAndIdentityChange(t *testing.T) {
	for _, action := range []string{"restart", "detach", "identity"} {
		s, x, spec := ruleFixture(t)
		_, e := s.CreateRule(spec)
		must(t, e)
		j, e := s.Submit(req(x))
		must(t, e)
		switch action {
		case "restart":
			s, e = Open(s.path)
			must(t, e)
		case "detach":
			must(t, s.Detach(x.ID, x.Generation))
		case "identity":
			if _, e = s.Poll(x.ID, x.Generation, "changed"); e == nil {
				t.Fatal("identity accepted")
			}
		}
		if len(s.Rules()) != 0 || len(s.state.Rules) != 0 {
			t.Fatal(action)
		}
		j, e = s.GetJob(j.ID)
		must(t, e)
		if j.Status != "cancelled" || j.RuleID != "" {
			t.Fatal(j)
		}
	}
}

func TestRuleSerializationCopiesAndRollback(t *testing.T) {
	s, x, spec := ruleFixture(t)
	rule, e := s.CreateRule(spec)
	must(t, e)
	b, e := json.Marshal(rule)
	must(t, e)
	var obj map[string]any
	must(t, json.Unmarshal(b, &obj))
	for _, key := range []string{"id", "name", "session_id", "generation", "program", "argument_patterns", "cwd", "max_timeout_ms", "minutes", "expires"} {
		if _, ok := obj[key]; !ok {
			t.Fatal(key)
		}
	}
	if rule.Expires.Before(time.Now()) || rule.Expires.After(time.Now().Add(31*time.Minute)) {
		t.Fatal(rule.Expires)
	}
	spec.ArgumentPatterns[0] = "changed"
	rule.ArgumentPatterns[0] = "changed"
	list := s.Rules()
	list[0].ArgumentPatterns[0] = "changed"
	if s.Rules()[0].ArgumentPatterns[0] != "test" {
		t.Fatal("aliased rules")
	}
	j, e := s.Submit(req(x))
	must(t, e)
	before, e := json.Marshal(s.state)
	must(t, e)
	s.path = filepath.Join(s.path, "missing", "state.json")
	if _, e := s.CreateRule(s.Rules()[0].RuleSpec); e == nil {
		t.Fatal("create acknowledged")
	}
	if e := s.RevokeRule(rule.ID); e == nil {
		t.Fatal("revoke acknowledged")
	}
	r := req(x)
	r.RequestID = "second"
	if _, e := s.Submit(r); e == nil {
		t.Fatal("submit acknowledged")
	}
	if _, e := s.Poll(x.ID, x.Generation, x.Fingerprint); e == nil {
		t.Fatal("dispatch acknowledged")
	}
	after, e := json.Marshal(s.state)
	must(t, e)
	if string(before) != string(after) {
		t.Fatal("storage failure mutated state", j.ID)
	}
}

func TestRuleDoesNotApprovePendingAndDenialRevokesScope(t *testing.T) {
	s, x, spec := ruleFixture(t)
	pending, e := s.Submit(req(x))
	must(t, e)
	rule, e := s.CreateRule(spec)
	must(t, e)
	pending, e = s.GetJob(pending.ID)
	must(t, e)
	if pending.Status != "pending" {
		t.Fatal("retroactive approval")
	}
	r := req(x)
	r.RequestID = "queued"
	queued, e := s.Submit(r)
	must(t, e)
	must(t, s.Decide(pending.ID, false))
	if len(s.Rules()) != 0 {
		t.Fatal("denial retained rule", rule.ID)
	}
	queued, e = s.GetJob(queued.ID)
	must(t, e)
	if queued.Status != "pending" || queued.RuleID != "" {
		t.Fatal(queued)
	}
}

func TestExactGrantPreferredAndRuleFullToken(t *testing.T) {
	s, x, r := grantFixture(t)
	_, e := s.CreateRule(RuleSpec{Name: "rule", SessionID: x.ID, Generation: x.Generation, Program: "printf", ArgumentPatterns: []string{"test"}, MaxTimeoutMS: r.TimeoutMS, Minutes: 1})
	must(t, e)
	r.RequestID = "preferred"
	j, e := s.Submit(r)
	must(t, e)
	if j.GrantID == "" || j.RuleID != "" {
		t.Fatal(j)
	}
	r.RequestID = "substring"
	r.Command = "printf testing"
	j, e = s.Submit(r)
	must(t, e)
	if j.Status != "pending" {
		t.Fatal("regex was not full-token")
	}
}

func TestRuleHTTPContract(t *testing.T) {
	s, _, spec := ruleFixture(t)
	h := Handler(s, Keys{Client: "c", Admin: "o"})
	b, e := json.Marshal(spec)
	must(t, e)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := call("POST", "/v1/admin/rules", "o", string(b))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var rule Rule
	must(t, json.Unmarshal(w.Body.Bytes(), &rule))
	for _, token := range []string{"c", "o"} {
		w = call("GET", "/v1/rules", token, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), rule.ID) {
			t.Fatal(w.Body.String())
		}
	}
	w = call("GET", "/v1/health", "c", "")
	if !strings.Contains(w.Body.String(), `"approval_rules":true`) {
		t.Fatal(w.Body.String())
	}
	w = call("POST", "/v1/admin/revoke-rule", "o", `{"id":"`+rule.ID+`"}`)
	if w.Code != 200 || len(s.Rules()) != 0 {
		t.Fatal(w.Body.String())
	}
}
