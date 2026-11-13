package bridge

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Isolated Bridge handler/store smoke: no service, runtime configuration or Xshell.
func TestCaptureUnknownBlocksSourceLifecycle(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	must(t, err)
	const source = "0123456789abcdef0123456789abcdef"
	const master = "isolated-master"
	token, err := SourceAgentToken(master, source)
	must(t, err)
	other, err := SourceAgentToken(master, "fedcba9876543210fedcba9876543210")
	must(t, err)
	h := Handler(s, Keys{Agent: master, Client: "client", Admin: "admin"})
	call := func(path, key string, body any, want int) []byte {
		t.Helper()
		b, err := json.Marshal(body)
		must(t, err)
		r := httptest.NewRequest("POST", path, strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: status %d, want %d: %s", path, w.Code, want, w.Body.String())
		}
		return w.Body.Bytes()
	}
	fp := `{"SourceID":"` + source + `","Terminal":"fixture"}`
	data := call("/v1/agent/register", token, map[string]any{"instance": "fixture-instance-12345", "name": "fixture", "environment": "isolated", "fingerprint": fp}, 200)
	var session Session
	must(t, json.Unmarshal(data, &session))
	must(t, s.Activate(session.ID, session.Generation))
	request := Submit{session.ID, session.Generation, "request-a", "fixture", "", 10000}
	first, err := s.Submit(request)
	must(t, err)
	must(t, s.Decide(first.ID, true))
	request.RequestID = "request-b"
	second, err := s.Submit(request)
	must(t, err)
	must(t, s.Decide(second.ID, true))
	request.RequestID = "request-c"
	third, err := s.Submit(request)
	must(t, err)
	must(t, s.Decide(third.ID, true))
	member := AgentMember{ID: session.ID, Generation: session.Generation, Fingerprint: fp}
	data = call("/v1/agent/poll", token, member, 200)
	var dispatched PollResult
	must(t, json.Unmarshal(data, &dispatched))
	if dispatched.Job == nil || dispatched.Job.ID != first.ID {
		t.Fatal("first job not dispatched")
	}
	call("/v1/agent/poll", other, member, 403)
	event := Event{SessionID: session.ID, Generation: session.Generation, Fingerprint: fp, JobID: first.ID, Seq: 1, Status: "running", Output: "captured\n"}
	call("/v1/agent/event", other, event, 403)
	call("/v1/agent/event", token, event, 200)
	code := 7
	event.Seq = 2
	event.Status = "completed"
	event.Output = ""
	event.ExitCode = &code
	call("/v1/agent/event", token, event, 200)
	got, err := s.GetJob(first.ID)
	must(t, err)
	if got.Status != "completed" || got.Output != "captured\n" || got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("completed job: %+v", got)
	}
	data = call("/v1/agent/poll", token, member, 200)
	must(t, json.Unmarshal(data, &dispatched))
	if dispatched.Job == nil || dispatched.Job.ID != second.ID {
		t.Fatal("second job not dispatched")
	}
	event.JobID = second.ID
	event.Seq = 1
	event.Status = "running"
	event.ExitCode = nil
	event.Output = "preserved\n"
	call("/v1/agent/event", token, event, 200)
	event.Seq = 2
	event.Status = "unknown"
	event.Output = ""
	event.Reason = "log truncated; capture cannot confirm completion"
	call("/v1/agent/event", token, event, 200)
	got, err = s.GetJob(second.ID)
	must(t, err)
	if got.Status != "unknown" || got.Output != "preserved\n" {
		t.Fatalf("lost output or status: %+v", got)
	}
	queued, err := s.GetJob(third.ID)
	must(t, err)
	if queued.Status != "cancelled" {
		t.Fatalf("queued job: %s", queued.Status)
	}
	if s.state.Sessions[session.ID].State != "blocked" {
		t.Fatal("attachment not blocked")
	}
	if _, err = s.Poll(session.ID, session.Generation, fp); err == nil {
		t.Fatal("blocked attachment polled successfully")
	}
	code = 0
	event.Seq = 3
	event.Status = "completed"
	event.ExitCode = &code
	call("/v1/agent/event", token, event, 409)
}
