package bridge

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const HardDenyPolicyID = "hard-deny-v1"

type CommandRestriction struct {
	Decision string `json:"decision"`
	ID       string `json:"id"`
	Message  string `json:"message"`
}

func deny(id, message string) *CommandRestriction {
	return &CommandRestriction{Decision: "deny", ID: HardDenyPolicyID + "/" + id, Message: message + "；策略禁止执行，人工审批及临时授权不能覆盖"}
}

func manualInspection(id, message string) *CommandRestriction {
	return &CommandRestriction{Decision: "manual", ID: HardDenyPolicyID + "/" + id, Message: message + "；需要逐次人工审批，不自动复用授权"}
}

func uninspectable() *CommandRestriction {
	return manualInspection("uninspectable", "无法静态确定执行内容")
}

func (d *CommandRestriction) err() error { return fail("COMMAND_DENIED", d.ID+": "+d.Message) }

// This is a static guard, never an interpreter. Walk includes substitutions,
// branches and function bodies, even unreachable ones. No remote/local commands,
// environment lookups or filesystem expansion are performed during inspection.
func checkCommandPolicy(command, cwd string) *CommandRestriction {
	return inspectShell(command, cwd, 0)
}

func inspectShell(command, cwd string, depth int) *CommandRestriction {
	if depth > 8 {
		return uninspectable()
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	var result *CommandRestriction
	if err != nil {
		result = manualInspection("shell-syntax", "Shell 语法无法完整解析")
	}
	syntax.Walk(f, func(node syntax.Node) bool {
		if result != nil && result.Decision == "deny" {
			return false
		}
		stmt, ok := node.(*syntax.Stmt)
		if !ok {
			return true
		}
		for _, r := range stmt.Redirs {
			switch r.Op {
			case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll, syntax.DplOut:
				value, literal := denyLiteral(r.Word, false)
				if !literal {
					if result == nil {
						result = uninspectable()
					}
					continue
				}
				if devicePath(value, cwd) {
					result = deny("device-write", "禁止直接写入设备路径")
					return false
				}
			}
		}
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			args := make([]denyArg, len(call.Args))
			for i, w := range call.Args {
				args[i].value, args[i].literal = denyLiteral(w, false)
			}
			candidate := inspectArgs(args, stmt.Redirs, cwd, depth)
			if candidate != nil && (candidate.Decision == "deny" || result == nil) {
				result = candidate
			}
		}
		return result == nil || result.Decision != "deny"
	})
	return result
}

type denyArg struct {
	value   string
	literal bool
}

