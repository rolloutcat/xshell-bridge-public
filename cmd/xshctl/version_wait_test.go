package main

import (
	"encoding/json"
	xshellbridge "github.com/rolloutcat/xshell-bridge-public"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVersionAndWaitCLI(t *testing.T) {
	oldArgs, oldOut := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = oldArgs, oldOut }()
	invoke := func(args ...string) (int, error, string) {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "out")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		os.Stdout = f
		os.Args = append([]string{"xshctl"}, args...)
		code, e := run()
		f.Seek(0, 0)
		b, _ := io.ReadAll(f)
		return code, e, string(b)
	}
	code, err, text := invoke("version")
	if code != 0 || err != nil || strings.TrimSpace(text) != xshellbridge.Version {
		t.Fatalf("%d %v %s", code, err, text)
	}
	_, _, text = invoke("help")
	if !strings.Contains(text, "xshctl "+xshellbridge.Version) {
		t.Fatal(text)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" {
			t.Error(r.Method)
		}
		io.WriteString(w, `{"id":"id","status":"pending"}`)
	}))
	defer srv.Close()
	cfg := filepath.Join(t.TempDir(), "client.json")
	b, _ := json.Marshal(map[string]string{"endpoint": srv.URL, "token": strings.Repeat("a", 32)})
	if err := os.WriteFile(cfg, b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, bound := range []string{"0", "-1", "30001", "bad"} {
		code, err, _ = invoke("--config", cfg, "job", "wait", "id", "--wait-ms", bound)
		if code != 1 || err == nil {
			t.Fatal("invalid flag accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid flags used network")
	}
	code, err, text = invoke("--config", cfg, "job", "wait", "id", "--wait-ms", "250")
	var out struct {
		TimedOut bool `json:"timed_out"`
		Terminal bool `json:"terminal"`
	}
	if json.Unmarshal([]byte(text), &out) != nil || code != 2 || err != nil || !out.TimedOut || out.Terminal {
		t.Fatalf("%d %v %s", code, err, text)
	}
}
