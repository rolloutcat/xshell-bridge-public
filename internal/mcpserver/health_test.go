package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

const fixtureToken = "test-token-012345678901234567890123456789"

type healthFixture struct {
	mode        atomic.Int32
	requests    chan int32
	badRequests atomic.Int32
	server      *httptest.Server
}

func newHealthFixture(t *testing.T) (*healthFixture, *client.Client) {
	t.Helper()
	f := &healthFixture{requests: make(chan int32, 1000)}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/health" || r.Header.Get("Authorization") != "Bearer "+fixtureToken {
			f.badRequests.Add(1)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		mode := f.mode.Load()
		switch mode {
		case 0:
			fmt.Fprint(w, `{"protocol":1,"version":"fixture"}`)
		case 1:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case 2:
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case 3:
			fmt.Fprint(w, `{"protocol":1}`)
		case 4:
			fmt.Fprint(w, `{"protocol":2,"version":"fixture"}`)
		case 5:
			fmt.Fprint(w, `{"protocol":1,"version":42}`)
		case 6:
			fmt.Fprint(w, `not json`)
		case 7:
			fmt.Fprint(w, `{"protocol":1,"version":"  "}`)
		case 8:
			f.requests <- mode
			<-r.Context().Done()
			return
		}
		f.requests <- mode
	}))
	f.server = s
	t.Cleanup(s.Close)
	c, err := client.New(client.Config{Endpoint: s.URL, Token: fixtureToken})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if n := f.badRequests.Load(); n != 0 {
			t.Errorf("unexpected or unauthenticated requests: %d", n)
		}
	})
	return f, c
}

func waitProbes(t *testing.T, f *healthFixture, mode int32, n int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for n > 0 {
		select {
		case got := <-f.requests:
			if got == mode {
				n--
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for probe mode %d", mode)
		}
	}
}

func startWatch(t *testing.T, c *client.Client, opts healthOptions) (<-chan bool, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- watchBridge(ctx, c, opts); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("watcher did not exit")
		}
	})
	return done, cancel
}

func assertRunning(t *testing.T, done <-chan bool, d time.Duration) {
	t.Helper()
	select {
	case expired := <-done:
		t.Fatalf("watcher exited unexpectedly: %v", expired)
	case <-time.After(d):
	}
}

func assertExpired(t *testing.T, done <-chan bool) {
	t.Helper()
	select {
	case expired := <-done:
		if !expired {
			t.Fatal("watcher did not report sustained unavailability")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not expire")
	}
}

func TestWatchBridgeStartupAndHealthyIdle(t *testing.T) {
	f, c := newHealthFixture(t)
	f.mode.Store(1)
	opts := healthOptions{10 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond}
	done, _ := startWatch(t, c, opts)
	waitProbes(t, f, 1, 2)
	assertRunning(t, done, 160*time.Millisecond)
	f.mode.Store(0)
	waitProbes(t, f, 0, 3)
	assertRunning(t, done, 160*time.Millisecond)
}

func TestWatchBridgeExpiresAfterUnavailable(t *testing.T) {
	f, c := newHealthFixture(t)
	done, _ := startWatch(t, c, healthOptions{10 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond})
	waitProbes(t, f, 0, 3)
	f.mode.Store(1)
	assertExpired(t, done)
}

func TestWatchBridgeRecoveryResetsWindow(t *testing.T) {
	f, c := newHealthFixture(t)
	done, _ := startWatch(t, c, healthOptions{10 * time.Millisecond, 300 * time.Millisecond, 100 * time.Millisecond})
	waitProbes(t, f, 0, 3)
	f.mode.Store(1)
	waitProbes(t, f, 1, 2)
	assertRunning(t, done, 170*time.Millisecond)
	f.mode.Store(0)
	waitProbes(t, f, 0, 3)
	f.mode.Store(1)
	waitProbes(t, f, 1, 2)
	assertRunning(t, done, 170*time.Millisecond)
	assertExpired(t, done)
}

func TestWatchBridgeRejectsUnhealthyResponses(t *testing.T) {
	for _, mode := range []int32{2, 3, 4, 5, 6, 7} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			f, c := newHealthFixture(t)
			done, _ := startWatch(t, c, healthOptions{10 * time.Millisecond, 80 * time.Millisecond, 100 * time.Millisecond})
			waitProbes(t, f, 0, 3)
			f.mode.Store(mode)
			waitProbes(t, f, mode, 2)
			assertExpired(t, done)
		})
	}
}

func TestWatchBridgeTimesOutStalledProbes(t *testing.T) {
	f, c := newHealthFixture(t)
	done, _ := startWatch(t, c, healthOptions{10 * time.Millisecond, 120 * time.Millisecond, 60 * time.Millisecond})
	waitProbes(t, f, 0, 3)
	f.mode.Store(8)
	waitProbes(t, f, 8, 2)
	assertExpired(t, done)
}

func TestWatchBridgeCancelInterruptsProbe(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	t.Cleanup(s.Close)
	c, err := client.New(client.Config{Endpoint: s.URL, Token: fixtureToken})
	if err != nil {
		t.Fatal(err)
	}
	done, cancel := startWatch(t, c, healthOptions{10 * time.Millisecond, time.Second, 5 * time.Second})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	cancel()
	select {
	case expired := <-done:
		if expired {
			t.Fatal("cancellation reported as unavailability")
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not cancel")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("probe was not canceled")
	}
}
