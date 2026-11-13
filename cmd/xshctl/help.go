package main

import (
	"fmt"
	"strings"
)

var commandHelp = map[string]string{
	"policy": `xshctl [--config FILE] policy show
Read current hard_deny policy and default read-only command catalogue/switch. The read-only switch cannot disable hard deny. V5 adds bounded ls, head/tail byte counts up to 67108864 (64 MiB), and one timeout wrapper around a fully read-only child. Line/context counts stay at 1000000. timeout accepts integer seconds (optional s), duration 1..60 and optional -k/--kill-after 1..10; their sum must fit the task timeout. It does not execute or authorize, and does not guarantee later target or policy state. Exit 0 on success, 1 on error.`,
	"policy show": `xshctl [--config FILE] policy show
Read current hard_deny policy and default read-only command catalogue/switch. The read-only switch cannot disable hard deny. V5 adds bounded ls, head/tail byte counts up to 67108864 (64 MiB), and one timeout wrapper around a fully read-only child. Line/context counts stay at 1000000. timeout accepts integer seconds (optional s), duration 1..60 and optional -k/--kill-after 1..10; their sum must fit the task timeout. It does not execute or authorize, and does not guarantee later target or policy state. Exit 0 on success, 1 on error.`,
	"list": `xshctl list sessions|jobs|audit [filters]
Read JSON arrays. See xshctl help list sessions or xshctl help list jobs. Exit 0 on success, 1 on error.`,
	"list sessions": `xshctl [--config FILE] list sessions [--name NAME] [--environment ENV] [--state STATE]
Exact case-sensitive AND filters, applied locally to the complete Bridge response. States: pending, active, expired, detached, blocked. Empty result: [].
Example: xshctl --config client.json list sessions --name web --environment prod --state active
Verify ID, generation and fingerprint before submitting. Exit 0 on success, 1 on error.`,
	"list jobs": `xshctl [--config FILE] list jobs [--session-id ID] [--request-id ID] [--status STATUS]
Exact case-sensitive AND filters, applied locally to the complete Bridge response. Statuses: pending, queued, dispatched, running, completed, denied, cancelled, unknown. Empty result: [].
Example: xshctl --config client.json list jobs --session-id SESSION --request-id REQUEST
After a lost submission response, check session ID, generation, request ID, command, cwd and timeout before tracking the original job. Never replay unknown work. Exit 0 on success, 1 on error.`,
	"list audit": `xshctl [--config FILE] list audit
Read the audit JSON array. Exit 0 on success, 1 on error.`,
	"exec": `xshctl [--config FILE] exec SESSION --generation GENERATION --type xshell [--request-id ID] [--cwd DIR] [--timeout-ms MS] [--command-file FILE] -- COMMAND
Example: xshctl --config client.json exec SESSION --generation GEN --type xshell --request-id RID --cwd / --timeout-ms 15000 -- hostname
Read the current default command catalogue with policy show when useful; it does not execute or authorize and cannot guarantee later target state. CLI joins command arguments with spaces; --command-file is mutually exclusive with command arguments. Timeout defaults to 60000 ms; cwd must be explicit for the builtin policy. For systemctl and journalctl reads use --no-pager and bound log output; never invoke more or less interactively. If request ID is omitted, one is generated and printed to stderr.
Exit 0 on success, 3 for an already registered pending job, 1 on error. COMMAND_DENIED means a definite destructive operation was refused: no job is created, and approval cannot override it. Uninspectable commands and scripts remain pending with requires_exact_approval=true; approve once, never remember or reuse a grant. Save the original job ID and follow it with job wait/output. If the response is lost, retain request ID and use list jobs for read-only lookup; verify the full original submission. Never replay unknown work.`,
	"explain": `xshctl [--config FILE] explain SESSION --generation GENERATION [--cwd DIR] [--timeout-ms MS] [--command-file FILE] -- COMMAND
Example: xshctl --config client.json explain SESSION --generation GEN --cwd / -- hostname
Read-only explanation of a new submission, not approval. blocked/command_denied means a definite dangerous operation; manual/exact_approval_required means fresh human review is needed, not a denial. Timeout defaults to 60000 ms. File and command are mutually exclusive. Exit 0 for a valid explanation, 1 on error.`,
	"resolve": `xshctl resolve session --name NAME [--environment ENV] [--fuzzy[=true|false]]
See xshctl help resolve session.`,
	"resolve session": `xshctl [--config FILE] resolve session --name NAME [--environment ENV] [--fuzzy[=true|false]]
Example: xshctl --config client.json resolve session --name web --environment prod
Exact case-sensitive matching; fuzzy suggestions require explicit selection. Verify ID, generation and fingerprint. Exit 0 matched, 2 otherwise, 1 on error.`,
	"get": `xshctl [--config FILE] get session ID
Read one session by exact ID. Exit 0 on success, 1 on error.`,
	"get session": `xshctl [--config FILE] get session ID
Read one session by exact ID. Exit 0 on success, 1 on error.`,
	"job": `xshctl job get|wait|output|interrupt ID
See xshctl help job wait or xshctl help job output. Always track the original job.`,
	"job get": `xshctl [--config FILE] job get JOB
Read the original job snapshot. Exit 0 on success, 1 on error.`,
	"job wait": `xshctl [--config FILE] job wait JOB [--wait-ms MS]
Example: xshctl --config client.json job wait JOB --wait-ms 30000
Read-only wait, 1..30000 ms (default 30000). Exit 0 on terminal status (including unknown), 2 on observation timeout, 1 on error. Continue bounded waits on the same job; unknown must not be replayed. Read output separately.`,
	"job output": `xshctl [--config FILE] job output JOB [--cursor BYTE_OFFSET]
Example: xshctl --config client.json job output JOB --cursor 0
Read output from the original job, continuing with next_cursor until it stops advancing. Exit 0 on success, 1 on error.`,
	"job interrupt": `xshctl [--config FILE] job interrupt JOB
Request Ctrl+C for the original job; this does not guarantee it has stopped. Exit 0 on success, 1 on error.`,
	"detach": `xshctl [--config FILE] detach SESSION GENERATION
Revoke attachment; a remote command may still run. Exit 0 on success, 1 on error.`,
	"doctor": `xshctl [--config FILE] doctor
Read Bridge health and local process status. Exit 0 on success, 1 on error.`,
	"processes": `xshctl processes
Read local process status without Bridge configuration. Exit 0 on success, 1 on error.`,
	"version": `xshctl version
Print version without Bridge configuration. Exit 0 on success.`,
	"mcp": `xshctl [--config FILE] mcp
Run the MCP stdio server using the same Bridge authorization.`,
	"init": `xshctl init --dir PRIVATE_RUNTIME_DIRECTORY
Initialize a private Bridge runtime directory. Exit 0 on success, 1 on error.`,
	"serve": `xshctl serve --dir PRIVATE_RUNTIME_DIRECTORY
Run the private Bridge. Exit 0 on success, 1 on error.`,
	"console": `xshctl console --dir PRIVATE_RUNTIME_DIRECTORY
Run the local operator console. Exit 0 on success, 1 on error.`,
}

