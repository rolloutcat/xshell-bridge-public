package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	xshellbridge "github.com/rolloutcat/xshell-bridge-public"
	"github.com/rolloutcat/xshell-bridge-public/internal/bridge"
	"github.com/rolloutcat/xshell-bridge-public/internal/client"
	"github.com/rolloutcat/xshell-bridge-public/internal/local"
	"github.com/rolloutcat/xshell-bridge-public/internal/mcpserver"
	"github.com/rolloutcat/xshell-bridge-public/internal/processstatus"
	"github.com/rolloutcat/xshell-bridge-public/internal/runlock"
)

var usage = `xshctl ` + xshellbridge.Version + ` — 复用已登录的 Xshell 会话
Host Bridge (private local runtime):
  xshctl init --dir <private-runtime-directory>
  xshctl serve --dir <private-runtime-directory>
  xshctl console --dir <private-runtime-directory>
Local diagnostics (no config or Bridge required):
  xshctl processes
Host (global --config must precede command):
  xshctl --config client.json list sessions|jobs|audit [filters]
  xshctl --config client.json get session <id>
  xshctl --config client.json resolve session --name NAME [--environment ENV] [--fuzzy[=true|false]]
  xshctl --config client.json exec <session-id> --generation <g> --type xshell [--request-id <id>] [--cwd <dir>] [--timeout-ms 60000] [--command-file <file>] -- <command>
  xshctl --config client.json explain <session-id> --generation <g> [--cwd <dir>] [--timeout-ms 60000] [--command-file <file>] -- <command>
  xshctl --config client.json policy show
  xshctl --config client.json job get|interrupt <id>
  xshctl --config client.json job output <id> [--cursor <byte-offset>]
  xshctl --config client.json job wait <id> [--wait-ms 30000]
  xshctl --config client.json detach <id> <generation>
  xshctl --config client.json doctor
  xshctl --config client.json mcp
Output is JSON. Submission returns pending (exit 3 = already registered, needs authorization; do not resubmit) or queued (matching default policy or operator grant/rule).
New installs and absent existing settings enable a builtin read-only policy with literal file reads from any path (including cat, grep, head and tail) on activated fresh exact ID/generation only (explicit absolute cwd, timeout <=60000 ms). Definite dangerous operations return COMMAND_DENIED without creating a job; otherwise unmatched commands stay pending for one operator click in the desktop. Never require a chat acknowledgement; automatically poll the original job while active. No client policy-write endpoint. V5 adds bounded ls, head/tail byte counts up to 67108864 (64 MiB), and one timeout wrapper around a fully read-only child. Line/context counts stay at 1000000. timeout accepts integer seconds (optional s), duration 1..60 and optional -k/--kill-after 1..10; their sum must fit the task timeout.
Wait is read-only, bounded to 1..30000 ms: exit 0 terminal, 2 timeout, 1 error. Remote exit_code remains in JSON. Read output separately with its cursor. unknown is terminal: stop, never replay. Chat acknowledgment is not approval.
Resolve is read-only: matched exits 0; ambiguous/suggestions/not_found exits 2; invalid input or request failure exits 1. It never executes a command.
Explain is read-only: current new-submission eligibility only, never approval of pending jobs. Hard-deny policy rejects definite destructive operations before all approvals. Uninspectable commands and scripts require fresh human approval (no remembered grants). The default builtin-readonly-v5 policy precedes temporary grants and rules; grants precede custom rules. Examples are informational and never replace or execute the original command. Operator opt-out persists. Do not rewrite commands to evade approval. Final execution still requires ID/generation. Valid explanations including manual/blocked exit 0; errors exit 1.
No SSH login, reconnect, direct terminal UI automation, or automatic retry.
For offline command help: xshctl help policy show, xshctl help exec, xshctl help list jobs, xshctl help job wait.
`

func printJSON(v any) { e := json.NewEncoder(os.Stdout); e.SetIndent("", "  "); _ = e.Encode(v) }

var registerProcess = processstatus.Register

