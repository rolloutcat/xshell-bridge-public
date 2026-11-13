package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
)

func TestWaitJob(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		statuses                   []string
		mismatch, httpError        bool
		wantErr, terminal, timeout bool
	}{
		{name: "progress", statuses: []string{"pending", "queued", "completed"}, terminal: true},
		{name: "unknown", statuses: []string{"unknown"}, terminal: true},
		{name: "completed", statuses: []string{"completed"}, terminal: true},
		{name: "denied", statuses: []string{"denied"}, terminal: true},
		{name: "cancelled", statuses: []string{"cancelled"}, terminal: true},
		{name: "deadline", statuses: []string{"pending"}, timeout: true},
		{name: "mismatch", statuses: []string{"completed"}, mismatch: true, wantErr: true},
		{name: "unexpected", statuses: []string{"surprise"}, wantErr: true},
		{name: "http", httpError: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/job" || r.URL.Query().Get("id") != "a&b" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				n := int(calls.Add(1))
				if tc.httpError {
					http.Error(w, "fail", 500)
					return
				}
				i := n - 1
				if i >= len(tc.statuses) {
					i = len(tc.statuses) - 1
				}
				id := "a&b"
				if tc.mismatch {
					id = "other"
				}
				json.NewEncoder(w).Encode(map[string]interface{}{"id": id, "status": tc.statuses[i], "exit_code": 7, "output": "must not escape"})
			}))
			defer srv.Close()
			c := &Client{Config: Config{Endpoint: srv.URL}, HTTP: srv.Client()}
			ms := 1000
			if tc.timeout {
				ms = 200
			}
			out, err := c.waitJob(context.Background(), "a&b", ms, time.Millisecond)
			if (err != nil) != tc.wantErr {
				t.Fatalf("%+v %v", out, err)
			}
			if !tc.wantErr && (out.Terminal != tc.terminal || out.TimedOut != tc.timeout || out.Job.Output != "") {
				t.Fatalf("%+v", out)
			}
			if (tc.wantErr || (tc.terminal && len(tc.statuses) == 1)) && calls.Load() != 1 {
				t.Fatalf("calls=%d", calls.Load())
			}
			if !tc.wantErr {
				b, _ := json.Marshal(out.Job)
				var job map[string]interface{}
				if err := json.Unmarshal(b, &job); err != nil {
					t.Fatal(err)
				}
				if job["exit_code"] != float64(7) {
					t.Fatalf("exit code lost: %s", b)
				}
			}
		})
	}
}

func TestWaitValidationAndCancellation(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		json.NewEncoder(w).Encode(bridge.Job{ID: "id", Status: "pending"})
	}))
	defer srv.Close()
	c := &Client{Config: Config{Endpoint: srv.URL}, HTTP: srv.Client()}
	for _, ms := range []int{-1, 0, 30001} {
		if _, err := c.WaitJob(context.Background(), "id", ms); err == nil {
			t.Fatal("accepted invalid bound")
		}
	}
	if _, err := c.WaitJob(context.Background(), "", 1); err == nil {
		t.Fatal("accepted empty ID")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.WaitJob(ctx, "id", 100); err == nil {
		t.Fatal("ignored cancellation")
	}
	if calls.Load() != 0 {
		t.Fatal("validation used network")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		select {
		case <-started:
			cancel()
		case <-ctx.Done():
		}
	}()
	if _, err := c.WaitJob(ctx, "id", 1000); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation treated as timeout: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func TestWaitStalledGET(t *testing.T) {
	for _, mode := range []string{"first", "after-pending", "cancel", "error-after-pending"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			started := make(chan struct{}, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if (mode == "after-pending" || mode == "error-after-pending") && n == 1 {
					json.NewEncoder(w).Encode(bridge.Job{ID: "id", Status: "pending"})
					return
				}
				if mode == "error-after-pending" {
					http.Error(w, "failure", 500)
					return
				}
				select {
				case started <- struct{}{}:
				default:
				}
				<-r.Context().Done()
			}))
			defer srv.Close()
			c := &Client{Config: Config{Endpoint: srv.URL}, HTTP: srv.Client()}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			ms := 200
			if mode == "cancel" {
				ms = 1000
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			out, err := c.waitJob(ctx, "id", ms, time.Millisecond)
			switch mode {
			case "first":
				if err == nil || !strings.Contains(err.Error(), "no successful job observation") || out != nil {
					t.Fatalf("%+v %v", out, err)
				}
			case "after-pending":
				if err != nil || out == nil || !out.TimedOut || out.Terminal || out.Job.Status != "pending" {
					t.Fatalf("%+v %v", out, err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || out != nil {
					t.Fatalf("%+v %v", out, err)
				}
			case "error-after-pending":
				if err == nil || out != nil {
					t.Fatalf("%+v %v", out, err)
				}
			}
			want := int32(1)
			if mode == "after-pending" || mode == "error-after-pending" {
				want = 2
			}
			if calls.Load() != want {
				t.Fatalf("calls=%d want=%d", calls.Load(), want)
			}
		})
	}
}
