//go:build windows

package processstatus

import (
	"bufio"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRoleAndIdentityScopedStatus(t *testing.T) {
	release := Register("mcp")
	defer release()
	report := Snapshot()
	var found bool
	for _, p := range report.Processes {
		if p.PID == uint32(os.Getpid()) {
			found = true
			if p.Role != "mcp" || p.ParentPID == 0 || p.ParentStatus != "alive" {
				t.Fatalf("unexpected self: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("current process absent from snapshot")
	}
	path, err := executable(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	if roleFor(uint32(os.Getpid()), 1, path) != "unknown" {
		t.Fatal("accepted wrong creation time")
	}
}

func TestProcessRoleFixture(t *testing.T) {
	role := os.Getenv("XSH_STATUS_FIXTURE")
	if role == "" {
		return
	}
	done := Register(role)
	defer done()
	fmt.Fprintln(os.Stdout, "ready")
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
}
func TestIndependentProcessesReportRoles(t *testing.T) {
	expected := map[uint32]string{}
	for _, role := range []string{"bridge", "mcp", "mcp", "cli"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRoleFixture$")
		cmd.Env = append(os.Environ(), "XSH_STATUS_FIXTURE="+role)
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { in.Close(); cmd.Wait() })
		line, err := bufio.NewReader(out).ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "ready" {
			t.Fatalf("child startup: %q %v", line, err)
		}
		expected[uint32(cmd.Process.Pid)] = role
	}
	report := Snapshot()
	for _, p := range report.Processes {
		if role, ok := expected[p.PID]; ok {
			if p.Role != role || p.ParentPID != uint32(os.Getpid()) || p.ParentStatus != "alive" {
				t.Fatalf("wrong child: %+v expected %s", p, role)
			}
			delete(expected, p.PID)
		}
	}
	if len(expected) != 0 {
		t.Fatalf("missing fixture processes: %v", expected)
	}
}
