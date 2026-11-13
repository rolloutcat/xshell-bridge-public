package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
	"golang.org/x/term"
)

type consoleAPI interface {
	Call(context.Context, string, string, any, any) error
}
type consoleLine struct {
	text string
	err  error
}

func console(ctx context.Context, dir string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stderr.Fd())) {
		return fmt.Errorf("NEEDS_TTY: operator console must run in the user's terminal on the Bridge machine")
	}
	c, err := client.Load(filepath.Join(dir, "admin.json"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan consoleLine)
	go func() {
		defer close(lines)
		scan := bufio.NewScanner(os.Stdin)
		for scan.Scan() {
			select {
			case lines <- consoleLine{text: scan.Text()}:
			case <-ctx.Done():
				return
			}
		}
		if err := scan.Err(); err != nil {
			select {
			case lines <- consoleLine{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	return consoleLoop(ctx, c, lines, os.Stdout, ticker.C)
}

// Timer refresh is read-only. Selection always fetches current pending state,
// then presents the immutable command/target for a separate exact "yes".
func consoleLoop(ctx context.Context, c consoleAPI, lines <-chan consoleLine, out io.Writer, ticks <-chan time.Time) error {
	var sessions []bridge.Session
	var jobs []bridge.Job
	var displayed string
	prompt := "activate <session-id> | allow <job-id> | deny <job-id> | Enter=refresh | quit > "
	refresh := func(force bool) error {
		if err := c.Call(ctx, "GET", "/v1/sessions", nil, &sessions); err != nil {
			return err
		}
		if err := c.Call(ctx, "GET", "/v1/jobs", nil, &jobs); err != nil {
			return err
		}
		var text strings.Builder
		fmt.Fprintln(&text, "\nPending sessions:")
		for _, s := range sessions {
			if s.State == "pending" {
				fmt.Fprintf(&text, "%s name=%q environment=%q fingerprint=%q\n", s.ID, s.Name, s.Environment, s.Fingerprint)
			}
		}
		fmt.Fprintln(&text, "Pending jobs:")
		for _, j := range jobs {
			if j.Status == "pending" {
				fmt.Fprintf(&text, "%s session=%s cwd=%q command=%q timeout_ms=%d\n", j.ID, j.SessionID, j.Cwd, j.Command, j.TimeoutMS)
			}
		}
		current := text.String()
		// Heartbeats do not redraw the prompt; only pending item changes do.
		if force || current != displayed {
			fmt.Fprint(out, current, prompt)
			displayed = current
		}
		return nil
	}
	fmt.Fprintln(out, "Pending items refresh every 2 seconds. Type one action; confirmation still requires yes.")
	if err := refresh(true); err != nil {
		return err
	}
	for {
		var line consoleLine
		var ok bool
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			if err := refresh(false); err != nil {
				return err
			}
			continue
		case line, ok = <-lines:
			if !ok {
				return nil
			}
			if line.err != nil {
				return line.err
			}
		}
		parts := strings.Fields(line.text)
		if len(parts) == 1 && parts[0] == "quit" {
			return nil
		}
		if len(parts) == 0 {
			if err := refresh(true); err != nil {
				return err
			}
			continue
		}
		if len(parts) != 2 {
			fmt.Fprintln(out, "invalid command")
			fmt.Fprint(out, prompt)
			continue
		}
		// Do not use the snapshot displayed before the user began typing.
		if err := refresh(false); err != nil {
			return err
		}
		var path string
		var payload any
		switch parts[0] {
		case "activate":
			for _, s := range sessions {
				if s.ID == parts[1] && s.State == "pending" {
					path = "/v1/admin/activate"
					payload = map[string]string{"id": s.ID, "generation": s.Generation}
					fmt.Fprintf(out, "Activate target %q / %q, fingerprint %q. Verify the final target and a plain Shell prompt in this manually attached terminal.\n", s.Name, s.Environment, s.Fingerprint)
				}
			}
		case "allow", "deny":
			for _, j := range jobs {
				if j.ID == parts[1] && j.Status == "pending" {
					path = "/v1/admin/decide"
					payload = map[string]any{"id": j.ID, "allow": parts[0] == "allow"}
					fmt.Fprintf(out, "%s exact command %q, cwd %q, session %s\n", parts[0], j.Command, j.Cwd, j.SessionID)
				}
			}
		}
		if path == "" {
			fmt.Fprintln(out, "pending item not found in current state; nothing approved")
			fmt.Fprint(out, prompt)
			continue
		}
		fmt.Fprint(out, "Type yes to confirm > ")
		// Pause display refresh while confirming, so the reviewed command stays visible.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok = <-lines:
			if !ok {
				return nil
			}
			if line.err != nil {
				return line.err
			}
		}
		if line.text != "yes" {
			fmt.Fprintln(out, "not approved")
		} else {
			if err := c.Call(ctx, "POST", path, payload, nil); err != nil {
				fmt.Fprintln(out, err)
			} else {
				fmt.Fprintln(out, "recorded")
			}
		}
		if err := refresh(true); err != nil {
			return err
		}
	}
}
