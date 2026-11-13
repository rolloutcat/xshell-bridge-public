package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invokeDiscovery(t *testing.T, args ...string) (int, error, string) {
	t.Helper()
	oldArgs, oldOut := os.Args, os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { os.Args, os.Stdout = oldArgs, oldOut; f.Close() }()
	os.Args = append([]string{"xshctl"}, args...)
	os.Stdout = f
	code, err := run()
	_, _ = f.Seek(0, 0)
	b, _ := io.ReadAll(f)
	return code, err, string(b)
}

func TestDiscoveryAndFilters(t *testing.T) {
	oldRegister := registerProcess
	registrations := 0
	registerProcess = func(string) func() { registrations++; return func() {} }
	defer func() { registerProcess = oldRegister }()
	var calls []string
	var submitted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.String())
		switch {
		case r.Method == "POST":
			_ = json.NewDecoder(r.Body).Decode(&submitted)
			io.WriteString(w, `{"status":"queued"}`)
		case r.URL.Path == "/v1/jobs":
			io.WriteString(w, `[{"id":"a","session_id":"s1","request_id":"r1","status":"unknown","generation":"g1","command":"one"},{"id":"b","session_id":"s2","request_id":"r1","status":"unknown","generation":"g2","command":"two"},{"id":"c","session_id":"s1","request_id":"r2","status":"queued"}]`)
		case r.URL.Path == "/v1/sessions":
			io.WriteString(w, `[{"id":"s1","name":"Same","environment":"A","state":"active"},{"id":"s2","name":"Same","environment":"a","state":"active"},{"id":"s3","name":"same","environment":"A","state":"pending"}]`)
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "fixture.json")
	b, _ := json.Marshal(map[string]string{"endpoint": srv.URL, "token": strings.Repeat("a", 32)})
	if err := os.WriteFile(cfg, b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args     []string
		contains string
	}{
		{[]string{"help", "exec"}, "--request-id"}, {[]string{"help", "job", "wait"}, "--wait-ms"},
		{[]string{"exec", "--help"}, "--generation"}, {[]string{"exec", "session", "--help"}, "--generation"}, {[]string{"job", "wait", "--help"}, "--wait-ms"}, {[]string{"job", "wait", "job", "--help"}, "--wait-ms"},
		{[]string{"list", "jobs", "--help"}, "--session-id"},
	} {
		code, err, out := invokeDiscovery(t, append([]string{"--config", filepath.Join(t.TempDir(), "missing")}, tc.args...)...)
		if code != 0 || err != nil || !strings.Contains(out, tc.contains) {
			t.Errorf("help %v: %d %v %s", tc.args, code, err, out)
		}
	}
	for _, args := range [][]string{{"help", "missing"}, {"help", "job", "missing"}, {"missing", "--help"}} {
		code, _, _ := invokeDiscovery(t, args...)
		if code == 0 {
			t.Errorf("unknown help accepted: %v", args)
		}
	}
	for _, args := range [][]string{{"list", "jobs", "--status", "active"}, {"list", "sessions", "--state", "running"}, {"list", "jobs", "--bogus", "x"}, {"list", "jobs", "--request-id", ""}, {"list", "sessions", "--name", ""}, {"list", "audit", "--status", "pending"}} {
		code, err, _ := invokeDiscovery(t, append([]string{"--config", filepath.Join(t.TempDir(), "missing")}, args...)...)
		if code != 1 || err == nil || strings.Contains(err.Error(), "missing") {
			t.Errorf("validation before config %v: %d %v", args, code, err)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("help or invalid arguments used network: %v", calls)
	}
	if registrations != 0 {
		t.Fatalf("help or invalid arguments registered runtime: %d", registrations)
	}
	for _, tc := range []struct {
		args []string
		ids  string
	}{
		{[]string{"list", "jobs"}, "a,b,c"},
		{[]string{"list", "jobs", "--request-id", "r1"}, "a,b"},
		{[]string{"list", "jobs", "--request-id", "r1", "--session-id", "s1", "--status", "unknown"}, "a"},
		{[]string{"list", "jobs", "--session-id", "missing"}, ""},
		{[]string{"list", "sessions", "--name", "Same", "--environment", "A", "--state", "active"}, "s1"},
		{[]string{"list", "sessions", "--name", "Same"}, "s1,s2"},
		{[]string{"list", "sessions", "--name", "same", "--environment", "A"}, "s3"},
		{[]string{"list", "sessions", "--environment", "missing"}, ""},
	} {
		code, err, out := invokeDiscovery(t, append([]string{"--config", cfg}, tc.args...)...)
		var items []map[string]any
		if code != 0 || err != nil || json.Unmarshal([]byte(out), &items) != nil || items == nil {
			t.Errorf("%v: %d %v %s", tc.args, code, err, out)
			continue
		}
		ids := []string{}
		for _, item := range items {
			ids = append(ids, item["id"].(string))
		}
		if strings.Join(ids, ",") != tc.ids {
			t.Errorf("%v: got %v want %s", tc.args, ids, tc.ids)
		}
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "GET /v1/") || strings.Contains(call, "?") {
			t.Errorf("unexpected filter side effect: %s", call)
		}
	}
	code, err, _ := invokeDiscovery(t, "--config", cfg, "exec", "s1", "--generation", "g1", "--type", "xshell", "--request-id", "r", "--cwd", "/", "--", "--help")
	if code != 0 || err != nil || submitted["command"] != "--help" {
		t.Errorf("remote --help intercepted: %d %v %v", code, err, submitted)
	}
	code, err, _ = invokeDiscovery(t, "--config", cfg, "exec", "s1", "--generation", "g1", "--type", "xshell", "--request-id", "r", "hostname", "--help")
	if code != 0 || err != nil || submitted["command"] != "hostname --help" {
		t.Errorf("remote trailing --help intercepted: %d %v %v", code, err, submitted)
	}
	code, err, out := invokeDiscovery(t, "--config", cfg, "list", "jobs", "--request-id", "--help")
	if code != 0 || err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("help as filter value intercepted: %d %v %s", code, err, out)
	}
}

