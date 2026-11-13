package bridge

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Preview snapshots are constructed without invoking the mutation under test.
func removalItem(s *Store, x Session) map[string]any {
	jobs := []map[string]string{}
	for _, j := range s.Jobs() {
		if j.SessionID == x.ID {
			jobs = append(jobs, map[string]string{"id": j.ID, "status": j.Status})
		}
	}
	return map[string]any{"id": x.ID, "generation": x.Generation, "state": x.State, "fingerprint": x.Fingerprint, "name": x.Name, "environment": x.Environment, "jobs": jobs}
}
func removalHTTP(s *Store, method, path, token string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	Handler(s, Keys{Admin: "admin", Client: "client", Agent: "agent"}).ServeHTTP(w, r)
	return w
}
func removeHTTP(s *Store, stop bool, items ...map[string]any) *httptest.ResponseRecorder {
	return removalHTTP(s, "POST", "/v1/admin/remove-sessions", "admin", map[string]any{"stop_active": stop, "items": items})
}
func assertRemovalStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("got %d, want %d: %s", w.Code, status, w.Body.String())
	}
}
func TestRemovalHTTPAuthorizationFirst(t *testing.T) {
	s, x := setup(t)
	for _, token := range []string{"client", "agent", ""} {
		want := 403
		if token == "" {
			want = 401
		}
		for _, path := range []string{"/v1/admin/remove-sessions", "/v1/admin/sessions?include_archived=true"} {
			assertRemovalStatus(t, removalHTTP(s, "POST", path, token, map[string]any{}), want)
		}
	}
	assertRemovalStatus(t, removeHTTP(s, true, removalItem(s, x)), 200)
	if len(s.Sessions()) != 0 {
		t.Fatal("removed terminal is discoverable")
	}
}
func TestRemovalBusyAndAtomicSnapshot(t *testing.T) {
	for _, state := range []string{"active", "pending", "detached", "blocked", "expired"} {
		for _, status := range []string{"pending", "queued", "dispatched", "running"} {
			t.Run(state+"/"+status, func(t *testing.T) {
				s, x := setup(t)
				s.state.Sessions[x.ID].State = state
				x.State = state
				s.state.Jobs["busy"] = &Job{Submit: Submit{SessionID: x.ID}, ID: "busy", Status: status}
				s.state.Order = []string{"busy"}
				before, _ := json.Marshal(s.state)
				assertRemovalStatus(t, removeHTTP(s, true, removalItem(s, x)), 409)
				after, _ := json.Marshal(s.state)
				if string(before) != string(after) {
					t.Fatal("busy removal changed state")
				}
			})
		}
	}
	for _, change := range []string{"generation", "state", "name", "environment", "fingerprint", "new-job", "job-status", "removed"} {
		t.Run(change, func(t *testing.T) {
			s, a := setup(t)
			must(t, s.Detach(a.ID, a.Generation))
			a.State = "detached"
			b, e := s.Register("second-instance-123456", "second", "isolated", "fingerprint-2")
			must(t, e)
			must(t, s.Detach(b.ID, b.Generation))
			b.State = "detached"
			s.state.Jobs["history"] = &Job{Submit: Submit{SessionID: b.ID}, ID: "history", Status: "unknown"}
			s.state.Order = []string{"history"}
			items := []map[string]any{removalItem(s, a), removalItem(s, b)}
			switch change {
			case "generation":
				s.state.Sessions[b.ID].Generation = "changed"
			case "state":
				s.state.Sessions[b.ID].State = "active"
			case "name":
				s.state.Sessions[b.ID].Name = "changed"
			case "environment":
				s.state.Sessions[b.ID].Environment = "changed"
			case "fingerprint":
				s.state.Sessions[b.ID].Fingerprint = "changed"
			case "new-job":
				s.state.Jobs["new"] = &Job{Submit: Submit{SessionID: b.ID}, ID: "new", Status: "pending"}
				s.state.Order = append(s.state.Order, "new")
			case "job-status":
				s.state.Jobs["history"].Status = "completed"
			case "removed":
				assertRemovalStatus(t, removeHTTP(s, false, items[1]), 200)
			}
			before, _ := json.Marshal(s.state)
			assertRemovalStatus(t, removeHTTP(s, false, items...), 409)
			after, _ := json.Marshal(s.state)
			if string(before) != string(after) {
				t.Fatal("partial batch changed state")
			}
		})
	}
}
func TestRemovalBoundsAndActiveBatch(t *testing.T) {
	s, x := setup(t)
	item := removalItem(s, x)
	assertRemovalStatus(t, removeHTTP(s, false, item), 409)
	// A stale heartbeat alone never makes an active record eligible for cleanup.
	s.state.Sessions[x.ID].State = "active"
	s.state.Sessions[x.ID].LastSeen = time.Now().Add(-time.Hour)
	x.State = "active"
	item = removalItem(s, x)
	assertRemovalStatus(t, removeHTTP(s, false, item), 409)
	assertRemovalStatus(t, removeHTTP(s, false, item, item), 400)
	assertRemovalStatus(t, removeHTTP(s, false), 400)
	items := make([]map[string]any, 101)
	for i := range items {
		items[i] = item
	}
	assertRemovalStatus(t, removeHTTP(s, false, items...), 400)
	assertRemovalStatus(t, removalHTTP(s, "POST", "/v1/admin/remove-sessions", "admin", map[string]any{"items": []any{item}}), 400)
	assertRemovalStatus(t, removalHTTP(s, "GET", "/v1/admin/remove-sessions", "admin", nil), 400)
	b, e := s.Register("second-instance-123456", "second", "isolated", "fingerprint-2")
	must(t, e)
	assertRemovalStatus(t, removeHTTP(s, true, item, removalItem(s, b)), 400)
	if len(s.Sessions()) != 2 {
		t.Fatal("invalid batch changed membership")
	}
}
func TestRemovalPreservesHistoryAndPreventsResurrection(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	must(t, s.SetSessionLabel(x.ID, x.Generation, "历史名称", "历史分组"))
	x = s.Sessions()[0]
	for i, status := range []string{"unknown", "completed", "denied", "cancelled"} {
		id := strings.Repeat(string(rune('a'+i)), 48)
		s.state.Jobs[id] = &Job{Submit: Submit{SessionID: x.ID, Generation: x.Generation, RequestID: id, Command: "printf test", TimeoutMS: 10000}, ID: id, Status: status, Output: "历史输出\n", Reason: "retained"}
		s.state.Order = append(s.state.Order, id)
	}
	jobs, labels := s.Jobs(), s.state.Labels
	item := removalItem(s, x)
	assertRemovalStatus(t, removeHTTP(s, true, item), 200)
	assertRemovalStatus(t, removeHTTP(s, true, item), 409)
	for _, store := range []*Store{s, func() *Store { r, e := Open(s.path); must(t, e); return r }()} {
		if len(store.Sessions()) != 0 {
			t.Fatal("archived session returned after reload")
		}
		if !reflect.DeepEqual(jobs, store.Jobs()) || !reflect.DeepEqual(labels, store.state.Labels) {
			t.Fatal("history or labels erased")
		}
		assertRemovalStatus(t, removalHTTP(store, "GET", "/v1/sessions", "client", nil), 200)
		w := removalHTTP(store, "GET", "/v1/admin/sessions?include_archived=true", "admin", nil)
		assertRemovalStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), "历史名称") || !strings.Contains(w.Body.String(), `"archived":true`) {
			t.Fatal("history identity absent")
		}
		if p, e := store.Poll(x.ID, x.Generation, x.Fingerprint); e == nil || p.Job != nil {
			t.Fatal("removed member polled")
		}
		if e := store.Heartbeat([]AgentMember{{x.ID, x.Generation, x.Fingerprint}}); e == nil {
			t.Fatal("removed member renewed")
		}
		if e := store.Activate(x.ID, x.Generation); e == nil {
			t.Fatal("removed member activated")
		}
		if _, e := store.Register(x.Instance, x.Name, x.Environment, x.Fingerprint); e == nil {
			t.Fatal("old instance re-registered")
		}
		r := req(x)
		r.RequestID = "new"
		if _, e := store.Submit(r); e == nil {
			t.Fatal("removed member submitted")
		}
		// Existing idempotency remains a history lookup; it cannot create/replay work.
		old := jobs[0]
		retried, e := store.Submit(old.Submit)
		must(t, e)
		if !reflect.DeepEqual(retried, old) {
			t.Fatal("idempotency changed history")
		}
		w = removalHTTP(store, "GET", "/v1/output?id="+old.ID, "client", nil)
		assertRemovalStatus(t, w, 200)
		if !strings.Contains(w.Body.String(), "历史输出") {
			t.Fatal("output lost")
		}
		if len(store.Sessions()) != 0 {
			t.Fatal("old agent resurrected record")
		}
	}
	fresh, e := s.Register("new-manual-instance-12345", "new", "isolated", x.Fingerprint)
	must(t, e)
	if fresh.ID == x.ID || fresh.Generation == x.Generation || fresh.Name != "历史名称" {
		t.Fatal("new attachment reused identity or lost label")
	}
}
func TestRemovalConcurrentSubmission(t *testing.T) {
	for i := 0; i < 20; i++ {
		s, x := setup(t)
		must(t, s.Activate(x.ID, x.Generation))
		x = s.Sessions()[0]
		item := removalItem(s, x)
		var wg sync.WaitGroup
		wg.Add(2)
		var removed int
		var submitErr error
		go func() { defer wg.Done(); removed = removeHTTP(s, true, item).Code }()
		go func() { defer wg.Done(); _, submitErr = s.Submit(req(x)) }()
		wg.Wait()
		if removed == 200 {
			if submitErr == nil || len(s.Jobs()) != 0 || len(s.Sessions()) != 0 {
				t.Fatal("job created across removal")
			}
		} else if removed != 409 || submitErr != nil || len(s.Sessions()) != 1 {
			t.Fatal("neither operation completed consistently")
		}
	}
}
func TestRemovalStorageFailureRollsBack(t *testing.T) {
	s, x := setup(t)
	item := removalItem(s, x)
	s.path = s.path + "/missing/state.json"
	assertRemovalStatus(t, removeHTTP(s, true, item), 500)
	if len(s.Sessions()) != 1 || s.Sessions()[0].State != "pending" {
		t.Fatal("failed persistence archived session")
	}
}

