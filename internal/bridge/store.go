package bridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

type Store struct {
	mu       sync.Mutex
	path     string
	state    State
	stopping bool
}
type PollResult struct {
	Job       *Job   `json:"job,omitempty"`
	Interrupt bool   `json:"interrupt"`
	State     string `json:"state"`
}

func ID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Open(path string) (*Store, error) {
	s := &Store{path: path, state: State{Sessions: map[string]*Session{}, Jobs: map[string]*Job{}}}
	b, e := os.ReadFile(path)
	if e == nil {
		if e = json.Unmarshal(b, &s.state); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if s.state.Sessions == nil || s.state.Jobs == nil {
		return nil, fmt.Errorf("invalid state file")
	}
	if s.state.BuiltinPolicyEnabled == nil {
		enabled := true
		s.state.BuiltinPolicyEnabled = &enabled
		s.audit("builtin_policy", BuiltinPackID, "enabled by default for absent setting; no retroactive approvals")
	}
	for _, x := range s.state.Sessions {
		s.invalidate(x, "detached", "bridge restarted; reattach required")
	}
	s.state.Grants = nil
	s.state.Rules = nil
	if e = s.persist(); e != nil {
		return nil, e
	}
	return s, nil
}
func (s *Store) persist() error {
	b, e := json.MarshalIndent(s.state, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(name, s.path)
}

// Persist before acknowledging any transition, especially before returning a command to an agent.
func (s *Store) change(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return fail("CONFLICT", "bridge is stopping; writes are disabled")
	}
	before, _ := json.Marshal(s.state)
	e := fn()
	if pe := s.persist(); pe != nil {
		// Decode into a fresh value: unmarshalling into existing maps merges
		// entries and omitted fields would retain mutations (including Labels).
		var restored State
		_ = json.Unmarshal(before, &restored)
		s.state = restored
		return fail("STORAGE_ERROR", pe.Error())
	}
	return e
}
func (s *Store) audit(action, id, detail string) {
	s.state.Audit = append(s.state.Audit, Audit{time.Now().UTC(), action, id, detail})
	if len(s.state.Audit) > 5000 {
		s.state.Audit = s.state.Audit[len(s.state.Audit)-5000:]
	}
}
func (s *Store) invalidate(x *Session, state, reason string) {
	x.State = state
	for i := len(s.state.Rules) - 1; i >= 0; i-- {
		if s.state.Rules[i].SessionID == x.ID {
			s.revokeRule(s.state.Rules[i].ID, reason)
		}
	}
	for i := len(s.state.Grants) - 1; i >= 0; i-- {
		if s.state.Grants[i].SessionID == x.ID {
			s.state.Grants = append(s.state.Grants[:i], s.state.Grants[i+1:]...)
		}
	}
	for _, j := range s.state.Jobs {
		if j.SessionID != x.ID {
			continue
		}
		switch j.Status {
		case "dispatched", "running":
			j.Status = "unknown"
			j.Reason = reason
		case "pending", "queued":
			j.Status = "cancelled"
			j.Reason = reason
		}
	}
	s.audit("invalidate", x.ID, reason)
}
func (s *Store) check(id, generation string, active bool) (*Session, error) {
	x := s.state.Sessions[id]
	if x == nil {
		return nil, fail("NOT_FOUND", "session not found")
	}
	if generation != x.Generation {
		return nil, fail("SESSION_CHANGED", "generation mismatch")
	}
	if x.Archived {
		return nil, fail("SESSION_CHANGED", "terminal record was removed; manual new attachment required")
	}
	if x.State != "pending" && x.State != "active" {
		return nil, fail("SESSION_CHANGED", "attachment is no longer usable")
	}
	if time.Since(x.LastSeen) > 15*time.Second {
		s.invalidate(x, "expired", "agent heartbeat expired")
		return nil, fail("SESSION_CHANGED", "agent heartbeat expired")
	}
	if active && x.State != "active" {
		return nil, fail("NEEDS_AUTHORIZATION", "approve session in VM console first")
	}
	return x, nil
}
func (s *Store) Register(instance, name, environment, fingerprint string) (out Session, err error) {
	return s.RegisterNamed(instance, name, environment, fingerprint, false)
}
func (s *Store) RegisterNamed(instance, name, environment, fingerprint string, autoName bool) (out Session, err error) {
	err = s.change(func() error {
		if len(instance) < 16 || len(instance) > 128 || len(name) == 0 || len(name) > 200 || len(environment) > 200 || len(fingerprint) < 8 || len(fingerprint) > 2048 {
			return fail("INVALID_ARGUMENT", "invalid registration")
		}
		// Validate incoming lengths above before applying persisted display metadata.
		// Apply before idempotence and deduplication; agents may retry old names.
		if label, ok := s.state.Labels[labelKey(fingerprint)]; ok {
			name, environment = label.Name, label.Environment
		}
		for _, x := range s.state.Sessions {
			if x.Instance == instance {
				if x.Fingerprint != fingerprint || x.Name != name || x.Environment != environment {
					return fail("CONFLICT", "registration changed")
				}
				if _, e := s.check(x.ID, x.Generation, false); e != nil {
					return e
				}
				out = *x
				return nil
			}
		}
		for _, x := range s.state.Sessions {
			if x.Fingerprint == fingerprint && (x.State == "pending" || x.State == "active") {
				if time.Since(x.LastSeen) <= 15*time.Second {
					return fail("CONFLICT", "terminal already attached; detach the old attachment first")
				}
				s.invalidate(x, "expired", "agent heartbeat expired")
			}
		}
		if _, exists := s.state.Labels[labelKey(fingerprint)]; autoName && !exists {
			if s.state.NextTerminalNumber == ^uint64(0) {
				return fail("INVALID_ARGUMENT", "terminal number exhausted")
			}
			s.state.NextTerminalNumber++
			name = fmt.Sprintf("终端 %02d", s.state.NextTerminalNumber)
			if s.state.Labels == nil {
				s.state.Labels = map[string]SessionLabel{}
			}
			s.state.Labels[labelKey(fingerprint)] = SessionLabel{Name: name, Environment: environment}
		}
		now := time.Now().UTC()
		x := &Session{ID: ID(), Generation: ID(), Instance: instance, Name: name, Environment: environment, Fingerprint: fingerprint, State: "pending", CreatedAt: now, LastSeen: now}
		s.state.Sessions[x.ID] = x
		out = *x
		s.audit("register", x.ID, "awaiting operator activation")
		return nil
	})
	return
}
func (s *Store) Activate(id, generation string) error {
	return s.change(func() error {
		x, e := s.check(id, generation, false)
		if e != nil {
			return e
		}
		x.State = "active"
		s.audit("activate", id, "operator activated attachment")
		return nil
	})
}
func (s *Store) Submit(r Submit) (out Job, err error) {
	if r.TimeoutMS == 0 {
		r.TimeoutMS = 60000
	}
	err = s.change(func() error {
		if len(r.RequestID) < 1 || len(r.RequestID) > 128 || !validCommand(r.Command, r.Cwd, r.TimeoutMS) {
			return fail("INVALID_ARGUMENT", "invalid command, request ID, cwd or timeout (1000..3600000 ms)")
		}
		for _, j := range s.state.Jobs {
			if j.SessionID == r.SessionID && j.RequestID == r.RequestID {
				if j.Submit != r {
					return fail("CONFLICT", "request ID already used with different payload")
				}
				out = copyJob(j)
				return nil
			}
		}
		if _, e := s.check(r.SessionID, r.Generation, true); e != nil {
			return e
		}
		policy := checkCommandPolicy(r.Command, r.Cwd)
		if d := policy; d != nil && d.Decision == "deny" {
			return d.err()
		}
		exactOnly := policy != nil || needsExactApproval(r.Command)
		j := &Job{Submit: r, ID: ID(), Status: "pending", RequiresExactApproval: exactOnly, Created: time.Now().UTC()}
		if id, _, _ := s.builtinMatch(r); id != "" {
			j.Status = "queued"
			j.RequiresExactApproval = false
			j.BuiltinRuleID = id
			s.recordApproval(j, "builtin", id)
		}
		for _, g := range s.state.Grants {
			if j.Status == "queued" || exactOnly {
				break
			}
			if g.matches(r) && time.Now().Before(g.Expires) {
				j.Status = "queued"
				j.GrantID = g.ID
				break
			}
		}
		if j.Status == "pending" && !exactOnly {
			for _, rule := range s.state.Rules {
				if s.ruleActive(rule) && rule.matches(r) {
					j.Status = "queued"
					j.RuleID = rule.ID
					break
				}
			}
		}
		s.state.Jobs[j.ID] = j
		s.state.Order = append(s.state.Order, j.ID)
		if j.ApprovalSource == "builtin" {
			s.audit("auto_approve", j.ID, "default policy "+j.BuiltinRuleID)
		} else if j.GrantID != "" {
			s.recordApproval(j, "grant", j.GrantID)
			s.audit("auto_approve", j.ID, "exact temporary grant "+j.GrantID)
		} else if j.RuleID != "" {
			s.recordApproval(j, "rule", j.RuleID)
			s.audit("auto_approve", j.ID, "operator approval rule "+j.RuleID+": full-token scope matched")
		} else {
			s.audit("submit", j.ID, "awaiting exact-command approval")
		}
		out = copyJob(j)
		return nil
	})
	return
}
func (s *Store) Decide(id string, allow bool) error {
	return s.DecideRemember(id, allow, 0)
}
func (s *Store) DecideRemember(id string, allow bool, minutes int) error {
	if (minutes != 0 && minutes != 30) || (!allow && minutes != 0) {
		return fail("INVALID_ARGUMENT", "remember_minutes must be 0 or 30, and requires allow")
	}
	return s.change(func() error {
		j := s.state.Jobs[id]
		if j == nil {
			return fail("NOT_FOUND", "job not found")
		}
		if j.Status != "pending" {
			return fail("CONFLICT", "job is not awaiting approval")
		}
		if _, e := s.check(j.SessionID, j.Generation, true); e != nil {
			return e
		}
		if allow {
			policy := checkCommandPolicy(j.Command, j.Cwd)
			if d := policy; d != nil && d.Decision == "deny" {
				return d.err()
			}
			if minutes != 0 && (policy != nil || needsExactApproval(j.Command)) {
				return fail("EXACT_APPROVAL_REQUIRED", "scripts and uninspectable commands require approval for each submission; cannot remember")
			}
		}
		s.decideLocked(j, allow, minutes)
		return nil
	})
}

// Caller holds the store lock and has validated the pending job and target.
func (s *Store) decideLocked(j *Job, allow bool, minutes int) {
	id := j.ID
	s.recordApproval(j, "operator", id)
	if allow {
		j.Status = "queued"
		s.audit("approve", id, "operator approved immutable command")
		if minutes == 30 {
			kept := s.state.Grants[:0]
			for _, g := range s.state.Grants {
				if !g.matches(j.Submit) && time.Now().Before(g.Expires) {
					kept = append(kept, g)
				}
			}
			s.state.Grants = kept
			g := Grant{ID: ID(), SessionID: j.SessionID, Generation: j.Generation, Command: j.Command, Cwd: j.Cwd, TimeoutMS: j.TimeoutMS, Expires: time.Now().UTC().Add(30 * time.Minute)}
			s.state.Grants = append(s.state.Grants, g)
			s.audit("grant", g.ID, "exact command on attachment "+j.SessionID+" for 30 minutes")
		}
	} else {
		j.Status = "denied"
		j.Reason = "USER_DENIED"
		s.audit("deny", id, "operator denied command")
		for i := len(s.state.Rules) - 1; i >= 0; i-- {
			if s.state.Rules[i].matches(j.Submit) {
				s.revokeRule(s.state.Rules[i].ID, "operator denied matching task "+id)
			}
		}
		for i := len(s.state.Grants) - 1; i >= 0; i-- {
			if s.state.Grants[i].matches(j.Submit) {
				s.revokeGrant(s.state.Grants[i].ID)
			}
		}
	}
}
func (s *Store) Poll(id, generation, fingerprint string) (out PollResult, err error) {
	err = s.change(func() error {
		x, e := s.check(id, generation, false)
		if e != nil {
			return e
		}
		if x.Fingerprint != fingerprint {
			s.invalidate(x, "detached", "terminal fingerprint changed")
			return fail("SESSION_CHANGED", "terminal fingerprint changed")
		}
		x.LastSeen = time.Now().UTC()
		out.State = x.State
		if x.State != "active" {
			return nil
		}
		for _, j := range s.state.Jobs {
			if j.SessionID == id && (j.Status == "dispatched" || j.Status == "running") {
				out.Interrupt = j.Interrupt
				return nil
			}
		}
		for _, jid := range s.state.Order {
			j := s.state.Jobs[jid]
			if j.SessionID == id && j.Generation == generation && j.Status == "queued" {
				policy := checkCommandPolicy(j.Command, j.Cwd)
				if d := policy; d != nil && d.Decision == "deny" {
					j.Status = "denied"
					j.Reason = "COMMAND_DENIED: " + d.ID + ": " + d.Message
					s.audit("policy_deny", j.ID, j.Reason)
					continue
				}
				if (j.GrantID != "" || j.RuleID != "") && (policy != nil || needsExactApproval(j.Command)) {
					j.Status = "pending"
					j.RequiresExactApproval = true
					j.GrantID, j.RuleID = "", ""
					s.audit("approval_required", j.ID, "script or uninspectable command requires fresh exact human approval")
					continue
				}
				if j.ApprovalSource == "builtin" {
					matched, _, _ := s.builtinMatch(j.Submit)
					if matched == "" || matched != j.BuiltinRuleID {
						j.Status = "pending"
						j.RequiresExactApproval = needsExactApproval(j.Command)
						s.audit("approval_required", j.ID, "default policy no longer matches; original provenance retained")
						continue
					}
				}
				if j.GrantID != "" && !s.validGrant(j) {
					j.Status = "pending"
					j.GrantID = ""
					s.audit("approval_required", j.ID, "temporary grant expired or revoked before dispatch")
					continue
				}
				if j.RuleID != "" && !s.validRule(j) {
					ruleID := j.RuleID
					j.Status = "pending"
					j.RuleID = ""
					s.audit("approval_required", j.ID, "rule "+ruleID+": expired, revoked or scope invalid before dispatch")
					continue
				}
				j.Status = "dispatched"
				now := time.Now().UTC()
				j.Dispatched = &now
				cp := copyJob(j)
				out.Job = &cp
				s.audit("dispatch", j.ID, "command handed to agent; never automatically replay")
				break
			}
		}
		return nil
	})
	return
}
func (s *Store) Event(v Event) error {
	return s.change(func() error {
		x, e := s.check(v.SessionID, v.Generation, true)
		if e != nil {
			return e
		}
		if x.Fingerprint != v.Fingerprint {
			s.invalidate(x, "detached", "terminal fingerprint changed")
			return fail("SESSION_CHANGED", "terminal fingerprint changed")
		}
		j := s.state.Jobs[v.JobID]
		if j == nil || j.SessionID != x.ID || j.Generation != v.Generation {
			return fail("NOT_FOUND", "job not found for attachment")
		}
		b, _ := json.Marshal(v)
		h := fmt.Sprintf("%x", sha256.Sum256(b))
		if v.Seq == j.LastSeq && h == j.EventHash {
			x.LastSeen = time.Now().UTC()
			return nil
		}
		if v.Seq != j.LastSeq+1 {
			return fail("CONFLICT", "event sequence mismatch")
		}
		if j.Status != "dispatched" && j.Status != "running" {
			return fail("CONFLICT", "job is terminal")
		}
		if len(v.Output) > 32768 || !utf8.ValidString(v.Output) || len(v.Reason) > 512 {
			return fail("INVALID_ARGUMENT", "invalid event size")
		}
		if v.Status != "running" && v.Status != "completed" && v.Status != "unknown" {
			return fail("INVALID_ARGUMENT", "invalid event status")
		}
		if (v.Status == "completed") != (v.ExitCode != nil) {
			return fail("INVALID_ARGUMENT", "exit code required only for completed event")
		}
		if v.ExitCode != nil && (*v.ExitCode < 0 || *v.ExitCode > 255) {
			return fail("INVALID_ARGUMENT", "invalid shell exit code")
		}
		remaining := MaxOutput - len(j.Output)
		chunk := v.Output
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
			for !utf8.ValidString(chunk) {
				chunk = chunk[:len(chunk)-1]
			}
			j.Truncated = true
		}
		j.Output += chunk
		j.Status = v.Status
		j.ExitCode = v.ExitCode
		j.LastSeq = v.Seq
		j.EventHash = h
		j.Reason = v.Reason
		x.LastSeen = time.Now().UTC()
		if v.Status == "unknown" {
			s.invalidate(x, "blocked", "command outcome unknown: "+v.Reason)
		}
		if v.Status == "completed" {
			s.audit("complete", j.ID, fmt.Sprintf("exit=%d", *v.ExitCode))
		}
		return nil
	})
}
func (s *Store) Detach(id, generation string) error {
	return s.change(func() error {
		x := s.state.Sessions[id]
		if x == nil {
			return fail("NOT_FOUND", "session not found")
		}
		if x.Generation != generation {
			return fail("SESSION_CHANGED", "generation mismatch")
		}
		s.invalidate(x, "detached", "attachment revoked; remote process may still run")
		return nil
	})
}
func (s *Store) Interrupt(id string) error {
	return s.change(func() error {
		j := s.state.Jobs[id]
		if j == nil {
			return fail("NOT_FOUND", "job not found")
		}
		switch j.Status {
		case "pending", "queued":
			j.Status = "cancelled"
		case "dispatched", "running":
			j.Interrupt = true
		default:
			return fail("CONFLICT", "job is terminal")
		}
		s.audit("interrupt", id, "requested; does not prove remote termination")
		return nil
	})
}
func (s *Store) Expire(now time.Time) error {
	return s.change(func() error {
		for _, x := range s.state.Sessions {
			if (x.State == "pending" || x.State == "active") && now.Sub(x.LastSeen) > 15*time.Second {
				s.invalidate(x, "expired", "agent heartbeat expired")
			}
		}
		for _, j := range s.state.Jobs {
			if (j.Status == "dispatched" || j.Status == "running") && j.Dispatched != nil && now.Sub(*j.Dispatched) > time.Duration(j.TimeoutMS)*time.Millisecond+5*time.Second {
				s.invalidate(s.state.Sessions[j.SessionID], "blocked", "deadline exceeded; remote process may still run")
			}
		}
		return nil
	})
}
func (s *Store) Sessions() []Session {
	return s.sessions(false)
}
func (s *Store) sessions(includeArchived bool) []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Session{}
	for _, x := range s.state.Sessions {
		if includeArchived || !x.Archived {
			out = append(out, *x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) GetJob(id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.state.Jobs[id]
	if j == nil {
		return Job{}, fail("NOT_FOUND", "job not found")
	}
	return copyJob(j), nil
}
func (s *Store) Jobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Job{}
	for _, id := range s.state.Order {
		out = append(out, copyJob(s.state.Jobs[id]))
	}
	return out
}
func (s *Store) Audits() []Audit {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Audit{}, s.state.Audit...)
	return out
}
