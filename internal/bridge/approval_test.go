package bridge

import (
	"fmt"
	"testing"
	"time"
)

func TestExplainRuleReasonsAndSubmissionParity(t *testing.T) {
	for _, tc := range []struct {
		command, cwd string
		timeout      int
		reason       string
	}{
		{"printf test", "", 10000, "matched"},
		{"printf test;id", "", 10000, "token_syntax"},
		{"echo test", "", 10000, "program"},
		{"printf test extra", "", 10000, "argument_count"},
		{"printf testing", "", 10000, "argument_pattern"},
		{"printf test", "/tmp", 10000, "cwd"},
		{"printf test", "", 10001, "timeout"},
	} {
		s, x, spec := ruleFixture(t)
		_, e := s.CreateRule(spec)
		must(t, e)
		v := ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: tc.command, Cwd: tc.cwd, TimeoutMS: tc.timeout}
		before := stateJSON(t, s)
		out, e := s.Explain(v)
		must(t, e)
		if before != stateJSON(t, s) || len(out.Checks) != 2 || out.Checks[1].ReasonCode != tc.reason || out.Checks[0].Kind != "builtin" {
			t.Fatal(out)
		}
		r := req(x)
		r.Command = tc.command
		r.Cwd = tc.cwd
		r.TimeoutMS = tc.timeout
		j, e := s.Submit(r)
		must(t, e)
		if (out.Decision == "rule") != (j.RuleID != "") {
			t.Fatal(out, j)
		}
	}
}
func TestExplainGrantPrecedenceAndCandidateIsolation(t *testing.T) {
	s, x, spec := ruleFixture(t)
	r := req(x)
	j, e := s.Submit(r)
	must(t, e)
	must(t, s.DecideRemember(j.ID, true, 30))
	_, e = s.CreateRule(spec)
	must(t, e)
	for i, command := range []string{"printf test", "printf test;id"} {
		if i == 1 {
			s.state.Grants[0].Command = command
		}
		out, e := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: command, TimeoutMS: r.TimeoutMS})
		must(t, e)
		if out.Decision != "grant" {
			t.Fatal(out)
		}
		v := r
		v.RequestID = fmt.Sprint(i)
		v.Command = command
		job, e := s.Submit(v)
		must(t, e)
		if job.GrantID != out.MatchedID {
			t.Fatal(out, job)
		}
	}
	s.state.Grants[0].Expires = time.Now().Add(-time.Second)
	s.state.Rules[0].Generation = "old"
	out, e := s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, TimeoutMS: r.TimeoutMS})
	must(t, e)
	if out.Decision != "manual" || out.Checks[1].ReasonCode != "expired" || out.Checks[2].ReasonCode != "generation" {
		t.Fatal(out)
	}
	s.state.Rules[0].Generation = x.Generation
	s.state.Rules[0].Expires = time.Now().Add(-time.Second)
	out, e = s.Explain(ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: r.Command, TimeoutMS: r.TimeoutMS})
	must(t, e)
	if out.Checks[2].ReasonCode != "expired" {
		t.Fatal(out)
	}
	other, e := s.Register("other-instance-123456", "other", "env", "other-fingerprint")
	must(t, e)
	must(t, s.Activate(other.ID, other.Generation))
	out, e = s.Explain(ExplainRequest{SessionID: other.ID, Generation: other.Generation, Command: r.Command, TimeoutMS: r.TimeoutMS})
	must(t, e)
	if out.Decision != "manual" || len(out.Checks) != 1 || out.Checks[0].Kind != "builtin" {
		t.Fatal("other session leaked", out)
	}
}
