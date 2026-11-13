package bridge

import "time"

const MaxOutput = 1024 * 1024

type Session struct {
	ID          string    `json:"id"`
	Generation  string    `json:"generation"`
	Instance    string    `json:"instance"`
	Name        string    `json:"name"`
	Environment string    `json:"environment"`
	Fingerprint string    `json:"fingerprint"`
	State       string    `json:"state"`
	Archived    bool      `json:"archived,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitzero"`
	LastSeen    time.Time `json:"last_seen"`
}

type Submit struct {
	SessionID  string `json:"session_id"`
	Generation string `json:"generation"`
	RequestID  string `json:"request_id"`
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	TimeoutMS  int    `json:"timeout_ms"`
}

type Job struct {
	Submit
	RequiresExactApproval bool             `json:"requires_exact_approval,omitempty"`
	BuiltinRuleID         string           `json:"builtin_rule_id,omitempty"`
	ApprovalSource        string           `json:"approval_source,omitempty"`
	ApprovalHistory       []ApprovalRecord `json:"approval_history,omitempty"`
	GrantID               string           `json:"grant_id,omitempty"`
	RuleID                string           `json:"rule_id,omitempty"`
	ID                    string           `json:"id"`
	Status                string           `json:"status"`
	Output                string           `json:"output"`
	ExitCode              *int             `json:"exit_code"`
	Truncated             bool             `json:"truncated"`
	Interrupt             bool             `json:"interrupt_requested"`
	Reason                string           `json:"reason,omitempty"`
	Created               time.Time        `json:"created"`
	Dispatched            *time.Time       `json:"dispatched,omitempty"`
	LastSeq               int              `json:"last_seq"`
	EventHash             string           `json:"event_hash,omitempty"`
}

func copyJob(j *Job) Job {
	out := *j
	out.ApprovalHistory = append([]ApprovalRecord(nil), j.ApprovalHistory...)
	return out
}

type Event struct {
	SessionID   string `json:"session_id"`
	Generation  string `json:"generation"`
	Fingerprint string `json:"fingerprint"`
	JobID       string `json:"job_id"`
	Seq         int    `json:"seq"`
	Output      string `json:"output"`
	Status      string `json:"status"`
	ExitCode    *int   `json:"exit_code"`
	Reason      string `json:"reason,omitempty"`
}

type Audit struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	ID     string    `json:"id"`
	Detail string    `json:"detail"`
}

type State struct {
	BuiltinPolicyEnabled  *bool                   `json:"builtin_policy_enabled,omitempty"`
	BuiltinPolicyRevision uint64                  `json:"builtin_policy_revision"`
	NextTerminalNumber    uint64                  `json:"next_terminal_number,omitempty"`
	Sessions              map[string]*Session     `json:"sessions"`
	Jobs                  map[string]*Job         `json:"jobs"`
	Order                 []string                `json:"order"`
	Audit                 []Audit                 `json:"audit"`
	Grants                []Grant                 `json:"grants"`
	Rules                 []Rule                  `json:"rules"`
	Labels                map[string]SessionLabel `json:"labels,omitempty"`
}

// RuleSpec describes an operator-defined scope for one active attachment.
type RuleSpec struct {
	Name             string   `json:"name"`
	SessionID        string   `json:"session_id"`
	Generation       string   `json:"generation"`
	Program          string   `json:"program"`
	ArgumentPatterns []string `json:"argument_patterns"`
	Cwd              string   `json:"cwd"`
	MaxTimeoutMS     int      `json:"max_timeout_ms"`
	Minutes          int      `json:"minutes"`
}

type Rule struct {
	ID string `json:"id"`
	RuleSpec
	Expires time.Time `json:"expires"`
}

// Grant is an exact operator-approved scope for one attachment generation.
type Grant struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Generation string    `json:"generation"`
	Command    string    `json:"command"`
	Cwd        string    `json:"cwd"`
	TimeoutMS  int       `json:"timeout_ms"`
	Expires    time.Time `json:"expires"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func fail(code, message string) error { return &Error{code, message} }
