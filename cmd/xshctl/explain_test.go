package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExplainCLI(t *testing.T) {
	oldArgs, oldOut := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = oldArgs, oldOut }()
	calls := 0
	decision := "manual"
	want := map[string]any{"session_id": "session", "generation": "generation", "command": "printf hello\n", "cwd": "/fixture", "timeout_ms": float64(60000)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.RequestURI() != "/v1/explain" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		}
		var got map[string]any
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("scope: %#v", got)
		}
		json.NewEncoder(w).Encode(map[string]any{"decision": decision, "checks": []any{}})
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "client.json")
	b, _ := json.Marshal(map[string]string{"endpoint": srv.URL, "token": strings.Repeat("a", 32)})
	if err := os.WriteFile(cfg, b, 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "command.txt")
	if err := os.WriteFile(file, []byte("\ufeffprintf hello\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) (int, error, string) {
		t.Helper()
		f, err := os.CreateTemp(dir, "output")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		os.Stdout = f
		os.Args = append([]string{"xshctl", "--config", cfg, "explain", "session"}, args...)
		code, e := run()
		f.Seek(0, 0)
		text, _ := io.ReadAll(f)
		return code, e, string(text)
	}
	for _, args := range [][]string{
		{"--", "printf hello"},
		{"--generation", "generation", "--unknown", "x"},
		{"--generation", "generation", "--type", "xshell", "--", "printf hello"},
		{"--generation", "generation", "--request-id", "x", "--", "printf hello"},
		{"--generation", "generation", "extra", "--", "printf hello"},
		{"--generation", "generation", "--command-file", file, "--", "printf hello"},
		{"--generation", "generation", "--timeout-ms", "bad", "--", "printf hello"},
	} {
		if code, err, _ := invoke(args...); code != 1 || err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if calls != 0 {
		t.Fatal("invalid flags used HTTP")
	}
	for _, d := range []string{"manual", "blocked", "grant", "rule"} {
		decision = d
		code, err, text := invoke("--generation", "generation", "--cwd", "/fixture", "--command-file", file)
		var out map[string]any
		if code != 0 || err != nil || json.Unmarshal([]byte(text), &out) != nil || out["decision"] != d {
			t.Fatalf("%d %v %s", code, err, text)
		}
	}
	if calls != 4 {
		t.Fatal(calls)
	}
}
