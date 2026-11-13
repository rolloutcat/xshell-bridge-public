package bridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceAgentPermissions(t *testing.T) {
	s, _ := setup(t)
	const a = "0123456789abcdef0123456789abcdef"
	const b = "fedcba9876543210fedcba9876543210"
	const master = "fixture-master"
	// Fixed interoperability inputs; reference uses the protocol bytes directly.
	mac := hmac.New(sha256.New, []byte(master))
	mac.Write([]byte("xshell-bridge-source-v1:" + a))
	want := "xsh-agent-v1." + a + "." + hex.EncodeToString(mac.Sum(nil))
	token, err := SourceAgentToken(master, a)
	if err != nil || token != want {
		t.Fatalf("fixture: %q %v", token, err)
	}
	other, _ := SourceAgentToken(master, b)
	h := Handler(s, Keys{Agent: master, Client: "client", Admin: "admin"})
	call := func(path, key string, body any, status int) []byte {
		t.Helper()
		data, _ := json.Marshal(body)
		method := "POST"
		if body == nil {
			method = "GET"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	register := func(key, source string) Session {
		t.Helper()
		fp := `{"Terminal":"same","SourceID":"` + source + `"}`
		if source == "" {
			fp = `{"Terminal":"same"}`
		}
		data := call("/v1/agent/register", key, map[string]any{"instance": "same-instance-12345", "name": "same", "fingerprint": fp, "auto_name": true}, 200)
		var x Session
		if err := json.Unmarshal(data, &x); err != nil {
			t.Fatal(err)
		}
		if x.State != "pending" {
			t.Fatal(x)
		}
		return x
	}
	x := register(token, a)
	y := register(other, b)
	z := register(master, "")
	if x.ID == y.ID || x.ID == z.ID || y.ID == z.ID || x.Name == y.Name || x.Name == z.Name {
		t.Fatal("namespace collision")
	}
	call("/v1/agent/register", token, map[string]string{"instance": "fake-instance-12345", "name": "fake", "fingerprint": `{"SourceID":"` + b + `"}`}, 403)
	call("/v1/agent/register", master, map[string]string{"instance": "fake-instance-12345", "name": "fake", "fingerprint": x.Fingerprint}, 403)
	call("/v1/agent/register", token, map[string]string{"instance": "fake-instance-12345", "name": "fake", "fingerprint": "opaque-fingerprint"}, 403)
	for _, key := range []string{other, master} {
		before, _ := json.Marshal(s.state)
		member := AgentMember{ID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint}
		call("/v1/agent/poll", key, member, 403)
		call("/v1/agent/event", key, Event{SessionID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint}, 403)
		call("/v1/agent/heartbeat", key, map[string]any{"members": []AgentMember{{ID: y.ID, Generation: y.Generation, Fingerprint: "changed"}, member}}, 403)
		call("/v1/agent/detach", key, member, 403)
		after, _ := json.Marshal(s.state)
		if string(before) != string(after) {
			t.Fatal("wrong scope mutated state or audit")
		}
	}
	for _, path := range []string{"/v1/sessions", "/v1/jobs", "/v1/health", "/v1/admin/decide", "/v1/admin/shutdown"} {
		call(path, token, nil, 403)
	}
	for _, bad := range []string{token + "0", "xsh-agent-v1." + strings.ToUpper(a) + "." + strings.Repeat("0", 64), "xsh-agent-v1." + a + "." + strings.Repeat("0", 64)} {
		call("/v1/agent/poll", bad, map[string]string{}, 401)
	}
	call("/v1/agent/poll", token, AgentMember{ID: x.ID, Generation: x.Generation, Fingerprint: x.Fingerprint}, 200)
	_ = http.StatusOK
}
