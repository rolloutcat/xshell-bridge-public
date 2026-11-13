package bridge

import (
	"testing"
	"time"
)

func grantFixture(t *testing.T) (*Store, Session, Submit) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	r := req(x)
	j, e := s.Submit(r)
	must(t, e)
	must(t, s.DecideRemember(j.ID, true, 30))
	must(t, s.Interrupt(j.ID))
	return s, x, r
}
func TestRememberOnlyExactAttachmentAndCommand(t *testing.T) {
	s, x, r := grantFixture(t)
	for i, change := range []func(*Submit){func(v *Submit) { v.Command += "; id" }, func(v *Submit) { v.Cwd = "/tmp" }, func(v *Submit) { v.TimeoutMS++ }} {
		v := r
		v.RequestID = string(rune('a' + i))
		change(&v)
		j, e := s.Submit(v)
		must(t, e)
		if j.Status != "pending" {
			t.Fatal("broadened grant", j)
		}
	}
	r.RequestID = "exact"
	j, e := s.Submit(r)
	must(t, e)
	if j.Status != "queued" || j.GrantID == "" {
		t.Fatal("exact grant not used")
	}
	other, e := s.Register("other-instance-123456", x.Name, x.Environment, "other-fingerprint")
	must(t, e)
	must(t, s.Activate(other.ID, other.Generation))
	r.SessionID = other.ID
	r.Generation = other.Generation
	r.RequestID = "other"
	j, e = s.Submit(r)
	must(t, e)
	if j.Status != "pending" {
		t.Fatal("cross attachment grant")
	}
}
func TestExpiredAndRevokedGrantsDoNotDispatch(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		s, x, r := grantFixture(t)
		r.RequestID = "future"
		j, e := s.Submit(r)
		must(t, e)
		if revoke {
			must(t, s.RevokeGrant(j.GrantID))
		} else {
			s.state.Grants[0].Expires = time.Now().Add(-time.Second)
		}
		p, e := s.Poll(x.ID, x.Generation, x.Fingerprint)
		must(t, e)
		if p.Job != nil {
			t.Fatal("expired grant dispatched")
		}
		j, e = s.GetJob(j.ID)
		must(t, e)
		if j.Status != "pending" {
			t.Fatal(j.Status)
		}
	}
}
func TestGrantLostOnDetachAndRestart(t *testing.T) {
	s, x, _ := grantFixture(t)
	must(t, s.Detach(x.ID, x.Generation))
	if len(s.Grants()) != 0 {
		t.Fatal("detach retained grant")
	}
	s, _, _ = grantFixture(t)
	restarted, e := Open(s.path)
	must(t, e)
	if len(restarted.Grants()) != 0 {
		t.Fatal("restart retained grant")
	}
}
func TestRememberRequiresValidDurationAndAllow(t *testing.T) {
	s, x := setup(t)
	must(t, s.Activate(x.ID, x.Generation))
	j, e := s.Submit(req(x))
	must(t, e)
	for _, minutes := range []int{-1, 1, 31} {
		if s.DecideRemember(j.ID, true, minutes) == nil {
			t.Fatal("bad duration")
		}
	}
	if s.DecideRemember(j.ID, false, 30) == nil {
		t.Fatal("deny created grant")
	}
	j, e = s.GetJob(j.ID)
	must(t, e)
	if j.Status != "pending" || len(s.Grants()) != 0 {
		t.Fatal("invalid decision mutated state")
	}
}
