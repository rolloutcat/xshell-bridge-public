package bridge

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBatchHTTPExplicitBoolean(t *testing.T) {
	for _, mode := range []string{"missing", "null", "false", "string", "nested_unknown"} {
		t.Run(mode, func(t *testing.T) {
			s, b := batchFixture(t)
			raw, e := json.Marshal(b.Items)
			must(t, e)
			body := `{"items":` + string(raw) + `}`
			switch mode {
			case "null":
				body = `{"allow":null,"items":` + string(raw) + `}`
			case "false":
				body = `{"allow":false,"items":` + string(raw) + `}`
			case "string":
				body = `{"allow":"false","items":` + string(raw) + `}`
			case "nested_unknown":
				body = `{"allow":false,"items":` + strings.Replace(string(raw), `"id":`, `"unknown":true,"id":`, 1) + `}`
			}
			before := stateJSON(t, s)
			r := httptest.NewRequest("POST", "/v1/admin/decide-batch", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer o")
			w := httptest.NewRecorder()
			Handler(s, Keys{Admin: "o"}).ServeHTTP(w, r)
			if mode == "false" {
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				for _, item := range b.Items {
					if s.state.Jobs[item.ID].Status != "denied" {
						t.Fatal("explicit false not applied")
					}
				}
			} else if w.Code != 400 || before != stateJSON(t, s) {
				t.Fatal(mode, w.Code, w.Body.String())
			}
		})
	}
}

func TestExplainStoppingMatchingRuleReadOnly(t *testing.T) {
	s, x, spec := ruleFixture(t)
	_, e := s.CreateRule(spec)
	must(t, e)
	s.stopping = true
	before := stateJSON(t, s)
	disk, e := os.ReadFile(s.path)
	must(t, e)
	out, e := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: "printf test", TimeoutMS: 10000})
	must(t, e)
	after, e := os.ReadFile(s.path)
	must(t, e)
	if out.Decision != "blocked" || out.ReasonCode != "stopping" || out.MatchedID != "" || len(out.Checks) != 0 || before != stateJSON(t, s) || string(disk) != string(after) {
		t.Fatal(out)
	}
}

func TestExplainValidationAndDefaultParity(t *testing.T) {
	for _, tc := range []struct {
		command, cwd string
		timeout      int
	}{
		{"", "", 0}, {" \t", "", 0}, {"a\x00", "", 0}, {"a\r", "", 0},
		{strings.Repeat("a", 16385), "", 0}, {"printf test", "a\n", 0},
		{"printf test", strings.Repeat("a", 4097), 0}, {"printf test", "", 999}, {"printf test", "", 3600001},
	} {
		s, x := setup(t)
		must(t, s.Activate(x.ID, x.Generation))
		before := stateJSON(t, s)
		_, e := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: tc.command, Cwd: tc.cwd, TimeoutMS: tc.timeout})
		if e == nil || before != stateJSON(t, s) {
			t.Fatal("explain validation", tc)
		}
		r := req(x)
		r.Command = tc.command
		r.Cwd = tc.cwd
		r.TimeoutMS = tc.timeout
		if _, e = s.Submit(r); e == nil {
			t.Fatal("submit validation", tc)
		}
	}
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.TimeoutMS = 0
	out, e := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, Cwd: r.Cwd})
	must(t, e)
	j, e := s.Submit(r)
	must(t, e)
	if j.TimeoutMS != 60000 || out.Decision != "manual" {
		t.Fatal(out, j)
	}
}

func TestExplainHTTPSuccessReadOnly(t *testing.T) {
	s, x, spec := ruleFixture(t)
	_, e := s.CreateRule(spec)
	must(t, e)
	body, e := json.Marshal(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: "printf test", TimeoutMS: 10000})
	must(t, e)
	for _, token := range []string{"c", "o"} {
		before := stateJSON(t, s)
		disk, e := os.ReadFile(s.path)
		must(t, e)
		r := httptest.NewRequest("POST", "/v1/explain", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		Handler(s, Keys{Client: "c", Admin: "o"}).ServeHTTP(w, r)
		var out ApprovalExplanation
		must(t, json.Unmarshal(w.Body.Bytes(), &out))
		after, e := os.ReadFile(s.path)
		must(t, e)
		if w.Code != 200 || out.Decision != "rule" || before != stateJSON(t, s) || string(disk) != string(after) {
			t.Fatal(w.Code, out)
		}
	}
}

func TestBatchLaterDifferentSessionStale(t *testing.T) {
	s, b := batchFixture(t)
	x, e := s.Register("other-instance-123456", "other", "env", "other-fingerprint")
	must(t, e)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	r.RequestID = "different-session"
	j, e := s.Submit(r)
	must(t, e)
	b.Items[1] = BatchItem{ID: j.ID, SessionID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint, Name: x.Name, Environment: x.Environment, Command: j.Command, Cwd: j.Cwd, TimeoutMS: j.TimeoutMS}
	s.state.Sessions[x.ID].LastSeen = time.Now().Add(-time.Minute)
	before := stateJSON(t, s)
	if s.DecideBatch(b) == nil || before != stateJSON(t, s) {
		t.Fatal("later stale session partially committed")
	}
}
