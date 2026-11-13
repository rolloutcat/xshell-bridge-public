//go:build windows

package mcpserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

func TestMCPProcessFixture(t *testing.T) {
	switch os.Getenv("XSH_MCP_FIXTURE") {
	case "child", "health-child", "blocked-child":
		endpoint := "http://127.0.0.1:0"
		if os.Getenv("XSH_MCP_FIXTURE") != "child" {
			endpoint = os.Getenv("XSH_MCP_ENDPOINT")
		}
		c, err := client.New(client.Config{Endpoint: endpoint, Token: fixtureToken})
		if err != nil {
			os.Exit(5)
		}
		fmt.Fprintln(os.Stderr, "child-ready")
		if os.Getenv("XSH_MCP_FIXTURE") != "child" {
			timeout := 100 * time.Millisecond
			if os.Getenv("XSH_MCP_FIXTURE") == "blocked-child" {
				timeout = 5 * time.Second
			}
			_ = runWithHealth(context.Background(), c, healthOptions{10 * time.Millisecond, 150 * time.Millisecond, timeout})
		} else {
			_ = Run(context.Background(), c)
		}
		return
	case "parent":
		cmd := exec.Command(os.Args[0], "-test.run=^TestMCPProcessFixture$")
		cmd.Env = append(os.Environ(), "XSH_MCP_FIXTURE=child")
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			os.Exit(4)
		}
		fmt.Fprintln(os.Stderr, "pid="+strconv.Itoa(cmd.Process.Pid))
		time.Sleep(2 * time.Second)
		os.Exit(0)
	default:
		return
	}
}
func startFixture(t *testing.T, role string, endpoint ...string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPProcessFixture$")
	cmd.Env = append(os.Environ(), "XSH_MCP_FIXTURE="+role)
	if len(endpoint) > 0 {
		cmd.Env = append(cmd.Env, "XSH_MCP_ENDPOINT="+endpoint[0])
	}
	in, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	errout, e := cmd.StderrPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	return cmd, in, out, errout
}

func TestMCPExitsAfterFixtureBridgeStops(t *testing.T) {
	f, c := newHealthFixture(t)
	cmd, in, _, stderr := startFixture(t, "health-child", c.Endpoint)
	defer in.Close()
	defer cmd.Process.Kill()
	if line, _ := bufio.NewReader(stderr).ReadString('\n'); !strings.Contains(line, "child-ready") {
		t.Fatal(line)
	}
	waitProbes(t, f, 0, 3)
	f.server.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("MCP survived sustained fixture Bridge unavailability")
	}
}

func TestMCPStdioEOFCancelsHealthProbe(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer s.Close()
	cmd, in, _, stderr := startFixture(t, "blocked-child", s.URL)
	defer cmd.Process.Kill()
	if line, _ := bufio.NewReader(stderr).ReadString('\n'); !strings.Contains(line, "child-ready") {
		t.Fatal(line)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("health probe did not start")
	}
	in.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("MCP did not exit after EOF")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("health probe was not cancelled")
	}
}
func TestMCPStdioEOFExits(t *testing.T) {
	cmd, in, _, stderr := startFixture(t, "child")
	defer cmd.Process.Kill()
	if line, _ := bufio.NewReader(stderr).ReadString('\n'); !strings.Contains(line, "child-ready") {
		t.Fatal(line)
	}
	in.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("MCP did not exit after EOF")
	}
}
func TestMCPParentExitEvenWithInheritedPipe(t *testing.T) {
	cmd, in, out, stderr := startFixture(t, "parent")
	defer in.Close()
	defer cmd.Process.Kill()
	reader := bufio.NewReader(stderr)
	pid := 0
	ready := false
	for i := 0; i < 2; i++ {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "pid=") {
			pid, _ = strconv.Atoi(strings.TrimPrefix(line, "pid="))
		}
		if line == "child-ready" {
			ready = true
		}
	}
	if pid == 0 || !ready {
		t.Fatal("fixture child not ready")
	}
	child, e := os.FindProcess(pid)
	if e != nil {
		t.Fatal(e)
	}
	defer child.Kill()
	// Keep the stdin writer open. A live, idle client must not be timed out.
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, out); close(done) }()
	select {
	case <-done:
		t.Fatal("idle MCP exited while parent alive")
	case <-time.After(700 * time.Millisecond):
	}
	// Wait closes StdoutPipe, so only wait after observing EOF from the child.
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("MCP survived parent exit with inherited stdin open")
	}
	_ = cmd.Wait()
}
