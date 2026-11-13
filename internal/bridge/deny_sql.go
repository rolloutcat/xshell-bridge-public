package bridge

import (
	"strings"
	"unicode"

	"mvdan.cc/sh/v3/syntax"
)

func inspectSQLClient(program string, args []denyArg, redirs []*syntax.Redirect) *CommandRestriction {
	a, ok := literalArgs(args)
	if !ok {
		return uninspectable()
	}
	var queries []string
	positional := []string{}
	for i := 0; i < len(a); i++ {
		v := a[i]
		queryFlags := " -e --execute "
		switch program {
		case "psql":
			queryFlags = " -c --command "
		case "sqlcmd":
			queryFlags = " -Q -q "
		case "sqlite3":
			queryFlags = " -cmd "
		}
		if strings.Contains(queryFlags, " "+v+" ") {
			if i+1 >= len(a) {
				return uninspectable()
			}
			i++
			queries = append(queries, a[i])
			continue
		}
		if key, value, eq := strings.Cut(v, "="); eq && strings.Contains(queryFlags, " "+key+" ") {
			queries = append(queries, value)
			continue
		}
		if len(v) > 2 && strings.Contains(queryFlags, " "+v[:2]+" ") && v[0] == '-' && v[1] != '-' {
			queries = append(queries, v[2:])
			continue
		}
		if contains([]string{"-f", "--file", "-i", "--init-command", "--init-command-add", "--defaults-file", "--defaults-extra-file"}, strings.SplitN(v, "=", 2)[0]) {
			return uninspectable()
		}
		if strings.HasPrefix(v, "-") {
			// Connection/format options do not carry SQL. Unknown options cannot
			// hide an alternate executable-input switch (including abbreviations).
			flags := " -N -B -s -t -A -X -q --batch --raw --skip-column-names --no-align --tuples-only --no-psqlrc -csv -json -readonly -batch -noheader -header "
			values := " -h -P -p -u -D -d -U -S --host --port --user --username --password --database --dbname --socket --ssl-mode -v --set -separator -nullvalue "
			if strings.Contains(flags, " "+v+" ") {
				continue
			}
			if strings.Contains(values, " "+v+" ") && i+1 < len(a) {
				i++
				continue
			}
			if key, _, eq := strings.Cut(v, "="); eq && strings.Contains(values, " "+key+" ") {
				continue
			}
			if len(v) > 2 && strings.Contains(values, " "+v[:2]+" ") && v[1] != '-' {
				continue
			}
			return uninspectable()
		}
		positional = append(positional, v)
	}
	if program == "sqlite3" && len(positional) > 1 {
		queries = append(queries, positional[1:]...)
	}
	if body, ok := inlineInput(redirs); ok {
		queries = append(queries, body)
	}
	// File/FD input is not available to the local inspector.
	var review *CommandRestriction
	for _, r := range redirs {
		if r.Op == syntax.RdrIn || r.Op == syntax.DplIn {
			review = uninspectable()
		}
	}
	if len(queries) == 0 {
		return uninspectable()
	}
	for _, query := range queries {
		if d := inspectSQL(query); d != nil {
			if d.Decision == "deny" {
				return d
			}
			review = d
		}
	}
	return review
}

type codeToken struct {
	text   string
	quoted bool
}

