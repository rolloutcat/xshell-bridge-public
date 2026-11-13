package bridge

import (
	"encoding/json"
	"net/http"
)

// BeginShutdown atomically persists acceptance and erects a write barrier.
// It neither expires attachments nor cancels, interrupts, or replays jobs.
func (s *Store) BeginShutdown() error {
	return s.beginShutdown(false)
}

func (s *Store) beginShutdown(desktop bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return fail("CONFLICT", "bridge is stopping; do not retry shutdown")
	}
	if !desktop {
		for _, x := range s.state.Sessions {
			if x.State == "active" || x.State == "pending" {
				return fail("CONFLICT", "attached sessions prevent shutdown")
			}
		}
		for _, j := range s.state.Jobs {
			switch j.Status {
			case "pending", "queued", "dispatched", "running":
				return fail("CONFLICT", "unfinished jobs prevent shutdown")
			}
		}
	}
	before, err := json.Marshal(s.state)
	if err != nil {
		return fail("STORAGE_ERROR", "cannot snapshot shutdown state")
	}
	if desktop {
		for _, x := range s.state.Sessions {
			if x.State == "active" || x.State == "pending" {
				s.invalidate(x, "detached", "desktop closed; remote process may still run")
			}
		}
		// Settle every unfinished job, including those on already detached sessions.
		for _, j := range s.state.Jobs {
			switch j.Status {
			case "pending", "queued":
				j.Status = "cancelled"
				j.Reason = "desktop closed before dispatch"
			case "dispatched", "running":
				j.Status = "unknown"
				j.Reason = "desktop closed; remote process may still run"
			}
		}
		s.audit("desktop_shutdown", "", "operator closed desktop; no jobs replayed")
	} else {
		s.audit("safe_shutdown", "", "operator requested idle shutdown; no jobs replayed")
	}
	s.stopping = true
	if err := s.persist(); err != nil {
		var restored State
		_ = json.Unmarshal(before, &restored)
		s.state = restored
		s.stopping = false
		return fail("STORAGE_ERROR", err.Error())
	}
	return nil
}

func shutdownRequest(w http.ResponseWriter, r *http.Request, s *Store, callback func()) error {
	if callback == nil {
		return fail("NOT_FOUND", "endpoint not found")
	}
	// A pointer rejects null as well as arrays/scalars. Unknown fields and trailing
	// JSON are rejected by decode. Only the literal empty object is accepted.
	var body *struct{}
	if err := decode(w, r, &body); err != nil {
		return err
	}
	if body == nil {
		return fail("INVALID_ARGUMENT", "empty JSON object required")
	}
	return s.beginShutdown(r.URL.Path == "/v1/admin/desktop-shutdown")
}
