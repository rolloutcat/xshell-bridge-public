package bridge

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHeartbeatDoesNotDispatch(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	must(t, s.Heartbeat([]AgentMember{{x.ID, x.Generation, x.Fingerprint}}))
	got, e := s.GetJob(j.ID)
	must(t, e)
	if got.Status != "pending" {
		t.Fatal(got.Status)
	}
	must(t, s.Decide(j.ID, true))
	must(t, s.Heartbeat([]AgentMember{{x.ID, x.Generation, x.Fingerprint}}))
	got, e = s.GetJob(j.ID)
	must(t, e)
	if got.Status != "queued" {
		t.Fatal("heartbeat dispatched", got.Status)
	}
}

func TestHeartbeatAtomicValidation(t *testing.T) {
	for _, kind := range []string{"generation", "fingerprint", "expired", "restart"} {
		t.Run(kind, func(t *testing.T) {
			s, x := setup(t)
			y, e := s.Register("another-instance-123456", "other", "isolated", "fingerprint-2")
			must(t, e)
			before := s.state.Sessions[x.ID].LastSeen
			members := []AgentMember{{x.ID, x.Generation, x.Fingerprint}, {y.ID, y.Generation, y.Fingerprint}}
			switch kind {
			case "generation":
				members[1].Generation = "wrong"
			case "fingerprint":
				members[1].Fingerprint = "wrong"
			case "expired":
				s.state.Sessions[y.ID].LastSeen = time.Now().Add(-time.Minute)
			case "restart":
				s, e = Open(s.path)
				must(t, e)
			}
			if s.Heartbeat(members) == nil {
				t.Fatal("invalid heartbeat accepted")
			}
			if !s.state.Sessions[x.ID].LastSeen.Equal(before) {
				t.Fatal("partial renewal")
			}
			if kind == "fingerprint" && s.state.Sessions[y.ID].State != "detached" {
				t.Fatal("wrong fingerprint not invalidated")
			}
		})
	}
}

func TestAgentDetachExactIdentity(t *testing.T) {
	s, x := setup(t)
	if s.AgentDetach(AgentMember{x.ID, x.Generation, "wrong"}) == nil {
		t.Fatal("wrong fingerprint accepted")
	}
	if s.state.Sessions[x.ID].State != "pending" {
		t.Fatal("incorrect detach mutated attachment")
	}
	must(t, s.AgentDetach(AgentMember{x.ID, x.Generation, x.Fingerprint}))
	if s.Heartbeat([]AgentMember{{x.ID, x.Generation, x.Fingerprint}}) == nil {
		t.Fatal("revived detached attachment")
	}
}

func TestHeartbeatWrongRoles(t *testing.T) {
	s, _ := setup(t)
	for _, token := range []string{"client-key", "admin-key"} {
		r := httptest.NewRequest("POST", "/v1/agent/heartbeat", strings.NewReader(`{"members":[]}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		Handler(s, Keys{Client: "client-key", Agent: "agent-key", Admin: "admin-key"}).ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
