package bridge

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func labelSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	b, err := json.Marshal(s.state)
	must(t, err)
	return string(b)
}

func TestNumberedTerminalLabelsAreStable(t *testing.T) {
	s, x := setup(t)
	first, err := s.RegisterNamed("numbered-instance-1", "tab-A", "company", "new-fingerprint-A", true)
	must(t, err)
	second, err := s.RegisterNamed("numbered-instance-2", "tab-B", "company", "new-fingerprint-B", true)
	must(t, err)
	if first.Name != "终端 01" || second.Name != "终端 02" || x.Name != "test" {
		t.Fatal(first.Name, second.Name)
	}
	must(t, s.SetSessionLabel(first.ID, first.Generation, "跳板机", "测试环境"))
	repeated, err := s.RegisterNamed(first.Instance, "tab-A", "company", first.Fingerprint, true)
	must(t, err)
	if repeated.ID != first.ID || repeated.Name != "跳板机" {
		t.Fatal("numbered registration changed resource")
	}
	s, err = Open(s.path)
	must(t, err)
	first, err = s.RegisterNamed("numbered-new-instance-1", "tab-A", "company", first.Fingerprint, true)
	must(t, err)
	third, err := s.RegisterNamed("numbered-new-instance-3", "tab-C", "company", "new-fingerprint-C", true)
	must(t, err)
	if first.Name != "跳板机" || first.Environment != "测试环境" || third.Name != "终端 03" {
		t.Fatal(first, third)
	}
}

func TestSessionLabelsStoreAPI(t *testing.T) {
	s, x := setup(t)
	must(t, s.SetSessionLabel(x.ID, x.Generation, "  renamed  ", "  production  "))
	got := s.Sessions()[0]
	if got.Name != "renamed" || got.Environment != "production" || got.State != "pending" || got.ID != x.ID || got.Generation != x.Generation || got.Fingerprint != x.Fingerprint || got.Instance != x.Instance || got.LastSeen != x.LastSeen {
		t.Fatalf("unexpected session: %+v", got)
	}
	retry, err := s.Register(x.Instance, x.Name, x.Environment, x.Fingerprint)
	must(t, err)
	if retry != got {
		t.Fatal("original registration retry was not idempotent")
	}
	b, err := os.ReadFile(s.path)
	must(t, err)
	var disk State
	must(t, json.Unmarshal(b, &disk))
	if len(disk.Labels) != 1 || disk.Labels[labelKey(x.Fingerprint)].Name != "renamed" {
		t.Fatal("label not persisted by fingerprint hash")
	}
	s, err = Open(s.path)
	must(t, err)
	y, err := s.Register("new-instance-123456", x.Name, x.Environment, x.Fingerprint)
	must(t, err)
	if y.Name != "renamed" || y.Environment != "production" || y.State != "pending" || y.Generation == x.Generation || y.ID == x.ID {
		t.Fatalf("bad reattachment: %+v", y)
	}
	z, err := s.Register("other-instance-123456", "other", "other-env", x.Fingerprint+" ")
	must(t, err)
	if z.Name != "other" || z.Environment != "other-env" {
		t.Fatal("different exact fingerprint inherited labels")
	}
	must(t, s.SetSessionLabel(z.ID, z.Generation, "separate", ""))
	if s.state.Labels[labelKey(y.Fingerprint)].Name != "renamed" || len(s.state.Labels) != 2 {
		t.Fatal("fingerprint labels were conflated")
	}
	before := labelSnapshot(t, s)
	if _, err := s.Register("third-instance-123456", strings.Repeat("a", 201), "", y.Fingerprint); err == nil {
		t.Fatal("persisted label bypassed incoming length validation")
	}
	if before != labelSnapshot(t, s) {
		t.Fatal("invalid registration mutated state")
	}
}

func TestSessionLabelsRejectWithoutMutation(t *testing.T) {
	s, x := setup(t)
	for _, tc := range []struct{ generation, name, env string }{
		{"stale", "valid", ""},
		{x.Generation, "   ", ""},
		{x.Generation, "bad\n", ""},
		{x.Generation, "valid", "bad\u0085"},
		{x.Generation, strings.Repeat("é", 101), ""},
		{x.Generation, "valid", strings.Repeat("a", 201)},
		{x.Generation, string([]byte{255}), ""},
		{x.Generation, "valid", string([]byte{255})},
	} {
		before := labelSnapshot(t, s)
		if err := s.SetSessionLabel(x.ID, tc.generation, tc.name, tc.env); err == nil {
			t.Fatal("invalid edit accepted")
		}
		if before != labelSnapshot(t, s) {
			t.Fatal("rejected edit mutated state")
		}
	}
	must(t, s.Detach(x.ID, x.Generation))
	before := labelSnapshot(t, s)
	if s.SetSessionLabel(x.ID, x.Generation, "valid", "") == nil || before != labelSnapshot(t, s) {
		t.Fatal("detached attachment edited")
	}
}

