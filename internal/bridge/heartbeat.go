package bridge

import "time"

// AgentMember identifies an existing ephemeral attachment, not a terminal selection.
type AgentMember struct {
	ID          string `json:"id"`
	Generation  string `json:"generation"`
	Fingerprint string `json:"fingerprint"`
}

// Heartbeat validates the entire batch before renewing any lease. It deliberately
// has no dispatch path and makes no assertion about the selected terminal.
func (s *Store) Heartbeat(members []AgentMember) error {
	return s.change(func() error {
		if len(members) > 512 {
			return fail("INVALID_ARGUMENT", "too many heartbeat members")
		}
		seen := map[string]bool{}
		valid := make([]*Session, 0, len(members))
		var first error
		for _, m := range members {
			if seen[m.ID] || m.ID == "" || m.Generation == "" || m.Fingerprint == "" {
				if first == nil {
					first = fail("INVALID_ARGUMENT", "invalid or duplicate heartbeat member")
				}
				continue
			}
			seen[m.ID] = true
			x, e := s.check(m.ID, m.Generation, false)
			if e != nil {
				if first == nil {
					first = e
				}
				continue
			}
			if x.Fingerprint != m.Fingerprint {
				s.invalidate(x, "detached", "terminal fingerprint changed")
				if first == nil {
					first = fail("SESSION_CHANGED", "terminal fingerprint changed")
				}
				continue
			}
			valid = append(valid, x)
		}
		if first != nil {
			return first
		}
		now := time.Now().UTC()
		for _, x := range valid {
			x.LastSeen = now
		}
		return nil
	})
}

// AgentDetach cannot revoke another generation or a different fingerprint.
func (s *Store) AgentDetach(m AgentMember) error {
	return s.change(func() error {
		x := s.state.Sessions[m.ID]
		if x == nil {
			return fail("NOT_FOUND", "session not found")
		}
		if x.Generation != m.Generation || x.Fingerprint != m.Fingerprint {
			return fail("SESSION_CHANGED", "attachment identity mismatch")
		}
		s.invalidate(x, "detached", "agent removed attachment; remote process may still run")
		return nil
	})
}