// SQL lexical boundaries keep keywords in comments, strings and identifiers
// from becoming operations. This is a conservative classifier, not a dialect
// validator. Executable comments and client meta-commands are not accepted.
func sqlTokens(source string) ([]codeToken, bool) {
	var out []codeToken
	for i := 0; i < len(source); {
		c := source[i]
		if c <= ' ' {
			i++
			continue
		}
		if c == '#' || strings.HasPrefix(source[i:], "--") {
			for i < len(source) && source[i] != '\n' {
				i++
			}
			continue
		}
		if strings.HasPrefix(source[i:], "/*") {
			if strings.HasPrefix(source[i:], "/*!") || strings.HasPrefix(source[i:], "/*M!") {
				return nil, false
			}
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += end + 4
			continue
		}
		if c == '\\' {
			return nil, false
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			close := c
			if c == '[' {
				close = ']'
			}
			i++
			closed := false
			for i < len(source) {
				if source[i] == '\\' {
					i += 2
					continue
				}
				if source[i] == close {
					i++
					if i < len(source) && source[i] == close {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
			out = append(out, codeToken{"quoted", true})
			continue
		}
		if c == '$' {
			end := i + 1
			for end < len(source) && (source[end] == '_' || source[end] >= 'a' && source[end] <= 'z' || source[end] >= 'A' && source[end] <= 'Z' || source[end] >= '0' && source[end] <= '9') {
				end++
			}
			if end < len(source) && source[end] == '$' {
				delim := source[i : end+1]
				tail := strings.Index(source[end+1:], delim)
				if tail < 0 {
					return nil, false
				}
				i = end + 1 + tail + len(delim)
				out = append(out, codeToken{"quoted", true})
				continue
			}
		}
		if unicode.IsLetter(rune(c)) || c == '_' || c >= '0' && c <= '9' {
			start := i
			i++
			for i < len(source) && (unicode.IsLetter(rune(source[i])) || source[i] == '_' || source[i] >= '0' && source[i] <= '9') {
				i++
			}
			out = append(out, codeToken{strings.ToUpper(source[start:i]), false})
			continue
		}
		out = append(out, codeToken{string(c), false})
		i++
	}
	return out, true
}

func inspectSQL(source string) *CommandRestriction {
	tokens, ok := sqlTokens(source)
	if !ok {
		return manualInspection("sql-uninspectable", "SQL 含无法检查的语法、可执行注释或客户端脚本指令")
	}
	var review *CommandRestriction
	for start := 0; start < len(tokens); {
		end := start
		for end < len(tokens) && tokens[end].text != ";" {
			end++
		}
		stmt := tokens[start:end]
		start = end + 1
		if len(stmt) == 0 {
			continue
		}
		depth := 0
		for i, token := range stmt {
			if token.quoted {
				continue
			}
			switch token.text {
			case "(":
				depth++
				continue
			case ")":
				depth--
				if depth < 0 {
					return uninspectable()
				}
				continue
			}
			statementStart := i == 0 || contains([]string{"(", ")", "EXPLAIN", "ANALYZE"}, stmt[i-1].text)
			if contains([]string{"DROP", "TRUNCATE", "GRANT", "REVOKE", "PREPARE", "EXECUTE", "EXEC", "CALL", "COPY", "OUTFILE", "DUMPFILE", "SHUTDOWN", "LOAD", "INSTALL", "UNINSTALL", "SOURCE", "ATTACH", "DETACH", "VACUUM"}, token.text) || token.text == "DO" && i == 0 {
				return deny("sql-destructive", "禁止 SQL 删除结构、清空、权限变更或间接执行")
			}
			if (token.text == "CREATE" || token.text == "ALTER") && i+1 < len(stmt) && contains([]string{"USER", "ROLE", "SYSTEM"}, stmt[i+1].text) {
				return deny("sql-administration", "禁止数据库用户、角色或实例级配置变更")
			}
			if token.text == "SET" && i+1 < len(stmt) && contains([]string{"GLOBAL", "PERSIST"}, stmt[i+1].text) {
				return deny("sql-administration", "禁止数据库全局配置变更")
			}
			if statementStart && (token.text == "UPDATE" || token.text == "DELETE") {
				level, where := depth, false
				for _, later := range stmt[i+1:] {
					if later.quoted {
						continue
					}
					if later.text == "(" {
						level++
					}
					if later.text == ")" {
						level--
						if level < depth {
							break
						}
					}
					if later.text == "WHERE" && level == depth {
						where = true
						break
					}
				}
				if !where {
					return deny("sql-unbounded-write", "禁止没有本层 WHERE 条件的 UPDATE 或 DELETE")
				}
			}
		}
		if depth != 0 {
			return uninspectable()
		}
		if stmt[0].quoted || !contains([]string{"SELECT", "SHOW", "EXPLAIN", "DESCRIBE", "DESC", "INSERT", "REPLACE", "UPDATE", "DELETE", "WITH", "BEGIN", "COMMIT", "ROLLBACK", "SET", "USE", "CREATE", "ALTER"}, stmt[0].text) {
			review = uninspectable()
		}
	}
	return review
}
