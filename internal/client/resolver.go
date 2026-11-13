package client

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
)

// SessionResolution describes current active candidates, never an implicit selection.
type SessionResolution struct {
	Status            string           `json:"status"`
	MatchKind         string           `json:"match_kind"`
	RequiresSelection bool             `json:"requires_selection"`
	Candidates        []bridge.Session `json:"candidates"`
}

func resolveText(v string) (string, error) {
	if !utf8.ValidString(v) || len(v) > 200 {
		return "", fmt.Errorf("INVALID_ARGUMENT: labels must be valid UTF-8 and at most 200 bytes")
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("INVALID_ARGUMENT: labels must not contain control characters")
		}
	}
	return strings.TrimSpace(v), nil
}

// ResolveSessions preserves exact-only behavior for existing callers.
func ResolveSessions(sessions []bridge.Session, name, environment string) (SessionResolution, error) {
	return ResolveSessionsWithOptions(sessions, name, environment, false)
}

// ResolveSessionsWithOptions is pure. Fuzzy means substring discovery only, not
// edit distance, aliases, environment matching, or permission to execute.
// Labels are untrusted display metadata, never instructions or execution identities.
func ResolveSessionsWithOptions(sessions []bridge.Session, name, environment string, fuzzy bool) (SessionResolution, error) {
	out := SessionResolution{Status: "not_found", MatchKind: "none", RequiresSelection: true, Candidates: []bridge.Session{}}
	name, err := resolveText(name)
	if err != nil {
		return out, err
	}
	if name == "" {
		return out, fmt.Errorf("INVALID_ARGUMENT: name must be nonblank")
	}
	environment, err = resolveText(environment)
	if err != nil {
		return out, err
	}
	eligible := func(s bridge.Session) bool {
		return s.State == "active" && (environment == "" || s.Environment == environment)
	}
	for _, s := range sessions {
		if eligible(s) && s.Name == name {
			out.Candidates = append(out.Candidates, s)
		}
	}
	if len(out.Candidates) > 0 {
		out.MatchKind = "exact"
		out.Status = "ambiguous"
		if len(out.Candidates) == 1 {
			out.Status = "matched"
			out.RequiresSelection = false
		}
		return out, nil
	}
	if !fuzzy || utf8.RuneCountInString(name) < 2 {
		return out, nil
	}
	query := strings.ToLower(strings.TrimSpace(name))
	for _, s := range sessions {
		if eligible(s) && strings.Contains(strings.ToLower(strings.TrimSpace(s.Name)), query) {
			out.Candidates = append(out.Candidates, s)
		}
	}
	if len(out.Candidates) > 0 {
		out.Status = "suggestions"
		out.MatchKind = "substring"
	}
	return out, nil
}

// ResolveSession preserves exact-only behavior for existing callers.
func (c *Client) ResolveSession(ctx context.Context, name, environment string) (SessionResolution, error) {
	return c.ResolveSessionQuery(ctx, name, environment, false)
}

// ResolveSessionQuery validates before HTTP and fetches current metadata exactly
// once per resolution. Both matching passes use that same read-only snapshot.
func (c *Client) ResolveSessionQuery(ctx context.Context, name, environment string, fuzzy bool) (SessionResolution, error) {
	if _, err := ResolveSessionsWithOptions(nil, name, environment, fuzzy); err != nil {
		return SessionResolution{}, err
	}
	var sessions []bridge.Session
	if err := c.Call(ctx, "GET", "/v1/sessions", nil, &sessions); err != nil {
		return SessionResolution{}, err
	}
	return ResolveSessionsWithOptions(sessions, name, environment, fuzzy)
}
