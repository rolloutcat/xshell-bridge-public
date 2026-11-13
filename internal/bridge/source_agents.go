package bridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Hash only the instance component; the source remains an explicit namespace.
func sourceInstance(source, instance string) string {
	sum := sha256.Sum256([]byte(instance))
	return "source:" + source + ":" + hex.EncodeToString(sum[:])
}

func validSourceID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// SourceAgentToken derives a remote-source credential from the UTF-8 master key.
// It does not read configuration or activate any attachment.
func SourceAgentToken(master, source string) (string, error) {
	if master == "" || !validSourceID(source) {
		return "", fail("INVALID_ARGUMENT", "invalid source token inputs")
	}
	m := hmac.New(sha256.New, []byte(master))
	_, _ = m.Write([]byte("xshell-bridge-source-v1:" + source))
	return "xsh-agent-v1." + source + "." + hex.EncodeToString(m.Sum(nil)), nil
}

func authenticateSource(master, token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "xsh-agent-v1" || !validSourceID(parts[1]) {
		return "", false
	}
	expected, err := SourceAgentToken(master, parts[1])
	if err != nil || !hmac.Equal([]byte(expected), []byte(token)) {
		return "", false
	}
	return parts[1], true
}

// sourceFingerprint reads the exact, case-sensitive protocol field. Opaque
// legacy fingerprints have no source. Invalid/non-string claims never confer
// authority and are rejected rather than treated as local.
func sourceFingerprint(fp string) (source string, object bool, err error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(fp), &fields) != nil || fields == nil {
		return "", false, nil
	}
	raw, exists := fields["SourceID"]
	if !exists {
		return "", true, nil
	}
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &source) != nil {
		return "", true, fail("FORBIDDEN", "invalid fingerprint source")
	}
	return source, true, nil
}

func checkRegistrationSource(fp, source string) error {
	claimed, object, err := sourceFingerprint(fp)
	if err != nil {
		return err
	}
	if claimed != source || source != "" && !object {
		return fail("FORBIDDEN", "fingerprint source does not match agent")
	}
	return nil
}

// CheckAgentSource is deliberately read-only: in particular it does not call
// check, which can expire attachments and write audit entries. Check every ID
// before invoking any mutating Store method, including heartbeat batches.
// Session fingerprints are immutable, so the authorization remains valid after
// releasing the lock. Existing Store methods still enforce generation/replay.
func (s *Store) CheckAgentSource(source string, ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		x := s.state.Sessions[id]
		if x == nil {
			continue
		} // Preserve the underlying operation's NOT_FOUND.
		claimed, _, err := sourceFingerprint(x.Fingerprint)
		if err != nil || claimed != source {
			return fail("FORBIDDEN", "attachment belongs to another source")
		}
	}
	return nil
}
