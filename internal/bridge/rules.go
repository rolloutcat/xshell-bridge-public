package bridge

import (
	"regexp"
	"strings"
	"time"
)

var ruleProgram = regexp.MustCompile(`\A[A-Za-z0-9_][A-Za-z0-9_.-]*\z`)

func rulePattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(`\A(?:` + pattern + `)\z`)
}

// Only literal ASCII tokens are eligible. This is not a shell parser and never
// authorizes a prefix of a command. Everything else requires exact approval.
func ruleTokens(command string) ([]string, bool) {
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == ' ' || c == '\t' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_./:@%+,=-", rune(c)) {
			continue
		}
		return nil, false
	}
	tokens := strings.FieldsFunc(command, func(c rune) bool { return c == ' ' || c == '\t' })
	return tokens, len(tokens) != 0
}

func (r Rule) matches(v Submit) bool {
	code, _ := r.matchReason(v)
	return code == "matched"
}

// Scope only: expiry and attachment liveness remain the caller's responsibility.
// Argument indexes are zero-based, excluding the program token.
func (r Rule) matchReason(v Submit) (string, *int) {
	if r.SessionID != v.SessionID {
		return "session", nil
	}
	if r.Generation != v.Generation {
		return "generation", nil
	}
	if r.Cwd != v.Cwd {
		return "cwd", nil
	}
	if v.TimeoutMS < 1000 || v.TimeoutMS > r.MaxTimeoutMS {
		return "timeout", nil
	}
	tokens, ok := ruleTokens(v.Command)
	if !ok {
		return "token_syntax", nil
	}
	if tokens[0] != r.Program {
		return "program", nil
	}
	if len(tokens)-1 != len(r.ArgumentPatterns) {
		return "argument_count", nil
	}
	for i, pattern := range r.ArgumentPatterns {
		re, e := rulePattern(pattern)
		if e != nil || !re.MatchString(tokens[i+1]) {
			index := i
			return "argument_pattern", &index
		}
	}
	return "matched", nil
}

func copyRule(r Rule) Rule {
	r.ArgumentPatterns = append([]string{}, r.ArgumentPatterns...)
	return r
}

func (s *Store) ruleActive(r Rule) bool {
	x := s.state.Sessions[r.SessionID]
	return x != nil && x.State == "active" && x.Generation == r.Generation && time.Since(x.LastSeen) <= 15*time.Second && time.Now().Before(r.Expires)
}

func (s *Store) CreateRule(spec RuleSpec) (out Rule, err error) {
	err = s.change(func() error {
		if strings.TrimSpace(spec.Name) == "" || len(spec.Name) > 200 || !ruleProgram.MatchString(spec.Program) || len(spec.Cwd) > 4096 || strings.ContainsAny(spec.Cwd, "\x00\r\n") || spec.MaxTimeoutMS < 1000 || spec.MaxTimeoutMS > 3600000 || spec.Minutes < 1 || spec.Minutes > 480 || len(spec.ArgumentPatterns) > 16 {
			return fail("INVALID_ARGUMENT", "invalid approval rule scope or limits")
		}
		for _, pattern := range spec.ArgumentPatterns {
			if len(pattern) > 256 {
				return fail("INVALID_ARGUMENT", "argument pattern exceeds 256 bytes")
			}
			if _, e := rulePattern(pattern); e != nil {
				return fail("INVALID_ARGUMENT", "invalid RE2 argument pattern")
			}
		}
		if _, e := s.check(spec.SessionID, spec.Generation, true); e != nil {
			return e
		}
		// Expired scopes confer no authority and need not consume the rule limit.
		kept := make([]Rule, 0, len(s.state.Rules))
		for _, r := range s.state.Rules {
			if s.ruleActive(r) {
				kept = append(kept, r)
			}
		}
		if len(kept) >= 100 {
			return fail("INVALID_ARGUMENT", "maximum 100 approval rules")
		}
		r := copyRule(Rule{ID: ID(), RuleSpec: spec, Expires: time.Now().UTC().Add(time.Duration(spec.Minutes) * time.Minute)})
		s.state.Rules = append(kept, r)
		s.audit("create_rule", r.ID, "operator rule for attachment "+r.SessionID+" generation "+r.Generation+" program "+r.Program)
		out = copyRule(r)
		return nil
	})
	if err != nil {
		out = Rule{}
	}
	return
}

func (s *Store) Rules() []Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Rule{}
	for _, r := range s.state.Rules {
		if s.ruleActive(r) {
			out = append(out, copyRule(r))
		}
	}
	return out
}

func (s *Store) validRule(j *Job) bool {
	for _, r := range s.state.Rules {
		if r.ID == j.RuleID && s.ruleActive(r) && r.matches(j.Submit) {
			return true
		}
	}
	return false
}

func (s *Store) revokeRule(id, reason string) bool {
	for i, r := range s.state.Rules {
		if r.ID != id {
			continue
		}
		s.state.Rules = append(s.state.Rules[:i], s.state.Rules[i+1:]...)
		for _, j := range s.state.Jobs {
			if j.RuleID == id && j.Status == "queued" {
				j.Status = "pending"
				j.RuleID = ""
				s.audit("approval_required", j.ID, "rule "+id+": "+reason)
			}
		}
		s.audit("revoke_rule", id, reason+"; dispatched jobs unaffected")
		return true
	}
	return false
}

func (s *Store) RevokeRule(id string) error {
	return s.change(func() error {
		if !s.revokeRule(id, "operator revoked approval rule") {
			return fail("NOT_FOUND", "rule not found")
		}
		return nil
	})
}
