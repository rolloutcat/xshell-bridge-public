package bridge

import (
	"path"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Scripts are never automatically authorized, even after a clean scan. This
// scanner recognizes explicit destructive calls and literal shell/SQL payloads;
// it is not a Python/JavaScript runtime, alias analysis or a proof of safety.
func inspectScript(source, cwd string, depth int, language string) *CommandRestriction {
	if depth > 8 {
		return uninspectable()
	}
	tokens, ok := scriptTokens(source, language)
	if !ok {
		return nil
	} // the full original script remains subject to human review
	var review *CommandRestriction
	for i, token := range tokens {
		if token.quoted || i+1 >= len(tokens) || tokens[i+1].text != "(" {
			continue
		}
		name := token.text
		qualified := name
		if i >= 2 && tokens[i-1].text == "." {
			qualified = tokens[i-2].text + "." + name
		}
		if language == "python" && (contains([]string{"rmtree", "removedirs", "unlink", "rmdir", "truncate"}, name) || qualified == "os.remove") {
			return deny("python-destructive", "禁止 Python 递归删除、移除文件或截断文件的显式调用")
		}
		if language == "mongo" && contains([]string{"dropDatabase", "dropCollection", "drop", "deleteMany", "remove", "shutdownServer", "createUser", "dropUser", "grantRolesToUser", "revokeRolesFromUser"}, name) {
			return deny("mongo-destructive", "禁止 MongoDB 删除、批量清理或权限管理操作")
		}
		if i+2 >= len(tokens) {
			continue
		}
		arg := tokens[i+2]
		if language == "redis" && (qualified == "redis.call" || qualified == "redis.pcall") && arg.quoted {
			var command []denyArg
			for _, value := range tokens[i+2:] {
				if !value.quoted && value.text == ")" {
					break
				}
				if value.quoted {
					command = append(command, denyArg{value.text, true})
				}
			}
			if len(command) > 0 {
				if d := inspectRedis(command); d != nil && d.Decision == "deny" {
					return d
				}
			}
		}
		if qualified == name && contains([]string{"exec", "eval"}, name) {
			if !arg.quoted {
				review = manualInspection("dynamic-code", "动态生成或解码后执行代码，无法确定实际操作")
				continue
			}
			if d := inspectScript(arg.text, cwd, depth+1, language); d != nil {
				if d.Decision == "deny" {
					return d
				}
				review = d
			}
		}
		if contains([]string{"execute", "executemany", "executescript"}, name) && arg.quoted {
			if d := inspectSQL(arg.text); d != nil {
				if d.Decision == "deny" {
					return d
				}
				review = d
			}
		}
		if contains([]string{"system", "popen", "run", "call", "check_call", "check_output", "Popen", "getoutput", "getstatusoutput"}, name) {
			if arg.quoted {
				if d := inspectShell(arg.text, cwd, depth+1); d != nil {
					if d.Decision == "deny" {
						return d
					}
					review = d
				}
			} else if arg.text == "[" || arg.text == "(" {
				var argv []denyArg
				complete := false
				for _, value := range tokens[i+3:] {
					if !value.quoted && (value.text == "]" || value.text == ")") {
						complete = true
						break
					}
					if !value.quoted && value.text == "," {
						continue
					}
					if !value.quoted {
						break
					}
					argv = append(argv, denyArg{value.text, true})
				}
				if complete && len(argv) > 0 {
					if d := inspectArgs(argv, nil, cwd, depth+1); d != nil {
						if d.Decision == "deny" {
							return d
						}
						review = d
					}
				}
			}
		}
	}
	return review
}

func scriptTokens(source, language string) ([]codeToken, bool) {
	var tokens []codeToken
	for i := 0; i < len(source); {
		c := source[i]
		if c <= ' ' {
			i++
			continue
		}
		if language == "python" && c == '#' || language == "mongo" && strings.HasPrefix(source[i:], "//") {
			for i < len(source) && source[i] != '\n' {
				i++
			}
			continue
		}
		if language == "mongo" && strings.HasPrefix(source[i:], "/*") {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += end + 4
			continue
		}
		if c == '\'' || c == '"' || language == "mongo" && c == '`' {
			delim := string(c)
			if language == "python" && strings.HasPrefix(source[i:], strings.Repeat(delim, 3)) {
				delim = strings.Repeat(delim, 3)
			}
			i += len(delim)
			var value strings.Builder
			closed := false
			for i < len(source) {
				if strings.HasPrefix(source[i:], delim) {
					i += len(delim)
					closed = true
					break
				}
				if source[i] == '\\' && i+1 < len(source) {
					i++
					e := source[i]
					i++
					switch e {
					case '\\', '\'', '"':
						value.WriteByte(e)
					case 'n':
						value.WriteByte('\n')
					case 'r':
						value.WriteByte('\r')
					case 't':
						value.WriteByte('\t')
					case '\n':
					case 'x', 'u', 'U':
						n := 2
						if e == 'u' {
							n = 4
						}
						if e == 'U' {
							n = 8
						}
						if i+n > len(source) {
							return nil, false
						}
						v, err := strconv.ParseUint(source[i:i+n], 16, 32)
						if err != nil {
							return nil, false
						}
						value.WriteRune(rune(v))
						i += n
					default:
						value.WriteByte('\\')
						value.WriteByte(e)
					}
					continue
				}
				value.WriteByte(source[i])
				i++
			}
			if !closed {
				return nil, false
			}
			tokens = append(tokens, codeToken{value.String(), true})
			continue
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			start := i
			i++
			for i < len(source) && (source[i] == '_' || source[i] >= 'a' && source[i] <= 'z' || source[i] >= 'A' && source[i] <= 'Z' || source[i] >= '0' && source[i] <= '9') {
				i++
			}
			tokens = append(tokens, codeToken{source[start:i], false})
			continue
		}
		tokens = append(tokens, codeToken{string(c), false})
		i++
	}
	return tokens, true
}

// Script and dynamic execution always needs a new exact human decision.
// Plain compound commands retain existing exact-grant behavior.
func needsExactApproval(command string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return true
	}
	exact := false
	syntax.Walk(f, func(node syntax.Node) bool {
		if exact {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		for _, word := range call.Args {
			if _, ok := denyLiteral(word, false); !ok {
				exact = true
				return false
			}
		}
		program, _ := denyLiteral(call.Args[0], false)
		program = path.Base(program)
		if contains([]string{"sh", "bash", "dash", "ash", "ksh", "zsh", "python", "python2", "python3", "perl", "ruby", "node", "nodejs", "php", "lua", "luajit", "powershell", "pwsh", "cscript", "wscript", "mongo", "mongosh", "eval", "source", ".", "sudo", "env", "command", "exec", "nohup", "timeout", "nice", "busybox", "xargs", "find"}, program) {
			exact = true
		}
		for _, suffix := range []string{".sh", ".bash", ".py", ".pl", ".rb", ".js", ".ps1", ".bat", ".cmd"} {
			if strings.HasSuffix(strings.ToLower(program), suffix) {
				exact = true
			}
		}
		return !exact
	})
	return exact
}
