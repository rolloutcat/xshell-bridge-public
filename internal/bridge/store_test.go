package bridge

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func setup(t *testing.T) (*Store, Session) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.Register("instance-123456789", "test", "isolated", "fingerprint-1")
	if err != nil {
		t.Fatal(err)
	}
	return s, x
}
func req(x Session) Submit { return Submit{x.ID, x.Generation, "request-1", "printf test", "", 10000} }
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

func TestNoDispatchBeforeBothApprovals(t *testing.T) {
	s, x := setup(t)
	if _, e := s.Submit(req(x)); e == nil {
		t.Fatal("unapproved session accepted")
	}
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	p, e := s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	if p.Job != nil {
		t.Fatal("unapproved command dispatched")
	}
	must(t, s.Decide(j.ID, true))
	p, e = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	if p.Job == nil || p.Job.Status != "dispatched" {
		t.Fatal("approved job missing")
	}
	p, e = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	if p.Job != nil {
		t.Fatal("job replayed")
	}
}
func TestIdempotencyAndChangedPayload(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	a, e := s.Submit(r)
	must(t, e)
	b, e := s.Submit(r)
	must(t, e)
	if a.ID != b.ID {
		t.Fatal("duplicate job")
	}
	r.Command = "different"
	if _, e = s.Submit(r); e == nil {
		t.Fatal("changed payload accepted")
	}
}
func TestFingerprintChangeRevokesQueuedWork(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	must(t, s.Decide(j.ID, true))
	if _, e = s.Poll(x.ID, x.Generation, "different"); e == nil {
		t.Fatal("target changed silently")
	}
	j, e = s.GetJob(j.ID)
	must(t, e)
	if j.Status != "cancelled" {
		t.Fatal(j.Status)
	}
}
func TestRestartNeverReplaysAndRequiresReattach(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	must(t, s.Decide(j.ID, true))
	_, e = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	s2, e := Open(s.path)
	must(t, e)
	j, e = s2.GetJob(j.ID)
	must(t, e)
	if j.Status != "unknown" {
		t.Fatal(j.Status)
	}
	if _, e = s2.Register(x.Instance, x.Name, x.Environment, x.Fingerprint); e == nil {
		t.Fatal("old attachment resurrected")
	}
}
func TestStaleHeartbeatBlocksLateCompletion(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	must(t, s.Decide(j.ID, true))
	_, e = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	must(t, s.Expire(time.Now().Add(time.Minute)))
	code := 0
	if e = s.Event(Event{x.ID, x.Generation, x.Fingerprint, j.ID, 1, "", "completed", &code, ""}); e == nil {
		t.Fatal("late result restored expired attachment")
	}
	j, e = s.GetJob(j.ID)
	must(t, e)
	if j.Status != "unknown" {
		t.Fatal(j.Status)
	}
}
func TestSequentialDispatchAndEventDedup(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	a, e := s.Submit(req(x))
	must(t, e)
	must(t, s.Decide(a.ID, true))
	r := req(x)
	r.RequestID = "request-2"
	b, e := s.Submit(r)
	must(t, e)
	must(t, s.Decide(b.ID, true))
	_, e = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	p, e := s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	if p.Job != nil {
		t.Fatal("concurrent dispatch")
	}
	v := Event{x.ID, x.Generation, x.Fingerprint, a.ID, 1, "hello", "running", nil, ""}
	must(t, s.Event(v))
	must(t, s.Event(v))
	v.Output = "changed"
	if s.Event(v) == nil {
		t.Fatal("changed duplicate event")
	}
	j, e := s.GetJob(a.ID)
	must(t, e)
	if j.Output != "hello" {
		t.Fatal(j.Output)
	}
	code := 7
	v = Event{x.ID, x.Generation, x.Fingerprint, a.ID, 2, " world", "completed", &code, ""}
	must(t, s.Event(v))
	must(t, s.Event(v))
	p, e = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	if p.Job == nil || p.Job.ID != b.ID {
		t.Fatal("next job missing")
	}
}

func TestSecondAgentCannotAttachSameTerminal(t *testing.T) {
	s, x := setup(t)
	if _, e := s.Register("second-instance-12345", x.Name, x.Environment, x.Fingerprint); e == nil {
		t.Fatal("two agents accepted for one terminal")
	}
	must(t, s.Detach(x.ID, x.Generation))
	if _, e := s.Register("second-instance-12345", x.Name, x.Environment, x.Fingerprint); e != nil {
		t.Fatal(e)
	}
}

func TestOutputCapKeepsUTF8AndReportsTruncation(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	must(t, s.Decide(j.ID, true))
	_, e = s.Poll(x.ID, x.Generation, x.Fingerprint)
	must(t, e)
	for i := 1; i <= 36; i++ {
		must(t, s.Event(Event{x.ID, x.Generation, x.Fingerprint, j.ID, i, strings.Repeat("中", 10000), "running", nil, ""}))
	}
	j, e = s.GetJob(j.ID)
	must(t, e)
	if !j.Truncated || len(j.Output) > MaxOutput || !utf8.ValidString(j.Output) {
		t.Fatal("invalid capped output")
	}
}

func TestFailedPersistenceDoesNotAcknowledgeDispatch(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	must(t, s.Decide(j.ID, true))
	s.path = filepath.Join(s.path, "missing", "state.json")
	if _, e = s.Poll(x.ID, x.Generation, x.Fingerprint); e == nil {
		t.Fatal("dispatch acknowledged without durable record")
	}
	j, e = s.GetJob(j.ID)
	must(t, e)
	if j.Status != "queued" {
		t.Fatal(j.Status)
	}
}
