package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
)

type consoleFixture struct {
	mu     sync.Mutex
	reads  int
	posts  int
	onRead func(int) []bridge.Job
	notify chan struct{}
}

func (f *consoleFixture) Call(_ context.Context, method, path string, input, output any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if method == "POST" {
		f.posts++
		return nil
	}
	switch path {
	case "/v1/sessions":
		*output.(*[]bridge.Session) = nil
	case "/v1/jobs":
		f.reads++
		*output.(*[]bridge.Job) = f.onRead(f.reads)
		if f.notify != nil {
			f.notify <- struct{}{}
		}
	default:
		return fmt.Errorf("unexpected request")
	}
	return nil
}
func pendingFixture() []bridge.Job {
	return []bridge.Job{{ID: "new-job", Status: "pending", Submit: bridge.Submit{SessionID: "fixture", Command: "hostname", Cwd: "/"}}}
}
func TestConsoleRefreshesBeforeFirstApprovalAttempt(t *testing.T) {
	f := &consoleFixture{onRead: func(n int) []bridge.Job {
		if n == 1 {
			return nil
		}
		return pendingFixture()
	}}
	lines := make(chan consoleLine, 3)
	lines <- consoleLine{text: "allow new-job"}
	lines <- consoleLine{text: "yes"}
	lines <- consoleLine{text: "quit"}
	close(lines)
	var out bytes.Buffer
	if err := consoleLoop(context.Background(), f, lines, &out, nil); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || strings.Contains(out.String(), "pending item not found") {
		t.Fatalf("first command did not approve exactly once: posts=%d output=%s", f.posts, out.String())
	}
	if !strings.Contains(out.String(), `command "hostname"`) {
		t.Fatal("exact command not presented")
	}
}
func TestConsoleStillRequiresExactHumanConfirmation(t *testing.T) {
	for _, confirmation := range []string{"", "no", "YES", "quit"} {
		t.Run(confirmation, func(t *testing.T) {
			f := &consoleFixture{onRead: func(int) []bridge.Job { return pendingFixture() }}
			lines := make(chan consoleLine, 2)
			lines <- consoleLine{text: "allow new-job"}
			lines <- consoleLine{text: confirmation}
			close(lines)
			if err := consoleLoop(context.Background(), f, lines, &bytes.Buffer{}, nil); err != nil {
				t.Fatal(err)
			}
			if f.posts != 0 {
				t.Fatal("approved without exact yes")
			}
		})
	}
}

type lockedOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (w *lockedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.Write(p)
}
func (w *lockedOutput) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.data.String() }
func TestConsoleShowsNewJobsWithoutInput(t *testing.T) {
	f := &consoleFixture{notify: make(chan struct{}, 10), onRead: func(n int) []bridge.Job {
		if n == 1 {
			return nil
		}
		return pendingFixture()
	}}
	lines := make(chan consoleLine)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out lockedOutput
	done := make(chan error, 1)
	go func() { done <- consoleLoop(ctx, f, lines, &out, ticks) }()
	select {
	case <-f.notify:
	case <-ctx.Done():
		t.Fatal("no initial refresh")
	}
	select {
	case ticks <- time.Now():
	case <-ctx.Done():
		t.Fatal("no timer refresh")
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(out.String(), "new-job") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(out.String(), "new-job") {
		t.Fatal("new job not displayed automatically")
	}
	close(lines)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.posts != 0 {
		t.Fatal("refresh performed a write")
	}
}