func main() {
	code, e := run()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
	}
	os.Exit(code)
}
func run() (int, error) {
	global := flag.NewFlagSet("xshctl", flag.ContinueOnError)
	cfg := global.String("config", os.Getenv("XSHCTL_CONFIG"), "client config path")
	global.Usage = func() { fmt.Print(usage) }
	if e := global.Parse(os.Args[1:]); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0, nil
		}
		return 1, e
	}
	args := global.Args()
	if len(args) == 0 {
		fmt.Print(usage)
		return 0, nil
	}
	if handled, err := commandHelpRequest(args); handled {
		if err != nil {
			return 1, err
		}
		return 0, nil
	}
	if args[0] == "version" {
		if len(args) != 1 {
			return 1, fmt.Errorf("version does not accept arguments")
		}
		fmt.Println(xshellbridge.Version)
		return 0, nil
	}
	var filter *listFilter
	if args[0] == "list" {
		var err error
		filter, err = parseList(args)
		if err != nil {
			return 1, err
		}
	}
	role := "cli"
	if args[0] == "mcp" {
		role = "mcp"
	} else if args[0] == "serve" {
		role = "bridge"
	}
	unregister := func() {}
	defer func() { unregister() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if args[0] == "init" || args[0] == "serve" || args[0] == "console" {
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		dir := fs.String("dir", "", "host private runtime directory")
		if e := fs.Parse(args[1:]); e != nil {
			return 1, e
		}
		if *dir == "" || len(fs.Args()) != 0 {
			return 1, fmt.Errorf("--dir is required; unexpected arguments rejected")
		}
		unregister = registerProcess(role)
		var e error
		switch args[0] {
		case "init":
			e = local.Init(*dir)
			if e == nil {
				fmt.Println("Created private Bridge configuration. Tokens were not printed. Start with README.md for setup and usage.")
			}
		case "serve":
			e = serve(ctx, *dir)
		case "console":
			e = console(ctx, *dir)
		}
		if e != nil {
			return 1, e
		}
		return 0, nil
	}
	if args[0] == "processes" {
		if len(args) != 1 {
			return 1, fmt.Errorf("processes 不接受额外参数")
		}
		unregister = registerProcess(role)
		printJSON(processstatus.Snapshot())
		return 0, nil
	}
	var c *client.Client
	var e error
	connect := func() error {
		if c != nil {
			return nil
		}
		if *cfg == "" {
			return fmt.Errorf("--config or XSHCTL_CONFIG required")
		}
		var err error
		c, err = client.Load(*cfg)
		if err != nil {
			return err
		}
		unregister = registerProcess(role)
		return nil
	}
	method, path := "GET", ""
	var input any
	switch args[0] {
	case "mcp":
		if len(args) != 1 {
			return 1, fmt.Errorf("mcp does not accept arguments")
		}
		if e = connect(); e != nil {
			return 1, e
		}
		e = mcpserver.Run(ctx, c)
		if e != nil {
			return 1, e
		}
		return 0, nil
	case "doctor":
		if len(args) != 1 {
			return 1, fmt.Errorf("doctor does not accept arguments")
		}
		path = "/v1/health"
	case "policy":
		if len(args) != 2 || args[1] != "show" {
			return 1, fmt.Errorf("policy show")
		}
		path = "/v1/command-policy"
	case "list":
		path = "/v1/" + filter.kind
		if filter.kind != "audit" {
			if e = connect(); e != nil {
				return 1, e
			}
			var items []map[string]any
			if err := c.Call(ctx, "GET", path, nil, &items); err != nil {
				return 1, err
			}
			printJSON(filter.apply(items))
			return 0, nil
		}
	case "resolve":
		if len(args) < 2 || args[1] != "session" {
			return 1, fmt.Errorf("resolve session --name NAME [--environment ENV] [--fuzzy[=true|false]]")
		}
		fs := flag.NewFlagSet("resolve session", flag.ContinueOnError)
		name := fs.String("name", "", "exact current display name")
		environment := fs.String("environment", "", "optional exact environment")
		fuzzy := fs.Bool("fuzzy", false, "discover substring candidates only when no exact matches; never select or execute")
		if e = fs.Parse(args[2:]); e != nil {
			return 1, e
		}
		if len(fs.Args()) != 0 {
			return 1, fmt.Errorf("unexpected resolve arguments")
		}
		if e = connect(); e != nil {
			return 1, e
		}
		result, err := c.ResolveSessionQuery(ctx, *name, *environment, *fuzzy)
		if err != nil {
			return 1, err
		}
		printJSON(result)
		if result.Status != "matched" {
			return 2, nil
		}
		return 0, nil
	case "get":
		if len(args) != 3 || args[1] != "session" {
			return 1, fmt.Errorf("get session <id>")
		}
		if e = connect(); e != nil {
			return 1, e
		}
		var sessions []bridge.Session
		if e = c.Call(ctx, "GET", "/v1/sessions", nil, &sessions); e != nil {
			return 1, e
		}
		for _, s := range sessions {
			if s.ID == args[2] {
				printJSON(s)
				return 0, nil
			}
		}
		return 1, fmt.Errorf("NOT_FOUND: session")
	case "explain":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			return 1, fmt.Errorf("session ID required")
		}
		fs := flag.NewFlagSet("explain", flag.ContinueOnError)
		gen := fs.String("generation", "", "attachment generation (required)")
		cwd := fs.String("cwd", "", "explicit working directory")
		timeout := fs.Int("timeout-ms", 60000, "command timeout")
		file := fs.String("command-file", "", "UTF-8 shell script")
		// Separate the command explicitly: positional leftovers before -- are errors.
		flags, command := args[2:], []string(nil)
		for i, arg := range flags {
			if arg == "--" {
				command = flags[i+1:]
				flags = flags[:i]
				break
			}
		}
		if e = fs.Parse(flags); e != nil {
			return 1, e
		}
		if *gen == "" || len(fs.Args()) != 0 {
			return 1, fmt.Errorf("--generation required; unexpected arguments rejected")
		}
		cmd := strings.Join(command, " ")
		if *file != "" {
			if len(command) != 0 {
				return 1, fmt.Errorf("command and command-file are mutually exclusive")
			}
			b, err := os.ReadFile(*file)
			if err != nil {
				return 1, err
			}
			cmd = strings.ReplaceAll(strings.TrimPrefix(string(b), "\ufeff"), "\r\n", "\n")
		}
		// The Bridge is authoritative for command, cwd, timeout and target validation.
		input = bridge.ExplainRequest{SessionID: args[1], Generation: *gen, Command: cmd, Cwd: *cwd, TimeoutMS: *timeout}
		method, path = "POST", "/v1/explain"
	case "exec":
		if len(args) < 2 {
			return 1, fmt.Errorf("session ID required")
		}
		fs := flag.NewFlagSet("exec", flag.ContinueOnError)
		gen := fs.String("generation", "", "attachment generation")
		typ := fs.String("type", "", "type assertion")
		rid := fs.String("request-id", "", "idempotency key")
		cwd := fs.String("cwd", "", "explicit working directory")
		timeout := fs.Int("timeout-ms", 60000, "command timeout")
		file := fs.String("command-file", "", "UTF-8 shell script")
		if e = fs.Parse(args[2:]); e != nil {
			return 1, e
		}
		if *typ != "xshell" || *gen == "" {
			return 1, fmt.Errorf("--type xshell and --generation required")
		}
		if e = connect(); e != nil {
			return 1, e
		}
		cmd := strings.Join(fs.Args(), " ")
		if *file != "" {
			if cmd != "" {
				return 1, fmt.Errorf("command and command-file are mutually exclusive")
			}
			b, e := os.ReadFile(*file)
			if e != nil {
				return 1, e
			}
			cmd = strings.ReplaceAll(strings.TrimPrefix(string(b), "\ufeff"), "\r\n", "\n")
		}
		if *rid == "" {
			*rid = bridge.ID()
		}
		fmt.Fprintln(os.Stderr, "request_id="+*rid)
		input = bridge.Submit{SessionID: args[1], Generation: *gen, RequestID: *rid, Command: cmd, Cwd: *cwd, TimeoutMS: *timeout}
		method = "POST"
		path = "/v1/jobs"
	case "job":
		if len(args) < 3 {
			return 1, fmt.Errorf("job get|output|wait|interrupt <id>")
		}
		switch args[1] {
		case "wait":
			fs := flag.NewFlagSet("job wait", flag.ContinueOnError)
			waitMS := fs.Int("wait-ms", 30000, "read-only wait bound in milliseconds (1..30000)")
			if err := fs.Parse(args[3:]); err != nil {
				return 1, err
			}
			if len(fs.Args()) != 0 {
				return 1, fmt.Errorf("unexpected wait arguments")
			}
			if e = connect(); e != nil {
				return 1, e
			}
			result, err := c.WaitJob(ctx, args[2], *waitMS)
			if err != nil {
				return 1, err
			}
			printJSON(result)
			if result.TimedOut {
				return 2, nil
			}
			return 0, nil
		case "get":
			if len(args) != 3 {
				return 1, fmt.Errorf("unexpected arguments")
			}
			path = "/v1/job?id=" + url.QueryEscape(args[2])
		case "output":
			fs := flag.NewFlagSet("output", flag.ContinueOnError)
			cursor := fs.Int("cursor", 0, "byte cursor")
			if e = fs.Parse(args[3:]); e != nil || len(fs.Args()) != 0 {
				return 1, fmt.Errorf("invalid output arguments")
			}
			path = "/v1/output?id=" + url.QueryEscape(args[2]) + "&cursor=" + strconv.Itoa(*cursor)
		case "interrupt":
			if len(args) != 3 {
				return 1, fmt.Errorf("unexpected arguments")
			}
			method = "POST"
			path = "/v1/interrupt"
			input = map[string]string{"id": args[2]}
		default:
			return 1, fmt.Errorf("unknown job operation")
		}
	case "detach":
		if len(args) != 3 {
			return 1, fmt.Errorf("detach <id> <generation>")
		}
		method = "POST"
		path = "/v1/detach"
		input = map[string]string{"id": args[1], "generation": args[2]}
	default:
		return 1, fmt.Errorf("unknown command; run xshctl help")
	}
	if e = connect(); e != nil {
		return 1, e
	}
	var result any
	if e = c.Call(ctx, method, path, input, &result); e != nil {
		return 1, e
	}
	if args[0] == "doctor" {
		if m, ok := result.(map[string]any); ok {
			m["local_process_status"] = processstatus.Snapshot()
		}
	}
	printJSON(result)
	if args[0] == "exec" {
		if m, ok := result.(map[string]any); ok && m["status"] == "pending" {
			return 3, fmt.Errorf("NEEDS_AUTHORIZATION: approve this job in the operator console on the Bridge machine; do not submit a second request")
		}
	}
	return 0, nil
}