func TestSessionLabelsActiveJobsAndGrantsUnchanged(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, err := s.Submit(req(x))
	must(t, err)
	must(t, s.DecideRemember(j.ID, true, 30))
	_, err = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, err)
	jobs := s.Jobs()
	grants := append([]Grant(nil), s.state.Grants...)
	old := s.Sessions()[0]
	must(t, s.SetSessionLabel(x.ID, x.Generation, "active label", "new env"))
	if !reflect.DeepEqual(jobs, s.Jobs()) || !reflect.DeepEqual(grants, s.state.Grants) {
		t.Fatal("label edit changed job or grant")
	}
	got := s.Sessions()[0]
	got.Name, got.Environment = old.Name, old.Environment
	if got != old {
		t.Fatal("label edit changed attachment identity or status")
	}
}

func TestSessionLabelsHeartbeatAndRollback(t *testing.T) {
	s, x := setup(t)
	s.state.Sessions[x.ID].LastSeen = time.Now().Add(-time.Minute)
	if s.SetSessionLabel(x.ID, x.Generation, "expired", "") == nil || len(s.state.Labels) != 0 {
		t.Fatal("expired heartbeat allowed edit")
	}
	s, x = setup(t)
	before := labelSnapshot(t, s)
	path := s.path
	s.path = path + "/missing/state.json"
	if s.SetSessionLabel(x.ID, x.Generation, "rollback", "") == nil {
		t.Fatal("storage failure acknowledged")
	}
	if before != labelSnapshot(t, s) || s.state.Labels != nil {
		t.Fatal("storage failure did not roll back lazy map allocation")
	}
	s.path = path
	must(t, s.SetSessionLabel(x.ID, x.Generation, "retry", ""))
}

func TestSessionLabelsHTTP(t *testing.T) {
	s, x := setup(t)
	server := httptest.NewServer(Handler(s, Keys{Client: "client-token", Agent: "agent-token", Admin: "admin-token"}))
	defer server.Close()
	body, err := json.Marshal(map[string]string{"id": x.ID, "generation": x.Generation, "name": "HTTP label", "environment": "HTTP env"})
	must(t, err)
	for _, tc := range []struct {
		token, body string
		want        int
	}{
		{"client-token", string(body), 403},
		{"agent-token", string(body), 403},
		{"admin-token", strings.TrimSuffix(string(body), "}") + `,"unexpected":true}`, 400},
		{"admin-token", string(body), 200},
	} {
		r, err := http.NewRequest("POST", server.URL+"/v1/admin/session-label", strings.NewReader(tc.body))
		must(t, err)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		before := labelSnapshot(t, s)
		resp, err := server.Client().Do(r)
		must(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		must(t, err)
		if resp.StatusCode != tc.want {
			t.Fatalf("%s: got %d want %d", tc.token, resp.StatusCode, tc.want)
		}
		if tc.want != 200 && before != labelSnapshot(t, s) {
			t.Fatal("rejected HTTP request mutated state")
		}
	}
	for _, path := range []string{"/v1/sessions", "/v1/health"} {
		r, err := http.NewRequest("GET", server.URL+path, nil)
		must(t, err)
		r.Header.Set("Authorization", "Bearer client-token")
		resp, err := server.Client().Do(r)
		must(t, err)
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		must(t, err)
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
		if path == "/v1/sessions" {
			var sessions []Session
			must(t, json.Unmarshal(b, &sessions))
			if len(sessions) != 1 || sessions[0].Name != "HTTP label" || sessions[0].Environment != "HTTP env" || sessions[0].State != "pending" {
				t.Fatal(string(b))
			}
		} else {
			var health map[string]any
			must(t, json.Unmarshal(b, &health))
			if health["session_labels"] != true {
				t.Fatal(string(b))
			}
		}
	}
}
