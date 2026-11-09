package state

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	summaryRunes = 300  // a one-line summary is cut here
	fullRunes    = 2000 // the hover text of a Bash command
	kvValueRunes = 60
)

func cutLine(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// firstLineMore returns the first non-blank line of s, " …" appended when more follows.
func firstLineMore(s string) string {
	s = strings.TrimSpace(s)
	first, rest, more := strings.Cut(s, "\n")
	first = strings.TrimSpace(first)
	if more && strings.TrimSpace(rest) != "" {
		first += " …"
	}
	return first
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// num reads a number field (decoded as float64) as an int; ok is false when absent.
func (in ToolInput) num(k string) (int, bool) {
	switch v := in[k].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

// scalarKV renders the scalar fields of in as "k=v k=v", keys sorted, skipping
// the named keys and the "_chars" bookkeeping fields.
func scalarKV(in ToolInput, skip ...string) string {
	var keys []string
	for k, v := range in {
		if strings.HasSuffix(k, "_chars") {
			continue
		}
		switch v.(type) {
		case string, float64, bool, int:
		default:
			continue
		}
		skipped := false
		for _, s := range skip {
			skipped = skipped || s == k
		}
		if !skipped {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := fmt.Sprint(in[k])
		if s, ok := in[k].(string); ok {
			if s == "" {
				continue
			}
			v = s
		}
		parts = append(parts, k+"="+cutLine(strings.Join(strings.Fields(v), " "), kvValueRunes))
	}
	return strings.Join(parts, " ")
}

func join(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// ToolSummary is the one-line description of a tool call's parameters, built from
// its stored input. When the input gives nothing it falls back to the event's
// target; an event written before the full input was stored carries its command
// in tool_input, so it reads the same way. Paths are relative to cwd when under it.
func ToolSummary(name string, in ToolInput, target, cwd string) string {
	s := ""
	if len(in) > 0 {
		s = summarize(name, in, cwd)
	}
	if s == "" {
		s = target
	}
	return cutLine(strings.Join(strings.Fields(s), " "), summaryRunes)
}

// ToolFull is the hover text of a call: the whole Bash command, else the summary.
func ToolFull(name string, in ToolInput, target, cwd string) string {
	if name == "Bash" {
		if c := strings.TrimSpace(in.Str("command")); c != "" {
			return cutLine(c, fullRunes)
		}
	}
	return ToolSummary(name, in, target, cwd)
}

func summarize(name string, in ToolInput, cwd string) string {
	path := func(k string) string { return RelPath(in.Str(k), cwd) }
	switch {
	case name == "Bash":
		return firstLineMore(in.Str("command"))
	case name == "Read":
		p := path("file_path")
		off, hasOff := in.num("offset")
		lim, hasLim := in.num("limit")
		switch {
		case hasOff && hasLim:
			return fmt.Sprintf("%s:%d+%d", p, off, lim)
		case hasOff:
			return fmt.Sprintf("%s:%d", p, off)
		case hasLim:
			return fmt.Sprintf("%s:0+%d", p, lim)
		}
		return p
	case name == "Edit":
		return join(path("file_path"), fmt.Sprintf("−%d +%d lines", lineCount(in.Str("old_string")), lineCount(in.Str("new_string"))))
	case name == "MultiEdit":
		a, b := 0, 0
		edits, _ := in["edits"].([]any)
		for _, e := range edits {
			if m, ok := e.(map[string]any); ok {
				a += lineCount(ToolInput(m).Str("old_string"))
				b += lineCount(ToolInput(m).Str("new_string"))
			}
		}
		return join(path("file_path"), fmt.Sprintf("−%d +%d lines", a, b))
	case name == "Write":
		n := lineCount(in.Str("content"))
		return join(path("file_path"), fmt.Sprintf("%d lines", n))
	case name == "NotebookEdit":
		return path("notebook_path")
	case name == "Grep":
		var flags []string
		for _, k := range []string{"path", "glob", "type"} {
			if v := in.Str(k); v != "" {
				if k == "path" {
					v = RelPath(v, cwd)
				}
				flags = append(flags, k+"="+v)
			}
		}
		return join(in.Str("pattern"), strings.Join(flags, " "))
	case name == "Glob":
		if p := path("path"); p != "" {
			return in.Str("pattern") + " in " + p
		}
		return in.Str("pattern")
	case name == "WebFetch":
		return in.Str("url")
	case name == "WebSearch":
		return in.Str("query")
	case name == "Agent" || name == "Task":
		d := firstLineMore(in.Str("description"))
		if t := in.Str("subagent_type"); t != "" {
			return t + ": " + d
		}
		return d
	case strings.HasPrefix(name, "mcp__"):
		rest := strings.TrimPrefix(name, "mcp__")
		server, tool, ok := strings.Cut(rest, "__")
		label := rest
		if ok {
			label = server + "." + tool
		}
		return join(label, scalarKV(in))
	case strings.HasPrefix(name, "Task"):
		id := ""
		if v := in.Str("taskId"); v != "" {
			id = "#" + v
		}
		if s := join(id, firstLineMore(in.Str("subject")), in.Str("status")); s != "" {
			return s
		}
	}
	return scalarKV(in)
}
