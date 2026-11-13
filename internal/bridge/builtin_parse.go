package bridge

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const builtinMaxWords = 128
const builtinMaxSegments = 10
const builtinMaxStages = 8

// Parse only literal shell words and the explicitly supported control operators.
// This is deliberately narrower than a shell; it never executes or expands input.
func builtinParse(command string) ([][][]string, string) {
	if !utf8.ValidString(command) {
		return nil, "builtin_syntax"
	}
	var segments [][][]string
	var pipeline [][]string
	var argv []string
	var word strings.Builder
	started, programQuoted := false, false
	quote := rune(0)
	lastSeparator := rune(0)
	tokens := 0
	flushWord := func() bool {
		if !started {
			return true
		}
		if len(argv) == 0 && programQuoted {
			return false
		}
		argv = append(argv, word.String())
		tokens++
		word.Reset()
		started = false
		programQuoted = false
		return tokens <= builtinMaxWords
	}
	flushStage := func() bool {
		if !flushWord() || len(argv) == 0 {
			return false
		}
		pipeline = append(pipeline, argv)
		argv = nil
		return len(pipeline) <= builtinMaxStages
	}
	flushSegment := func() bool {
		if !flushStage() {
			return false
		}
		segments = append(segments, pipeline)
		pipeline = nil
		return len(segments) <= builtinMaxSegments
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if c == '\r' || c == 0 || (unicode.IsControl(c) && c != '\n' && c != '\t') {
			return nil, "builtin_syntax"
		}
		if quote == '\'' {
			if c == '\n' || c == '\t' {
				return nil, "builtin_syntax"
			}
			if c == '\'' {
				quote = 0
			} else {
				word.WriteRune(c)
			}
			continue
		}
		if quote == '"' {
			switch c {
			case '\n', '\t', '$', '`':
				return nil, "builtin_syntax"
			case '"':
				quote = 0
			case '\\':
				if i+1 >= len(runes) || runes[i+1] == '\n' || runes[i+1] == '\r' {
					return nil, "builtin_syntax"
				}
				n := runes[i+1]
				if n == '$' || n == '`' || n == '"' || n == '\\' {
					word.WriteRune(n)
					i++
				} else {
					word.WriteRune(c)
				}
			default:
				word.WriteRune(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			if len(argv) == 0 {
				programQuoted = true
			}
			started = true
			quote = c
		case '\\':
			if i+1 >= len(runes) || runes[i+1] == '\n' || runes[i+1] == '\r' || unicode.IsControl(runes[i+1]) {
				return nil, "builtin_syntax"
			}
			if len(argv) == 0 {
				programQuoted = true
			}
			started = true
			i++
			word.WriteRune(runes[i])
		case ' ', '\t':
			if !flushWord() {
				return nil, "builtin_syntax"
			}
		case '|':
			if i+1 < len(runes) && runes[i+1] == '|' || !flushStage() {
				return nil, "builtin_syntax"
			}
			lastSeparator = '|'
		case ';', '&', '\n':
			if c == '&' {
				if i+1 >= len(runes) || runes[i+1] != '&' {
					return nil, "builtin_syntax"
				}
				i++
			}
			if c == '\n' && !started && len(argv) == 0 && len(pipeline) == 0 && len(segments) > 0 {
				if lastSeparator != '\n' {
					return nil, "builtin_syntax"
				}
				continue
			}
			if !flushSegment() {
				return nil, "builtin_syntax"
			}
			lastSeparator = c
		case '$', '`', '<', '>', '(', ')', '{', '}', '*', '?', '[', ']', '~', '#', '!':
			return nil, "builtin_syntax"
		default:
			started = true
			word.WriteRune(c)
		}
	}
	if quote != 0 {
		return nil, "builtin_syntax"
	}
	if started || len(argv) > 0 || len(pipeline) > 0 {
		if !flushSegment() {
			return nil, "builtin_syntax"
		}
	} else if len(segments) == 0 || lastSeparator != '\n' {
		return nil, "builtin_syntax"
	}
	return segments, ""
}
