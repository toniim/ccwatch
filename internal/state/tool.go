package state

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Tool is one tool call joined from its PreToolUse and PostToolUse(Failure)
// events by tool_use_id. Only a summary is kept: the tool name and its target.
type Tool struct {
	ID     string
	Actor  string // agent_id of the subagent that ran it, "" for the main session
	Name   string
	Target string
	Start  time.Time
	DurMs  int64 // 0 = unknown
	Done   bool
	Failed bool

	Input   ToolInput // stored input while the call is open, nil once finished
	Summary string    // one-line parameters, see ToolSummary
	Full    string    // hover text: the whole Bash command, else Summary
	Key     string    // handle of /api/tool, see ToolKey
}

// ToolKey is the row key of the tool call whose first stored event is events[i].
func ToolKey(i int) string { return "t" + strconv.Itoa(i) }

// describe fills Summary and Full from the input, the target and the session cwd.
func (t *Tool) describe(cwd string) {
	t.Summary = ToolSummary(t.Name, t.Input, t.Target, cwd)
	t.Full = ToolFull(t.Name, t.Input, t.Target, cwd)
}

// DisplayName is the tool name shown in a row: an MCP tool reads as "mcp".
func (t *Tool) DisplayName() string { return displayName(t.Name) }

func displayName(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	return name
}

// Label is "Read internal/x.go": the name and the one-line parameters.
func (t *Tool) Label() string { return ToolLabel(t.Name, t.Summary) }

// RecentTools is how many completed tool calls Session.Recent keeps.
const RecentTools = 20

// maxGroupItems bounds the child rows carried by one grouped log row.
const maxGroupItems = 30

// ToolLabel renders a tool call as "Read internal/x.go". An MCP tool is shown as
// "mcp server/tool" (its target already is that pair).
func ToolLabel(name, target string) string {
	name = displayName(name)
	if target == "" {
		return name
	}
	return name + " " + target
}

// FmtDur formats a duration given in milliseconds: 420ms, 3.2s, 12s, 1m05s.
func FmtDur(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 10_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	case ms < 60_000:
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%dm%02ds", ms/60_000, ms%60_000/1000)
}

// toolText is the log text of a call: the label, plus ✓/✗ and the duration once finished.
func toolText(t *Tool) string {
	s := t.Label()
	if !t.Done {
		return s
	}
	mark := " ✓"
	if t.Failed {
		mark = " ✗"
	}
	if t.DurMs > 0 {
		return s + mark + " " + FmtDur(t.DurMs)
	}
	return s + mark
}

func toolTag(t *Tool) string {
	switch {
	case !t.Done:
		return "run"
	case t.Failed:
		return "fail"
	}
	return "ok"
}

// ToolEntry builds the log row of a tool call.
func toolEntry(t *Tool) LogEntry {
	return LogEntry{TS: t.Start, Kind: "tool", Actor: t.Actor, Text: toolText(t), Tag: toolTag(t), DurMs: t.DurMs, Key: t.Key, Tool: t.DisplayName(), Summary: t.Summary}
}

// GroupTools collapses runs of 2+ consecutive tool rows of the same actor into
// one "tools" row ("N tool calls") that carries the last maxGroupItems of them
// as Items. Single tool rows and every other row pass through unchanged.
func GroupTools(rows []LogEntry) []LogEntry {
	out := make([]LogEntry, 0, len(rows))
	for i := 0; i < len(rows); {
		if rows[i].Kind != "tool" {
			out = append(out, rows[i])
			i++
			continue
		}
		j := i
		for j < len(rows) && rows[j].Kind == "tool" && rows[j].Actor == rows[i].Actor {
			j++
		}
		run := rows[i:j]
		if len(run) == 1 {
			out = append(out, run[0])
		} else {
			items := run
			if len(items) > maxGroupItems {
				items = items[len(items)-maxGroupItems:]
			}
			g := LogEntry{TS: run[0].TS, Kind: "tools", Who: run[0].Who, Actor: run[0].Actor,
				Text: fmt.Sprintf("%d tool calls", len(run)), Count: len(run), Tag: run[len(run)-1].Tag,
				Items: append([]LogEntry(nil), items...)}
			out = append(out, g)
		}
		i = j
	}
	return out
}
