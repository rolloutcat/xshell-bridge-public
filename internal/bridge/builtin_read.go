package bridge

import (
	"strconv"
	"strings"
)

const builtinMaxFiles = 32
const builtinMaxCount = 1000000
const builtinMaxReadBytes = 64 * 1024 * 1024

var builtinReadExamples = []struct{ program, example string }{
	{"ls", "ls -lh /var/log"},
	{"cat", "cat -n /var/log/app.log"},
	{"grep", "grep -n -m 100 -E 'ERROR|WARN' /var/log/app.log"},
	{"head", "head -n 100 /var/log/app.log"},
	{"tail", "tail -n 200 /var/log/app.log"},
	{"journalctl", "journalctl --no-pager -n 50 -u nginx.service"},
}

func builtinNumber(s string, signed bool, maximum int) bool {
	if signed && len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	if len(s) == 0 || len(s) > len(strconv.Itoa(maximum)) {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(s)
	return err == nil && n <= maximum
}

func builtinFile(s string, stdin bool) bool { return s != "" && (s != "-" || stdin) }

func builtinOption(options, value string) bool {
	for _, option := range strings.Split(options, "|") {
		if option != "" && option == value {
			return true
		}
	}
	return false
}

// Options are parsed even after operands, matching GNU getopt permutation.
// Unknown options remain manual rather than relying on prefix matches.
func builtinRead(tokens []string, stdin bool) bool {
	program := tokens[0]
	if program == "ls" {
		return !stdin && builtinList(tokens)
	}
	if program == "journalctl" {
		return builtinJournal(tokens, stdin)
	}
	if program != "cat" && program != "grep" && program != "head" && program != "tail" {
		return false
	}
	end, recursive, pattern := false, false, false
	files := 0
	catLong := "|--number|--number-nonblank|--squeeze-blank|--show-all|--show-ends|--show-tabs|--show-nonprinting|"
	grepLong := "|--line-number|--ignore-case|--invert-match|--extended-regexp|--fixed-strings|--word-regexp|--line-regexp|--count|--files-with-matches|--files-without-match|--only-matching|--quiet|--silent|--no-filename|--with-filename|--recursive|--dereference-recursive|"
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		if !end && t == "--" {
			end = true
			continue
		}
		if !end && strings.HasPrefix(t, "--") {
			if program == "cat" && builtinOption(catLong, t) {
				continue
			}
			if program == "grep" && builtinOption(grepLong, t) {
				if t == "--recursive" || t == "--dereference-recursive" {
					recursive = true
				}
				continue
			}
			name, value, hasValue := strings.Cut(t, "=")
			kind := ""
			maximum := builtinMaxCount
			if program == "grep" {
				switch name {
				case "--regexp":
					kind = "pattern"
				case "--max-count", "--after-context", "--before-context", "--context":
					kind = "number"
				}
			} else if program == "head" || program == "tail" {
				if name == "--lines" || name == "--bytes" {
					kind = "signed"
					if name == "--bytes" {
						maximum = builtinMaxReadBytes
					}
				}
			}
			if kind == "" {
				return false
			}
			if !hasValue {
				i++
				if i >= len(tokens) {
					return false
				}
				value = tokens[i]
			}
			if kind == "pattern" {
				pattern = true
			} else if !builtinNumber(value, kind == "signed", maximum) {
				return false
			}
			continue
		}
		if !end && len(t) > 1 && t[0] == '-' {
			for j := 1; j < len(t); j++ {
				c := t[j]
				if program == "cat" && strings.ContainsRune("nbsAETv", rune(c)) {
					continue
				}
				if program == "grep" && strings.ContainsRune("nivEFwxclLoqhHrR", rune(c)) {
					if c == 'r' || c == 'R' {
						recursive = true
					}
					continue
				}
				kind := ""
				maximum := builtinMaxCount
				if program == "grep" {
					if c == 'e' {
						kind = "pattern"
					} else if strings.ContainsRune("mABC", rune(c)) {
						kind = "number"
					}
				}
				if (program == "head" || program == "tail") && (c == 'n' || c == 'c') {
					kind = "signed"
					if c == 'c' {
						maximum = builtinMaxReadBytes
					}
				}
				if kind == "" {
					return false
				}
				value := t[j+1:]
				if value == "" {
					i++
					if i >= len(tokens) {
						return false
					}
					value = tokens[i]
				}
				if kind == "pattern" {
					pattern = true
				} else if !builtinNumber(value, kind == "signed", maximum) {
					return false
				}
				break
			}
			continue
		}
		if program == "grep" && !pattern {
			pattern = true
			continue
		}
		if !builtinFile(t, stdin) {
			return false
		}
		files++
		if files > builtinMaxFiles {
			return false
		}
	}
	if program == "grep" {
		return pattern && (files > 0 || stdin || recursive)
	}
	return files > 0 || (stdin && program != "cat")
}

func builtinJournal(tokens []string, stdin bool) bool {
	if stdin {
		return false
	}
	noPager, lines, unit := false, false, false
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		if t == "--no-pager" {
			if noPager {
				return false
			}
			noPager = true
			continue
		}
		if t == "-b" || t == "--boot" {
			continue
		}
		name, value, hasValue := strings.Cut(t, "=")
		if strings.HasPrefix(t, "-n") && !strings.HasPrefix(t, "--") && len(t) > 2 {
			name, value, hasValue = "-n", t[2:], true
		}
		if strings.HasPrefix(t, "-u") && !strings.HasPrefix(t, "--") && len(t) > 2 {
			name, value, hasValue = "-u", t[2:], true
		}
		switch name {
		case "-n", "--lines", "-u", "--unit", "--since", "--until", "--output", "-o", "--boot":
		default:
			return false
		}
		if !hasValue {
			i++
			if i >= len(tokens) {
				return false
			}
			value = tokens[i]
		}
		switch name {
		case "-n", "--lines":
			n, _ := strconv.Atoi(value)
			if lines || !builtinNumber(value, false, builtinMaxCount) || n == 0 {
				return false
			}
			lines = true
		case "-u", "--unit":
			if unit || !builtinUnit.MatchString(value) {
				return false
			}
			unit = true
		case "--since", "--until":
			if value == "" || strings.HasPrefix(value, "-") || len(value) > 128 {
				return false
			}
		case "--output", "-o":
			if !builtinOption("short|short-iso|short-precise|short-unix|cat|json|json-pretty", value) {
				return false
			}
		case "--boot":
			if !builtinNumber(value, true, builtinMaxCount) {
				return false
			}
		}
	}
	return noPager && lines
}

func builtinList(tokens []string) bool {
	end, files := false, 0
	for _, token := range tokens[1:] {
		if !end && token == "--" {
			end = true
			continue
		}
		if !end && strings.HasPrefix(token, "--") {
			if !builtinOption("--all|--almost-all|--directory|--human-readable|--numeric-uid-gid|--reverse|--size|--inode|--classify|--color=never", token) {
				return false
			}
			continue
		}
		if !end && strings.HasPrefix(token, "-") && len(token) > 1 {
			for _, flag := range token[1:] {
				if !strings.ContainsRune("lahAdtrS1Fpisn", flag) {
					return false
				}
			}
			continue
		}
		if token == "" {
			return false
		}
		files++
		if files > builtinMaxFiles {
			return false
		}
	}
	return true
}