func serve(ctx context.Context, dir string) error {
	c, e := local.Load(dir)
	if e != nil {
		return e
	}
	host, _, e := net.SplitHostPort(c.Listen)
	if e != nil || (host != "127.0.0.1" && host != "::1") {
		return fmt.Errorf("bridge must bind numeric loopback; use SSH forwarding")
	}
	lock, e := runlock.Acquire(dir)
	if e != nil {
		return e
	}
	defer lock.Close()
	l, e := net.Listen("tcp", c.Listen)
	if e != nil {
		return e
	}
	defer l.Close()
	s, e := bridge.Open(filepath.Join(dir, "state.json"))
	if e != nil {
		return e
	}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	mcpClient, e := client.New(client.Config{Endpoint: "http://" + l.Addr().String(), Token: c.Keys.Client})
	if e != nil {
		return e
	}
	defer mcpClient.HTTP.CloseIdleConnections()
	srv := &http.Server{Handler: bridge.HandlerWithMCP(s, c.Keys, cancel, mcpserver.HTTPHandler(mcpClient)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-serveCtx.Done()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "graceful bridge shutdown did not finish:", err)
		}
	}()
	defer func() { cancel(); <-shutdownDone }()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-serveCtx.Done():
				return
			case t := <-ticker.C:
				if e := s.Expire(t); e != nil {
					fmt.Fprintln(os.Stderr, "state persistence failed; stopping bridge")
					cancel()
					return
				}
			}
		}
	}()
	fmt.Fprintln(os.Stderr, "Bridge listening on", c.Listen, "(loopback only)")
	e = srv.Serve(l)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
