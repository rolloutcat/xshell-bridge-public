package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SessionLabel is display metadata, not an attachment identity or authorization.
type SessionLabel struct {
	Name        string `json:"name"`
	Environment string `json:"environment"`
}

func labelKey(fingerprint string) string {
	h := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(h[:])
}

func validLabelText(v string) bool {
	if !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// SetSessionLabel edits only the selected attachment and its persisted label.
func (s *Store) SetSessionLabel(id, generation, name, environment string) error {
	if !validLabelText(name) || !validLabelText(environment) {
		return fail("INVALID_ARGUMENT", "labels must be valid UTF-8 without control characters")
	}
	name, environment = strings.TrimSpace(name), strings.TrimSpace(environment)
	if name == "" || len(name) > 200 || len(environment) > 200 {
		return fail("INVALID_ARGUMENT", "name must be nonblank and labels must be at most 200 bytes")
	}
	return s.change(func() error {
		x, err := s.check(id, generation, false)
		if err != nil {
			return err
		}
		if s.state.Labels == nil {
			s.state.Labels = make(map[string]SessionLabel)
		}
		s.state.Labels[labelKey(x.Fingerprint)] = SessionLabel{Name: name, Environment: environment}
		x.Name, x.Environment = name, environment
		s.audit("session_label", id, "operator edited display labels")
		return nil
	})
}

func sessionLabelRequest(w http.ResponseWriter, r *http.Request, s *Store) error {
	// encoding/json replaces malformed UTF-8; reject it before decoding.
	b, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(b) {
		return fail("INVALID_ARGUMENT", "invalid JSON request")
	}
	r.Body = io.NopCloser(strings.NewReader(string(b)))
	var v struct {
		ID          string `json:"id"`
		Generation  string `json:"generation"`
		Name        string `json:"name"`
		Environment string `json:"environment"`
	}
	if err := decode(w, r, &v); err != nil {
		return err
	}
	return s.SetSessionLabel(v.ID, v.Generation, v.Name, v.Environment)
}
