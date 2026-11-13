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

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
)

func TestPolicyShowCLI(t *testing.T) {
	oldArgs, oldOut := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = oldArgs, oldOut }()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/command-policy" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"policy": map[string]any{"enabled": false}, "categories": []string{"fixture"}})
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "client.json")
	b, _ := json.Marshal(map[string]string{"endpoint": srv.URL, "token": strings.Repeat("c", 32)})
	if err := os.WriteFile(cfg, b, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(dir, "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdout = f
	os.Args = []string{"xshctl", "--config", cfg, "policy", "show"}
	code, err := run()
	_, _ = f.Seek(0, 0)
	out, _ := io.ReadAll(f)
	if code != 0 || err != nil || calls != 1 || !strings.Contains(string(out), "fixture") || !strings.Contains(string(out), "false") {
		t.Fatal(code, err, calls, string(out))
	}
	os.Args = []string{"xshctl", "--config", cfg, "policy", "show", "extra"}
	if code, err = run(); code != 1 || err == nil || calls != 1 {
		t.Fatal(code, err, calls)
	}
	os.Args = []string{"xshctl", "help", "policy", "show"}
	if code, err = run(); code != 0 || err != nil || calls != 1 {
		t.Fatal(code, err, calls)
	}
}

func TestPolicyV4CLIExplainAndSubmit(t *testing.T) {
	oldArgs, oldOut := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = oldArgs, oldOut }()
	dir := t.TempDir()
	store, err := bridge.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Register("synthetic-instance-081", "fixture", "isolated", "synthetic-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Activate(target.ID, target.Generation); err != nil {
		t.Fatal(err)
	}
	keys := bridge.Keys{Client: strings.Repeat("c", 32), Admin: strings.Repeat("a", 32), Agent: strings.Repeat("g", 32)}
	srv := httptest.NewServer(bridge.Handler(store, keys))
	defer srv.Close()
	cfg := filepath.Join(dir, "client.json")
	b, _ := json.Marshal(map[string]string{"endpoint": srv.URL, "token": keys.Client})
	if err = os.WriteFile(cfg, b, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(dir, "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdout = f
	call := func(args ...string) string {
		t.Helper()
		if err := f.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		os.Args = append([]string{"xshctl", "--config", cfg}, args...)
		code, err := run()
		if code != 0 || err != nil {
			t.Fatalf("%v: %d %v", args, code, err)
		}
		if _, err := f.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if out := call("policy", "show"); !strings.Contains(out, "builtin-readonly-v5") || !strings.Contains(out, "file_and_journal_read") {
		t.Fatal(out)
	}
	command := "tail -n 20 '/tmp/synthetic file' | grep -F 'warn'"
	if out := call("explain", target.ID, "--generation", target.Generation, "--cwd", "/", "--", command); !strings.Contains(out, `"decision": "builtin"`) {
		t.Fatal(out)
	}
	out := call("exec", target.ID, "--generation", target.Generation, "--type", "xshell", "--request-id", "synthetic-cli-v4", "--cwd", "/", "--", command)
	var job bridge.Job
	if err := json.Unmarshal([]byte(out), &job); err != nil {
		t.Fatal(err)
	}
	if job.Status != "queued" || job.Command != command || !strings.HasPrefix(job.BuiltinRuleID, bridge.BuiltinPackID+"/") {
		t.Fatal(job)
	}
	out = call("exec", target.ID, "--generation", target.Generation, "--type", "xshell", "--request-id", "synthetic-cli-v4", "--cwd", "/", "--", command)
	var again bridge.Job
	if err := json.Unmarshal([]byte(out), &again); err != nil || again.ID != job.ID {
		t.Fatal(again, err)
	}
	for _, dangerous := range []string{"rm -rf /tmp/fixture", "mysql -e 'DROP TABLE fixture'", "redis-cli FLUSHALL"} {
		if out := call("explain", target.ID, "--generation", target.Generation, "--cwd", "/", "--", dangerous); !strings.Contains(out, `"decision": "blocked"`) || !strings.Contains(out, "command_denied") {
			t.Fatal(out)
		}
		os.Args = []string{"xshctl", "--config", cfg, "exec", target.ID, "--generation", target.Generation, "--type", "xshell", "--request-id", "deny-fixture", "--cwd", "/", "--", dangerous}
		code, err := run()
		if code == 0 || err == nil || !strings.Contains(err.Error(), "COMMAND_DENIED") {
			t.Fatalf("CLI did not deny: %d %v", code, err)
		}
	}
	for _, readonly := range []string{"ls -lh /tmp", "tail -c 16777216 /tmp/fixture.log | grep -F fixture", "timeout -k 2s 25s grep -F -m 1 fixture /tmp/fixture.log"} {
		if out := call("explain", target.ID, "--generation", target.Generation, "--cwd", "/", "--timeout-ms", "35000", "--", readonly); !strings.Contains(out, `"decision": "builtin"`) {
			t.Fatal(out)
		}
		out := call("exec", target.ID, "--generation", target.Generation, "--type", "xshell", "--request-id", readonly, "--cwd", "/", "--timeout-ms", "35000", "--", readonly)
		var job bridge.Job
		if err := json.Unmarshal([]byte(out), &job); err != nil {
			t.Fatal(err)
		}
		if job.Status != "queued" || job.RequiresExactApproval || job.Command != readonly {
			t.Fatal(job)
		}
	}
}
