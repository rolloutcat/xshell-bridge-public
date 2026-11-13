package bridge

// RemovalItem is the immutable identity and complete job-state snapshot reviewed
// by the operator. Heartbeats are deliberately not part of this display identity.
type RemovalItem struct {
	ID          string       `json:"id"`
	Generation  string       `json:"generation"`
	State       string       `json:"state"`
	Fingerprint string       `json:"fingerprint"`
	Name        string       `json:"name"`
	Environment string       `json:"environment"`
	Jobs        []RemovalJob `json:"jobs"`
}

type RemovalJob struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func inactiveSession(state string) bool {
	return state == "detached" || state == "blocked" || state == "expired"
}

// RemoveSessions archives workbench records only. It never closes SSH/Xshell,
// deletes history, or changes a job's state to make it eligible for removal.
func (s *Store) RemoveSessions(stopActive bool, items []RemovalItem) error {
	return s.change(func() error {
		if len(items) < 1 || len(items) > 100 || stopActive && len(items) != 1 {
			return fail("INVALID_ARGUMENT", "removal requires 1..100 records; stop_active requires exactly one")
		}
		seen := map[string]bool{}
		for _, item := range items {
			if item.ID == "" || item.Generation == "" || seen[item.ID] || item.Jobs == nil {
				return fail("INVALID_ARGUMENT", "missing or duplicate removal identity/job snapshot")
			}
			seen[item.ID] = true
		}
		// Validate the ENTIRE request under the same lock used by submit, poll,
		// heartbeat and detach, before changing any session, permission or audit.
		for _, item := range items {
			x := s.state.Sessions[item.ID]
			if x == nil || x.Archived || x.Generation != item.Generation || x.State != item.State || x.Fingerprint != item.Fingerprint || x.Name != item.Name || x.Environment != item.Environment {
				return fail("SESSION_CHANGED", "removal identity changed; no records removed")
			}
			if !inactiveSession(x.State) && !(stopActive && (x.State == "pending" || x.State == "active")) {
				return fail("CONFLICT", "batch cleanup only accepts already inactive records")
			}
			expected := map[string]string{}
			for _, j := range item.Jobs {
				if j.ID == "" || j.Status == "" || expected[j.ID] != "" {
					return fail("INVALID_ARGUMENT", "invalid or duplicate job snapshot")
				}
				expected[j.ID] = j.Status
			}
			count := 0
			for _, j := range s.state.Jobs {
				if j.SessionID != x.ID {
					continue
				}
				switch j.Status {
				case "pending", "queued", "dispatched", "running":
					return fail("CONFLICT", "terminal has unfinished jobs; no records removed")
				}
				if expected[j.ID] != j.Status {
					return fail("CONFLICT", "job snapshot changed; no records removed")
				}
				count++
			}
			if count != len(expected) {
				return fail("CONFLICT", "job snapshot changed; no records removed")
			}
		}
		for _, item := range items {
			x := s.state.Sessions[item.ID]
			if x.State == "active" || x.State == "pending" {
				s.invalidate(x, "detached", "operator stopped attachment and removed terminal record; SSH remains open")
			}
			x.Archived = true
			s.audit("remove_session", x.ID, "operator archived terminal record; job history retained")
		}
		return nil
	})
}
