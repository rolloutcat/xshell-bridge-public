package bridge

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func batchFixture(t *testing.T) (*Store, BatchDecision) {
	t.Helper()
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	b := BatchDecision{Allow: true}
	for _, id := range []string{"one", "two", "unselected"} {
		r := req(x)
		r.RequestID = id
		j, e := s.Submit(r)
		must(t, e)
		if id != "unselected" {
			b.Items = append(b.Items, BatchItem{ID: j.ID, SessionID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint, Name: x.Name, Environment: x.Environment, Command: j.Command, Cwd: j.Cwd, TimeoutMS: j.TimeoutMS})
		}
	}
	return s, b
}
func stateJSON(t *testing.T, s *Store) string {
	t.Helper()
	b, e := json.Marshal(s.state)
	must(t, e)
	return string(b)
}

func TestBatchPreflightAtomicity(t *testing.T) {
	for _, mutate := range []func(*Store, *BatchDecision){
		func(s *Store, b *BatchDecision) { b.Items = nil },
		func(s *Store, b *BatchDecision) {
			for len(b.Items) < 21 {
				b.Items = append(b.Items, b.Items[0])
			}
		},
		func(s *Store, b *BatchDecision) { b.Items[1] = b.Items[0] },
		func(s *Store, b *BatchDecision) { b.Items[1].ID = "missing" },
		func(s *Store, b *BatchDecision) { b.Items[1].Name = "changed" },
		func(s *Store, b *BatchDecision) { b.Items[1].Environment = "changed" },
		func(s *Store, b *BatchDecision) { b.Items[1].Command = "changed" },
		func(s *Store, b *BatchDecision) { b.Items[1].Cwd = "changed" },
		func(s *Store, b *BatchDecision) { b.Items[1].TimeoutMS++ },
		func(s *Store, b *BatchDecision) { b.Items[1].Fingerprint = "changed" },
		func(s *Store, b *BatchDecision) { b.Items[1].Generation = "changed" },
		func(s *Store, b *BatchDecision) { s.state.Jobs[b.Items[1].ID].Status = "queued" },
		func(s *Store, b *BatchDecision) {
			s.state.Sessions[b.Items[1].SessionID].LastSeen = time.Now().Add(-time.Minute)
		},
		func(s *Store, b *BatchDecision) { s.state.Sessions[b.Items[1].SessionID].State = "detached" },
	} {
		s, b := batchFixture(t)
		mutate(s, &b)
		before := stateJSON(t, s)
		if s.DecideBatch(b) == nil {
			t.Fatal("invalid batch accepted")
		}
		if stateJSON(t, s) != before {
			t.Fatal("failed preflight mutated state or audit")
		}
	}
}
func TestBatchCommitReplayAndStorage(t *testing.T) {
	for _, allow := range []bool{true, false} {
		s, b := batchFixture(t)
		b.Allow = allow
		must(t, s.DecideBatch(b))
		for _, j := range s.Jobs() {
			want := "pending"
			if j.RequestID != "unselected" {
				want = "denied"
				if allow {
					want = "queued"
				}
			}
			if j.Status != want {
				t.Fatal(j)
			}
		}
		before := stateJSON(t, s)
		if s.DecideBatch(b) == nil || stateJSON(t, s) != before {
			t.Fatal("replay changed state")
		}
		if len(s.state.Grants) != 0 {
			t.Fatal("batch created authority")
		}
	}
	s, b := batchFixture(t)
	before := stateJSON(t, s)
	s.path = filepath.Join(s.path, "missing", "state.json")
	if s.DecideBatch(b) == nil || stateJSON(t, s) != before {
		t.Fatal("storage rollback failed")
	}
}
func TestApprovalEndpointFailures(t *testing.T) {
	s, _ := batchFixture(t)
	h := Handler(s, Keys{Client: "c", Agent: "a", Admin: "o"})
	for _, tc := range []struct {
		path, token, body string
		code              int
	}{
		{"/v1/admin/decide-batch", "c", "{}", 403},
		{"/v1/admin/decide-batch", "a", "{}", 403},
		{"/v1/explain", "a", "{}", 403},
		{"/v1/admin/decide-batch", "o", `{"unknown":true}`, 400},
		{"/v1/admin/decide-batch", "o", `null`, 400},
		{"/v1/admin/decide-batch", "o", `{} {}`, 400},
		{"/v1/explain", "c", `{"request_id":"forbidden"}`, 400},
		{"/v1/explain", "o", `{`, 400},
	} {
		before := stateJSON(t, s)
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code || stateJSON(t, s) != before {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}
func TestExplainBlockedAndReadOnly(t *testing.T) {
	for _, mode := range []string{"inactive", "expired", "generation", "gone"} {
		s, x := setup(t)
		r := ExplainRequest{SessionID: x.ID, Generation: x.Generation, Command: "printf test", TimeoutMS: 10000}
		switch mode {
		case "expired":
			s.state.Sessions[x.ID].LastSeen = time.Now().Add(-time.Minute)
		case "generation":
			r.Generation = "wrong"
		case "gone":
			delete(s.state.Sessions, x.ID)
		}
		before := stateJSON(t, s)
		disk, e := os.ReadFile(s.path)
		must(t, e)
		out, e := s.Explain(r)
		must(t, e)
		after, e := os.ReadFile(s.path)
		must(t, e)
		if out.Decision != "blocked" || before != stateJSON(t, s) || string(disk) != string(after) {
			t.Fatal(mode, out)
		}
	}
}
