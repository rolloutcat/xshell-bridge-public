package bridge

import "time"

func (g Grant) matches(r Submit) bool {
	return g.SessionID == r.SessionID && g.Generation == r.Generation && g.Command == r.Command && g.Cwd == r.Cwd && g.TimeoutMS == r.TimeoutMS
}
func (s *Store) validGrant(j *Job) bool {
	for _, g := range s.state.Grants {
		if g.ID == j.GrantID && g.matches(j.Submit) && time.Now().Before(g.Expires) {
			return true
		}
	}
	return false
}
func (s *Store) Grants() []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Grant{}
	for _, g := range s.state.Grants {
		x := s.state.Sessions[g.SessionID]
		if x != nil && x.State == "active" && x.Generation == g.Generation && time.Since(x.LastSeen) <= 15*time.Second && time.Now().Before(g.Expires) {
			out = append(out, g)
		}
	}
	return out
}
func (s *Store) revokeGrant(id string) bool {
	for i, g := range s.state.Grants {
		if g.ID != id {
			continue
		}
		s.state.Grants = append(s.state.Grants[:i], s.state.Grants[i+1:]...)
		for _, j := range s.state.Jobs {
			if j.GrantID == id && j.Status == "queued" {
				j.Status = "pending"
				j.GrantID = ""
				s.audit("approval_required", j.ID, "temporary grant revoked before dispatch")
			}
		}
		s.audit("revoke_grant", id, "operator revoked temporary grant; dispatched jobs are unaffected")
		return true
	}
	return false
}
func (s *Store) RevokeGrant(id string) error {
	return s.change(func() error {
		if !s.revokeGrant(id) {
			return fail("NOT_FOUND", "grant not found")
		}
		return nil
	})
}
