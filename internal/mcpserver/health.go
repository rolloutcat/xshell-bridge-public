package mcpserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/rolloutcat/xshell-bridge-public/internal/client"
)

type healthOptions struct {
	interval, unavailable, timeout time.Duration
}

const (
	healthInterval    = 5 * time.Second
	healthUnavailable = 30 * time.Second
	healthTimeout     = 3 * time.Second
)

// watchBridge returns true only when an observed Bridge remains unavailable.
func watchBridge(ctx context.Context, c *client.Client, opts healthOptions) bool {
	observed := false
	var firstFailure time.Time
	for {
		if ctx.Err() != nil {
			return false
		}
		timeout := opts.timeout
		if !firstFailure.IsZero() {
			remaining := time.Until(firstFailure.Add(opts.unavailable))
			if remaining <= 0 {
				return true
			}
			if remaining < timeout {
				timeout = remaining
			}
		}
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		var health struct {
			Protocol int    `json:"protocol"`
			Version  string `json:"version"`
		}
		err := c.Call(probeCtx, http.MethodGet, "/v1/health", nil, &health)
		cancel()
		if ctx.Err() != nil {
			return false
		}
		if !firstFailure.IsZero() && time.Until(firstFailure.Add(opts.unavailable)) <= 0 {
			return true
		}
		if err == nil && health.Protocol == 1 && strings.TrimSpace(health.Version) != "" {
			observed = true
			firstFailure = time.Time{}
		} else if observed && firstFailure.IsZero() {
			firstFailure = time.Now()
		}
		wait := opts.interval
		if !firstFailure.IsZero() {
			remaining := time.Until(firstFailure.Add(opts.unavailable))
			if remaining <= 0 {
				return true
			}
			if remaining < wait {
				wait = remaining
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
		if !firstFailure.IsZero() && time.Until(firstFailure.Add(opts.unavailable)) <= 0 {
			return true
		}
	}
}