// commandHelpRequest only recognizes an explicit help topic or a bare help flag
// directly after a command (or its required ID). Never inspect a remote command.
func commandHelpRequest(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	var topic string
	if args[0] == "help" {
		if len(args) == 1 {
			fmt.Print(usage)
			return true, nil
		}
		topic = strings.Join(args[1:], " ")
	} else {
		if args[len(args)-1] != "--help" && args[len(args)-1] != "-h" {
			return false, nil
		}
		before := args[:len(args)-1]
		for _, a := range before {
			// A preceding option may consume --help as its value; for exec,
			// positional leftovers can also be the remote command.
			if strings.HasPrefix(a, "-") {
				return false, nil
			}
		}
		topic = strings.Join(before, " ")
		if _, ok := commandHelp[topic]; !ok && len(before) > 1 && !strings.HasPrefix(before[len(before)-1], "-") {
			parent := strings.Join(before[:len(before)-1], " ")
			if parent == "exec" || parent == "explain" || parent == "job get" || parent == "job wait" || parent == "job output" || parent == "job interrupt" || parent == "get session" {
				topic = parent
			}
		}
	}
	if help, ok := commandHelp[topic]; ok {
		fmt.Println(help)
		return true, nil
	}
	return true, fmt.Errorf("unknown help topic: %s", topic)
}
