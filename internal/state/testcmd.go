package state

import (
	"strings"
)

// simpleCommand is one command of a shell line: its source text and words.
type simpleCommand struct {
	text  string
	words []string
}

// splitSimple splits a shell line on unquoted ; && || | and newlines, honouring
// single quotes, double quotes and backslash escapes. It is not a full shell
// parser: $(...) and heredocs are not understood.
func splitSimple(cmd string) []simpleCommand {
	var out []simpleCommand
	var words []string
	var word strings.Builder
	inWord := false
	start := 0
	flushWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	flushCmd := func(end int) {
		flushWord()
		if len(words) > 0 {
			out = append(out, simpleCommand{text: strings.TrimSpace(cmd[start:end]), words: words})
		}
		words = nil
	}
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				word.WriteByte(c)
			}
		case quote == '"':
			switch {
			case c == '"':
				quote = 0
			case c == '\\' && i+1 < len(cmd):
				i++
				word.WriteByte(cmd[i])
			default:
				word.WriteByte(c)
			}
		case c == '\\' && i+1 < len(cmd):
			i++
			word.WriteByte(cmd[i])
			inWord = true
		case c == '\'' || c == '"':
			quote = c
			inWord = true
		case c == ';' || c == '\n' || c == '|' || (c == '&' && i+1 < len(cmd) && cmd[i+1] == '&'):
			flushCmd(i)
			if (c == '|' || c == '&') && i+1 < len(cmd) && cmd[i+1] == c {
				i++
			}
			start = i + 1
		case c == ' ' || c == '\t' || c == '\r':
			flushWord()
		case (c == '(' || c == '{') && !inWord && len(words) == 0:
			start = i + 1 // subshell / group opener before the command
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	flushCmd(len(cmd))
	return out
}

func isEnvAssign(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := w[j]
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || j > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func isDuration(w string) bool {
	return w != "" && w[0] >= '0' && w[0] <= '9'
}

// stripWrappers drops leading VAR=x assignments and env/sudo/time/timeout.
func stripWrappers(w []string) []string {
	for len(w) > 0 {
		switch {
		case isEnvAssign(w[0]):
			w = w[1:]
		case w[0] == "env" || w[0] == "sudo" || w[0] == "time":
			w = w[1:]
			for len(w) > 0 && (strings.HasPrefix(w[0], "-") || isEnvAssign(w[0])) {
				w = w[1:]
			}
		case w[0] == "timeout":
			w = w[1:]
			for len(w) > 0 && strings.HasPrefix(w[0], "-") {
				w = w[1:]
			}
			if len(w) > 0 && isDuration(w[0]) {
				w = w[1:]
			}
		default:
			return w
		}
	}
	return w
}

func isRunner(w []string) bool {
	if len(w) == 0 {
		return false
	}
	at := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	switch w[0] {
	case "go":
		if at(1) == "-C" {
			return at(3) == "test"
		}
		if strings.HasPrefix(at(1), "-C") && len(at(1)) > 2 {
			return at(2) == "test"
		}
		return at(1) == "test"
	case "pytest":
		return true
	case "python", "python3":
		return at(1) == "-m" && at(2) == "pytest"
	case "npm":
		return at(1) == "test" || (at(1) == "run" && at(2) == "test")
	case "npx":
		return at(1) == "playwright" && at(2) == "test" || at(1) == "vitest" || at(1) == "jest"
	case "cargo", "make":
		return at(1) == "test"
	}
	return false
}

// TestCommand returns the simple command of a Bash line that runs a test
// suite, truncated to 120 chars. Runners are matched only at the start of a
// simple command, so a runner name inside a quoted argument never matches.
func TestCommand(cmd string) (string, bool) {
	for _, sc := range splitSimple(cmd) {
		if isRunner(stripWrappers(sc.words)) {
			t := sc.text
			if r := []rune(t); len(r) > 120 {
				t = string(r[:119]) + "…"
			}
			return t, true
		}
	}
	return "", false
}
