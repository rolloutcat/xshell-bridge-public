package bridge

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shutdownStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestShutdownHTTPGuards(t *testing.T) {
	for _, tc := range []struct {
		method, token, body string
		configured          bool
		want                int
	}{
		{"POST", "", "{}", true, 401},
		{"POST", "client", "{}", true, 403},
		{"POST", "agent", "{}", true, 403},
		{"GET", "admin", "{}", true, 400},
		{"POST", "admin", "null", true, 400},
		{"POST", "admin", "[]", true, 400},
		{"POST", "admin", "{\"unexpected\":true}", true, 400},
		{"POST", "admin", "{} {}", true, 400},
		{"POST", "admin", "{}", false, 404},
		{"POST", "admin", "{}", true, 200},
	} {
		t.Run(tc.method+tc.token+tc.body, func(t *testing.T) {
			s := shutdownStore(t)
			calls := 0
			var callback func()
			if tc.configured {
				callback = func() { calls++ }
			}
			h := HandlerWithShutdown(s, Keys{Client: "client", Agent: "agent", Admin: "admin"}, callback)
			r := httptest.NewRequest(tc.method, "/v1/admin/shutdown", strings.NewReader(tc.body))
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			wantCalls := 0
			if tc.want == 200 {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("callback calls=%d", calls)
			}
		})
	}
}

func TestShutdownBusyDoesNotMutate(t *testing.T) {
	for _, state := range []string{"pending", "active"} {
		s := shutdownStore(t)
		s.state.Sessions["s"] = &Session{ID: "s", State: state}
		if s.BeginShutdown() == nil || s.stopping {
			t.Fatal("accepted busy session", state)
		}
		if s.state.Sessions["s"].State != state {
			t.Fatal("session changed")
		}
	}
	for _, status := range []string{"pending", "queued", "dispatched", "running"} {
		s := shutdownStore(t)
		s.state.Jobs["j"] = &Job{ID: "j", Status: status}
		if s.BeginShutdown() == nil || s.stopping {
			t.Fatal("accepted busy job", status)
		}
		if s.state.Jobs["j"].Status != status {
			t.Fatal("job changed")
		}
	}
}

func TestShutdownHistoricalUnknownAndWriteBarrier(t *testing.T) {
	s := shutdownStore(t)
	s.state.Jobs["j"] = &Job{ID: "j", Status: "unknown"}
	if err := s.BeginShutdown(); err != nil {
		t.Fatal(err)
	}
	executed := false
	if err := s.change(func() error { executed = true; return nil }); err == nil || executed {
		t.Fatal("change executed after shutdown")
	}
	if _, err := s.Register("abcdefghijklmnop", "name", "env", "fingerprint"); err == nil {
		t.Fatal("registration accepted")
	}
	if _, err := s.Submit(Submit{}); err == nil {
		t.Fatal("submission accepted")
	}
	if s.state.Jobs["j"].Status != "unknown" {
		t.Fatal("historical job changed")
	}
	if err := s.BeginShutdown(); err == nil {
		t.Fatal("shutdown replay accepted")
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "safe_shutdown") {
		t.Fatal("audit not persisted")
	}
}

func TestShutdownPersistenceFailureRollsBack(t *testing.T) {
	s := shutdownStore(t)
	count := len(s.state.Audit)
	s.path = filepath.Join(t.TempDir(), "missing", "state.json")
	if s.BeginShutdown() == nil {
		t.Fatal("expected persistence error")
	}
	if s.stopping || len(s.state.Audit) != count {
		t.Fatal("failed shutdown was not rolled back")
	}
	executed := false
	_ = s.change(func() error { executed = true; return nil })
	if !executed {
		t.Fatal("write barrier retained after failure")
	}
}

func TestShutdownHealthCapability(t *testing.T) {
	for _, configured := range []bool{false, true} {
		s := shutdownStore(t)
		var cb func()
		if configured {
			cb = func() {}
		}
		h := HandlerWithShutdown(s, Keys{Client: "client"}, cb)
		r := httptest.NewRequest("GET", "/v1/health", nil)
		r.Header.Set("Authorization", "Bearer client")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "\"safe_shutdown\":true") != configured {
			t.Fatal(w.Body.String())
		}
	}
}

func TestDesktopShutdownSettlesWithoutReplay(t *testing.T) {
	s := shutdownStore(t)
	s.state.Sessions["s"] = &Session{ID: "s", State: "active"}
	for _, status := range []string{"pending", "queued", "dispatched", "running", "completed", "unknown"} {
		s.state.Jobs[status] = &Job{ID: status, Submit: Submit{SessionID: "s"}, Status: status}
	}
	h := HandlerWithShutdown(s, Keys{Admin: "admin", Client: "client", Agent: "agent"}, func() {})
	for _, token := range []string{"client", "agent", "admin"} {
		r := httptest.NewRequest("POST", "/v1/admin/desktop-shutdown", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if token != "admin" {
			if w.Code != 403 || s.stopping {
				t.Fatal("shutdown authorization", w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("desktop shutdown: %d %s", w.Code, w.Body.String())
		}
	}
	if !s.stopping || s.state.Sessions["s"].State != "detached" {
		t.Fatal("not stopped")
	}
	for id, want := range map[string]string{"pending": "cancelled", "queued": "cancelled", "dispatched": "unknown", "running": "unknown", "completed": "completed", "unknown": "unknown"} {
		if s.state.Jobs[id].Status != want {
			t.Fatal(id, s.state.Jobs[id].Status)
		}
	}
	called := false
	if s.change(func() error { called = true; return nil }) == nil || called {
		t.Fatal("write barrier missing")
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.state.Jobs["running"].Status != "unknown" || reopened.state.Jobs["queued"].Status != "cancelled" {
		t.Fatal("restart replay")
	}
}

func TestDesktopShutdownRollback(t *testing.T) {
	s := shutdownStore(t)
	s.state.Sessions["s"] = &Session{ID: "s", State: "active"}
	s.state.Jobs["j"] = &Job{ID: "j", Submit: Submit{SessionID: "s"}, Status: "running"}
	s.path = filepath.Join(t.TempDir(), "missing", "state.json")
	h := HandlerWithShutdown(s, Keys{Admin: "admin"}, func() { t.Fatal("stopped without persistence") })
	r := httptest.NewRequest("POST", "/v1/admin/desktop-shutdown", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || s.stopping || s.state.Jobs["j"].Status != "running" || s.state.Sessions["s"].State != "active" {
		t.Fatal("rollback", w.Code)
	}
}
