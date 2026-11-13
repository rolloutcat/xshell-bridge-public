package runlock

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHelper(t *testing.T) {
	if os.Getenv("RUNLOCK_HELPER") != "1" {
		return
	}
	g, err := Acquire(os.Getenv("RUNLOCK_DIR"))
	if err != nil {
		fmt.Println("refused")
		os.Exit(2)
	}
	fmt.Println("ready")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	if err := g.Close(); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func start(t *testing.T, dir string) (*exec.Cmd, func()) {
	t.Helper()
	c := exec.Command(os.Args[0], "-test.run=^TestHelper$")
	c.Env = append(os.Environ(), "RUNLOCK_HELPER=1", "RUNLOCK_DIR="+dir)
	in, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = in.Close() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("helper: %q %v", line, err)
	}
	return c, func() {
		_ = in.Close()
		if err := c.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLiveOwnerAndForcedKillRecovery(t *testing.T) {
	d := t.TempDir()
	state := filepath.Join(d, "state.json")
	want := "isolated state: sessions invalidated and unknown jobs never replayed by store"
	if err := os.WriteFile(state, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	c, _ := start(t, d)
	if g, err := Acquire(d); err == nil {
		g.Close()
		t.Fatal("live owner accepted")
	}
	if err := c.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = c.Wait()
	g, err := Acquire(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	stale, _ := filepath.Glob(filepath.Join(d, "bridge.lock.stale-*"))
	if len(stale) != 1 {
		t.Fatal(stale)
	}
	b, _ := os.ReadFile(state)
	if string(b) != want {
		t.Fatal("state changed")
	}
}

func TestSimultaneousStart(t *testing.T) {
	d := t.TempDir()
	gate := make(chan struct{})
	results := make(chan *Guard, 2)
	for i := 0; i < 2; i++ {
		go func() { <-gate; g, _ := Acquire(d); results <- g }()
	}
	close(gate)
	a, b := <-results, <-results
	if (a == nil) == (b == nil) {
		t.Fatal("expected exactly one owner")
	}
	if a != nil {
		a.Close()
	}
	if b != nil {
		b.Close()
	}
}

func TestRelease(t *testing.T) {
	d := t.TempDir()
	c, stop := start(t, d)
	_ = c
	before, err := os.Stat(filepath.Join(d, ".bridge-owner.lock"))
	if err != nil {
		t.Fatal(err)
	}
	stop()
	after, err := os.Stat(filepath.Join(d, ".bridge-owner.lock"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("guard inode changed")
	}
	if _, err = os.Lstat(filepath.Join(d, "bridge.lock")); !os.IsNotExist(err) {
		t.Fatal("marker retained")
	}
	g, err := Acquire(d)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
}

func TestLegacyPIDAndMalformed(t *testing.T) {
	// Obtain a demonstrably exited PID without depending on a platform PID ceiling.
	d := t.TempDir()
	c, stop := start(t, d)
	pid := c.Process.Pid
	stop()
	marker := filepath.Join(d, "bridge.lock")
	old := strconv.Itoa(pid) + "\n"
	if err := os.WriteFile(marker, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	g, err := Acquire(d)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	paths, _ := filepath.Glob(filepath.Join(d, "bridge.lock.stale-*"))
	if len(paths) != 1 {
		t.Fatal(paths)
	}
	b, _ := os.ReadFile(paths[0])
	if string(b) != old {
		t.Fatal("evidence changed")
	}
	for _, s := range []string{"", "0", "-1", "1 junk", " 1", "999999999999999999999999", strconv.Itoa(os.Getpid())} {
		os.WriteFile(marker, []byte(s), 0600)
		if g, err := Acquire(d); err == nil {
			g.Close()
			t.Fatalf("accepted %q", s)
		}
		b, _ := os.ReadFile(marker)
		if string(b) != s {
			t.Fatal("refusal modified marker")
		}
	}
}

func TestLauncherContract(t *testing.T) {
	b, err := os.ReadFile("../../scripts/start-desktop.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"Bridge lock exists;", "Stop-Process", "Get-Content -LiteralPath $errorLog"} {
		if strings.Contains(s, bad) {
			t.Fatal(bad)
		}
	}
	for _, want := range []string{"复用当前 Bridge", "原始 PID", "配置或监听冲突", "$healthyBeforeStart"} {
		if !strings.Contains(s, want) {
			t.Fatal(want)
		}
	}
}
