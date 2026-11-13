package bridge

import (
	"strconv"
	"strings"
)

const builtinMaxTimeoutSeconds = 60
const builtinMaxKillAfterSeconds = 10

// Inspect one literal timeout wrapper without rewriting the submitted command.
// The caller must still match the complete child argv and every pipeline stage.
func builtinTimeout(tokens []string, taskTimeoutMS int) ([]string, bool) {
	i, killSeconds := 1, 0
	if i < len(tokens) && (tokens[i] == "-k" || tokens[i] == "--kill-after" || strings.HasPrefix(tokens[i], "--kill-after=")) {
		_, value, inline := strings.Cut(tokens[i], "=")
		i++
		if !inline {
			if i >= len(tokens) {
				return nil, false
			}
			value = tokens[i]
			i++
		}
		var ok bool
		killSeconds, ok = builtinSeconds(value, builtinMaxKillAfterSeconds)
		if !ok {
			return nil, false
		}
	}
	if i < len(tokens) && tokens[i] == "--" {
		i++
	}
	if i+1 >= len(tokens) {
		return nil, false
	}
	seconds, ok := builtinSeconds(tokens[i], builtinMaxTimeoutSeconds)
	if !ok || (seconds+killSeconds)*1000 > taskTimeoutMS {
		return nil, false
	}
	return tokens[i+1:], true
}

func builtinSeconds(value string, maximum int) (int, bool) {
	value = strings.TrimSuffix(value, "s")
	if !builtinNumber(value, false, maximum) {
		return 0, false
	}
	seconds, _ := strconv.Atoi(value)
	return seconds, seconds > 0
}