// Only quote removal and literal escapes are evaluated. Expansions are rejected
// before expand.Fields/Document, with a fresh empty config (no process or file callbacks).
func denyLiteral(w *syntax.Word, document bool) (string, bool) {
	if w == nil {
		return "", false
	}
	var partsOK func([]syntax.WordPart, bool) bool
	partsOK = func(parts []syntax.WordPart, quoted bool) bool {
		for _, part := range parts {
			switch p := part.(type) {
			case *syntax.Lit:
				if p.Value == "[" || p.Value == "]" {
					continue
				} // POSIX test command / terminator
				if !quoted {
					for i := 0; i < len(p.Value); i++ {
						if p.Value[i] == '\\' {
							i++
							continue
						}
						if strings.ContainsRune("*?[]{}~", rune(p.Value[i])) {
							return false
						}
					}
				}
			case *syntax.SglQuoted:
			case *syntax.DblQuoted:
				if p.Dollar || !partsOK(p.Parts, true) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !partsOK(w.Parts, document) {
		return "", false
	}
	var value string
	var err error
	if document {
		value, err = expand.Document(&expand.Config{}, w)
	} else {
		var fields []string
		fields, err = expand.Fields(&expand.Config{}, w)
		if err == nil && len(fields) == 1 {
			value = fields[0]
		} else {
			return "", false
		}
	}
	return value, err == nil && !strings.ContainsRune(value, 0)
}

func devicePath(value, cwd string) bool {
	if !path.IsAbs(value) {
		value = path.Join(cwd, value)
	}
	value = path.Clean(value)
	switch value {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/fd/1", "/dev/fd/2":
		return false
	}
	return strings.HasPrefix(value, "/dev/")
}

func criticalPath(value, cwd string) bool {
	if !path.IsAbs(value) {
		if !path.IsAbs(cwd) {
			return true
		}
		value = path.Join(cwd, value)
	}
	switch path.Clean(value) {
	case "/", "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/opt", "/root", "/sbin", "/srv", "/usr", "/var", "/var/lib":
		return true
	}
	return false
}

func inspectArgs(args []denyArg, redirs []*syntax.Redirect, cwd string, depth int) *CommandRestriction {
	if len(args) == 0 {
		return uninspectable()
	}
	if !args[0].literal || args[0].value == "" {
		return uninspectable()
	}
	program := path.Base(args[0].value)
	a := args[1:]
	// Recognized wrappers have bounded option handling. Unknown options refuse
	// inspection rather than accidentally treating an option value as the program.
	switch program {
	case "sudo", "env", "command", "exec", "nohup", "xargs", "timeout", "nice":
		i := 0
		for i < len(a) {
			if !a[i].literal {
				return uninspectable()
			}
			v := a[i].value
			if v == "--" {
				i++
				break
			}
			if program == "env" && builtinAssignment.MatchString(v) {
				i++
				continue
			}
			if !strings.HasPrefix(v, "-") || v == "-" {
				break
			}
			if program == "command" && (v == "-v" || v == "-V") {
				return nil
			}
			noValue, withValue := "", ""
			switch program {
			case "sudo":
				noValue = " -n -E -H -S -k -b --non-interactive --preserve-env --set-home "
				withValue = " -u -g -h -p -C -T -D -R --user --group --host --prompt --chdir --chroot "
			case "env":
				noValue = " -i --ignore-environment "
				withValue = " -u --unset -C --chdir "
			case "command":
				noValue = " -p "
			case "exec":
				noValue = " -c -l "
				withValue = " -a "
			case "xargs":
				noValue = " -0 -r --null --no-run-if-empty "
				withValue = " -n -P -I -d --max-args --max-procs --replace --delimiter "
			case "timeout":
				noValue = " --foreground --preserve-status --verbose "
				withValue = " -k -s --kill-after --signal "
			case "nice":
				withValue = " -n --adjustment "
			}
			if strings.Contains(noValue, " "+v+" ") {
				i++
				continue
			}
			if strings.Contains(withValue, " "+v+" ") {
				if i+1 >= len(a) || !a[i+1].literal {
					return uninspectable()
				}
				i += 2
				continue
			}
			if key, _, ok := strings.Cut(v, "="); ok && strings.Contains(withValue, " "+key+" ") {
				i++
				continue
			}
			return uninspectable()
		}
		if program == "timeout" {
			i++
		}
		if i > len(a) {
			return uninspectable()
		}
		return inspectArgs(a[i:], redirs, cwd, depth)
	case "busybox":
		return inspectArgs(a, redirs, cwd, depth)
	case "sh", "bash", "dash", "ash", "ksh", "zsh":
		for i := 0; i < len(a); i++ {
			arg := a[i]
			if !arg.literal {
				return uninspectable()
			}
			if strings.HasPrefix(arg.value, "-") && !strings.HasPrefix(arg.value, "--") && strings.Contains(arg.value, "c") {
				if i+1 >= len(a) || !a[i+1].literal {
					return nil
				} // exact human approval only
				return inspectShell(a[i+1].value, cwd, depth+1)
			}
			if arg.value == "-o" || arg.value == "+o" {
				i++
				if i >= len(a) || !a[i].literal {
					return nil
				}
				continue
			}
			if !strings.HasPrefix(arg.value, "-") {
				return nil
			} // script file: human review
		}
		if body, ok := inlineInput(redirs); ok {
			return inspectShell(body, cwd, depth+1)
		}
		return nil
	case "eval":
		values, ok := literalArgs(a)
		if !ok {
			return uninspectable()
		}
		return inspectShell(strings.Join(values, " "), cwd, depth+1)
	case "python", "python2", "python3":
		if body, ok := interpreterBody(a, redirs, "-c"); ok {
			return inspectScript(body, cwd, depth+1, "python")
		}
		return nil
	case "source", ".", "perl", "ruby", "node", "nodejs", "php", "lua", "luajit", "powershell", "pwsh", "cscript", "wscript":
		return nil // opaque scripts always require a fresh exact human approval
	case "mysql", "mariadb", "psql", "sqlite3", "sqlcmd":
		return inspectSQLClient(program, a, redirs)
	case "redis-cli":
		return inspectRedis(a)
	case "mongo", "mongosh":
		if body, ok := interpreterBody(a, redirs, "--eval"); ok {
			return inspectScript(body, cwd, depth+1, "mongo")
		}
		return nil
	}
	return inspectOperation(program, a, cwd)
}

func interpreterBody(args []denyArg, redirs []*syntax.Redirect, flag string) (string, bool) {
	for i, arg := range args {
		if !arg.literal {
			continue
		}
		if arg.value == flag && i+1 < len(args) {
			return args[i+1].value, args[i+1].literal
		}
		if strings.HasPrefix(arg.value, flag+"=") {
			return strings.TrimPrefix(arg.value, flag+"="), true
		}
	}
	return inlineInput(redirs)
}

func literalArgs(args []denyArg) ([]string, bool) {
	values := make([]string, len(args))
	for i, arg := range args {
		if !arg.literal {
			return nil, false
		}
		values[i] = arg.value
	}
	return values, true
}

// Only one explicit inline stdin is inspectable. File/FD input and pipelines are
// not read by Bridge. AST traversal separately inspects expansions in heredocs.
func inlineInput(redirs []*syntax.Redirect) (string, bool) {
	var body string
	found := false
	for _, r := range redirs {
		if r.N != nil && r.N.Value != "0" {
			continue
		}
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			if found {
				return "", false
			}
			var ok bool
			if r.Op == syntax.WordHdoc {
				body, ok = denyLiteral(r.Word, false)
			} else {
				if r.Hdoc == nil || r.Word == nil {
					return "", false
				}
				quoted := false
				for _, part := range r.Word.Parts {
					if lit, literal := part.(*syntax.Lit); !literal || strings.Contains(lit.Value, "\\") {
						quoted = true
					}
				}
				if quoted {
					var text strings.Builder
					ok = true
					for _, part := range r.Hdoc.Parts {
						lit, literal := part.(*syntax.Lit)
						if !literal {
							ok = false
							break
						}
						text.WriteString(lit.Value)
					}
					body = text.String()
				} else {
					body, ok = denyLiteral(r.Hdoc, true)
				}
			}
			if !ok {
				return "", false
			}
			found = true
		case syntax.RdrIn, syntax.DplIn, syntax.RdrInOut:
			return "", false
		}
	}
	return body, found
}