func TestRemovalInactiveBatchAndStrictHTTP(t *testing.T) {
	s, a := setup(t)
	items := []map[string]any{}
	for i, state := range []string{"detached", "blocked", "expired"} {
		x := a
		if i > 0 {
			var e error
			x, e = s.Register("batch-instance-123456"+state, state, "isolated", "fingerprint-"+state)
			must(t, e)
		}
		must(t, s.Detach(x.ID, x.Generation))
		s.state.Sessions[x.ID].State = state
		x.State = state
		items = append(items, removalItem(s, x))
	}
	assertRemovalStatus(t, removeHTTP(s, false, items...), 200)
	if len(s.Sessions()) != 0 || len(s.sessions(true)) != 3 {
		t.Fatal("batch did not archive exactly its members")
	}
	for _, path := range []string{"/v1/sessions", "/v1/sessions?include_archived=true"} {
		w := removalHTTP(s, "GET", path, "client", nil)
		assertRemovalStatus(t, w, 200)
		if strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatal("discovery exposed archived records")
		}
	}
	for _, path := range []string{"/v1/admin/sessions", "/v1/admin/sessions?include_archived=false", "/v1/admin/sessions?include_archived=true&extra=true"} {
		assertRemovalStatus(t, removalHTTP(s, "GET", path, "admin", nil), 400)
	}
	assertRemovalStatus(t, removalHTTP(s, "POST", "/v1/admin/remove-sessions", "admin", map[string]any{"stop_active": false, "items": items, "extra": true}), 400)
}

func TestRemovalSourceBoundaryAndOldEvents(t *testing.T) {
	s, _ := setup(t)
	fp := `{"SourceID":"source-A","terminal":"isolated"}`
	x, e := s.Register("source-instance-123456", "source", "test", fp)
	must(t, e)
	assertRemovalStatus(t, removeHTTP(s, true, removalItem(s, x)), 200)
	must(t, s.CheckAgentSource("source-A", x.ID))
	if e := s.CheckAgentSource("source-B", x.ID); e == nil {
		t.Fatal("archival weakened source ownership")
	}
	if e := s.Event(Event{SessionID: x.ID, Generation: x.Generation, Fingerprint: fp, JobID: "old", Seq: 1, Status: "running"}); e == nil {
		t.Fatal("archived member reported event")
	}
	// Existing detach APIs retain their scope, but can never unarchive a record.
	must(t, s.AgentDetach(AgentMember{x.ID, x.Generation, fp}))
	must(t, s.Detach(x.ID, x.Generation))
	if len(s.Sessions()) != 1 {
		t.Fatal("old detach resurrected archived session")
	}
}
