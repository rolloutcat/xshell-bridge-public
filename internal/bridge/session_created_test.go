package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Read through JSON too: this metadata must survive the API and state file.
func sessionCreated(t *testing.T, x Session) time.Time {
	t.Helper()
	b, err := json.Marshal(x)
	must(t, err)
	var fields map[string]json.RawMessage
	must(t, json.Unmarshal(b, &fields))
	var created time.Time
	if len(fields["created_at"]) != 0 {
		must(t, json.Unmarshal(fields["created_at"], &created))
	}
	return created
}

func TestSessionCreationImmutableAndPersisted(t *testing.T) {
	before := time.Now().UTC()
	s, x := setup(t)
	created := sessionCreated(t, x)
	if created.IsZero() || created.Before(before) || created.After(time.Now().UTC()) {
		t.Fatal("new registration has no creation timestamp")
	}
	assertSame := func(x Session) {
		t.Helper()
		if !sessionCreated(t, x).Equal(created) || x.ID != s.Sessions()[0].ID || x.Generation != s.Sessions()[0].Generation || x.Fingerprint != "fingerprint-1" {
			t.Fatal("display metadata changed attachment identity or creation time")
		}
	}
	retry, err := s.Register(x.Instance, x.Name, x.Environment, x.Fingerprint)
	must(t, err)
	assertSame(retry)
	must(t, s.Activate(x.ID, x.Generation))
	must(t, s.SetSessionLabel(x.ID, x.Generation, "new name", "new group"))
	must(t, s.Heartbeat([]AgentMember{{x.ID, x.Generation, x.Fingerprint}}))
	_, err = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, err)
	assertSame(s.Sessions()[0])
	w := removalHTTP(s, "GET", "/v1/sessions", "client", nil)
	assertRemovalStatus(t, w, 200)
	var api []Session
	must(t, json.Unmarshal(w.Body.Bytes(), &api))
	assertSame(api[0])
	reloaded, err := Open(s.path)
	must(t, err)
	assertSame(reloaded.Sessions()[0])
	assertRemovalStatus(t, removeHTTP(reloaded, false, removalItem(reloaded, reloaded.Sessions()[0])), 200)
	assertSame(reloaded.sessions(true)[0])
	manual, err := reloaded.Register("new-manual-instance-123", "new", "new", x.Fingerprint)
	must(t, err)
	if !sessionCreated(t, manual).After(created) || manual.ID == x.ID || manual.Generation == x.Generation {
		t.Fatal("manual reattachment must have its own creation time and identity")
	}
}

func TestSessionLegacyCreationRemainsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	// No created_at: opening/restarting must never invent an attachment date.
	must(t, os.WriteFile(path, []byte(`{"sessions":{"legacy":{"id":"legacy","state":"detached","last_seen":"2020-01-01T23:59:59Z"}},"jobs":{}}`), 0600))
	s, err := Open(path)
	must(t, err)
	x := s.Sessions()[0]
	if !sessionCreated(t, x).IsZero() || x.LastSeen.Format(time.RFC3339) != "2020-01-01T23:59:59Z" {
		t.Fatal("legacy attachment date was fabricated or fallback was changed")
	}
}

// Optional synthetic Go -> native -> browser evidence. Handler is in-process;
// no live Bridge, credentials, listener or private runtime is involved.
func TestRemovalHandshakeFixture(t *testing.T) {
	s, _ := setup(t)
	s.state.Sessions = map[string]*Session{}
	for i, state := range []string{"blocked", "detached", "expired", "active", "pending", "blocked"} {
		x, err := s.Register("isolated-fixture-instance-"+state+string(rune('a'+i)), "fixture <"+state+">", "synthetic", "fixture-fingerprint-"+string(rune('a'+i)))
		must(t, err)
		s.state.Sessions[x.ID].State = state
		if i == 0 {
			jobID := ID()
			s.state.Jobs[jobID] = &Job{Submit: Submit{SessionID: x.ID, Generation: x.Generation, Command: "printf fixture", TimeoutMS: 1000}, ID: jobID, Status: "unknown", Output: "synthetic history output"}
			s.state.Order = []string{jobID}
		}
	}
	health := removalHTTP(s, "GET", "/v1/health", "client", nil)
	assertRemovalStatus(t, health, 200)
	var capabilities map[string]any
	must(t, json.Unmarshal(health.Body.Bytes(), &capabilities))
	if capabilities["session_removal"] != true || capabilities["session_labels"] != true {
		t.Fatal("health must advertise independent boolean removal and label capabilities")
	}
	if path := os.Getenv("XSH_REMOVAL_GO_FIXTURE"); path != "" {
		body, err := json.Marshal(map[string]any{"health": capabilities, "sessions": s.Sessions(), "jobs": s.Jobs()})
		must(t, err)
		must(t, os.WriteFile(path, body, 0600))
	}
	items := []map[string]any{}
	for _, x := range s.Sessions() {
		if inactiveSession(x.State) {
			items = append(items, removalItem(s, x))
		}
	}
	jobs := s.Jobs()
	assertRemovalStatus(t, removeHTTP(s, false, items...), 200)
	if len(items) != 4 || len(s.Sessions()) != 2 || len(s.sessions(true)) != 6 || !reflect.DeepEqual(jobs, s.Jobs()) {
		t.Fatal("six-terminal cleanup lost history or changed active/pending membership")
	}
	output := removalHTTP(s, "GET", "/v1/output?id="+jobs[0].ID, "client", nil)
	assertRemovalStatus(t, output, 200)
	if !strings.Contains(output.Body.String(), "synthetic history output") {
		t.Fatal("inactive unknown history output lost after cleanup")
	}
}
