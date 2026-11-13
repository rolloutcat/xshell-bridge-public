//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorIncludesLocalProcessStatusWithoutCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/health" {
			t.Error("unexpected request")
		}
		fmt.Fprint(w, `{"version":"fixture"}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "fixture.json")
	token := strings.Repeat("f", 32)
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`{"endpoint":%q,"token":%q}`, srv.URL, token)), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	oldArgs, oldOut := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = oldArgs, oldOut }()
	os.Args = []string{"xshctl", "--config", cfg, "doctor"}
	os.Stdout = out
	code, e := run()
	if code != 0 || e != nil {
		t.Fatal(code, e)
	}
	if _, err = out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version string `json:"version"`
		Status  struct {
			Processes []struct {
				PID  int    `json:"pid"`
				Role string `json:"role"`
			} `json:"processes"`
			Note string `json:"note"`
		} `json:"local_process_status"`
	}
	if err = json.NewDecoder(out).Decode(&result); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range result.Status.Processes {
		if p.PID == os.Getpid() && p.Role == "cli" {
			found = true
		}
	}
	if result.Version != "fixture" || !found || !strings.Contains(result.Status.Note, "多个 MCP") {
		t.Fatalf("status not reported: %+v", result)
	}
	b, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), token) || strings.Contains(string(b), cfg) {
		t.Fatal("doctor exposed configuration")
	}
}

func TestProcessesWithoutConfig(t *testing.T) {
	t.Setenv("XSHCTL_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.json"))
	out, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	oldArgs, oldOut := os.Args, os.Stdout
	defer func() { os.Args, os.Stdout = oldArgs, oldOut }()
	os.Args = []string{"xshctl", "processes"}
	os.Stdout = out
	code, e := run()
	if code != 0 || e != nil {
		t.Fatal(code, e)
	}
	out.Seek(0, 0)
	var result struct {
		Available bool
		Processes []struct {
			PID  int
			Role string
		}
	}
	if e = json.NewDecoder(out).Decode(&result); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, p := range result.Processes {
		if p.PID == os.Getpid() && p.Role == "cli" {
			found = true
		}
	}
	if !result.Available || !found {
		t.Fatal("missing local CLI role")
	}
	os.Args = []string{"xshctl", "processes", "serve"}
	if code, _ = run(); code == 0 {
		t.Fatal("extra args accepted")
	}
}
