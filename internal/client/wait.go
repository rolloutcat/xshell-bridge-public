package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
)

type WaitResult struct {
	Job      bridge.Job `json:"job"`
	Terminal bool       `json:"terminal"`
	TimedOut bool       `json:"timed_out"`
}

// WaitJob observes an existing job without submitting or changing it.
func (c *Client) WaitJob(ctx context.Context, id string, waitMS int) (*WaitResult, error) {
	return c.waitJob(ctx, id, waitMS, time.Second)
}

func (c *Client) waitJob(ctx context.Context, id string, waitMS int, interval time.Duration) (*WaitResult, error) {
	if strings.TrimSpace(id) == "" || waitMS < 1 || waitMS > 30000 {
		return nil, fmt.Errorf("id and wait_ms in [1,30000] required")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(waitMS)*time.Millisecond)
	defer cancel()
	var last *WaitResult
	finish := func() (*WaitResult, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if last == nil {
			return nil, fmt.Errorf("wait deadline: no successful job observation")
		}
		last.TimedOut = true
		return last, nil
	}
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if bounded.Err() != nil {
			return finish()
		}
		var job bridge.Job
		err := c.Call(bounded, "GET", "/v1/job?id="+url.QueryEscape(id), nil, &job)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			if bounded.Err() != nil {
				return finish()
			}
			return nil, err
		}
		if job.ID != id {
			return nil, fmt.Errorf("job ID mismatch")
		}
		terminal := false
		switch job.Status {
		case "completed", "denied", "cancelled", "unknown":
			terminal = true
		case "pending", "queued", "dispatched", "running":
		default:
			return nil, fmt.Errorf("unexpected job status %q", job.Status)
		}
		job.Output = ""
		last = &WaitResult{Job: job, Terminal: terminal}
		if terminal {
			return last, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-bounded.Done():
			timer.Stop()
			return finish()
		case <-timer.C:
		}
	}
}