func TestHelpFlagAsArgumentValue(t *testing.T) {
	for _, args := range [][]string{
		{"list", "jobs", "--request-id", "--help"},
		{"list", "sessions", "--name", "--help"},
		{"exec", "session", "--generation", "--help"},
		{"job", "wait", "job", "--wait-ms", "--help"},
		{"exec", "session", "--generation", "g", "--type", "xshell", "hostname", "--help"},
	} {
		if handled, _ := commandHelpRequest(args); handled {
			t.Errorf("help intercepted value or remote command: %v", args)
		}
	}
}

func TestInvalidSyntaxBeforeConfiguration(t *testing.T) {
	oldRegister := registerProcess
	registered := 0
	registerProcess = func(string) func() { registered++; return func() {} }
	defer func() { registerProcess = oldRegister }()
	for _, args := range [][]string{
		{"version", "extra"}, {"doctor", "extra"}, {"mcp", "--bogus"},
		{"exec", "session", "--generation", "g", "--type", "ssh", "--", "hostname"},
		{"exec", "session", "--bogus"}, {"resolve", "session", "--fuzzy=maybe"},
		{"job", "wait", "id", "--bogus"}, {"job", "invalid", "id"}, {"no-such-command"},
	} {
		code, err, _ := invokeDiscovery(t, append([]string{"--config", filepath.Join(t.TempDir(), "missing")}, args...)...)
		if code != 1 || err == nil || strings.Contains(err.Error(), "missing") {
			t.Errorf("invalid syntax %v: %d %v", args, code, err)
		}
	}
	if registered != 0 {
		t.Fatalf("invalid syntax registered runtime: %d", registered)
	}
}
