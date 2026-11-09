package state

import (
	"strings"
	"testing"
	"time"
)

func tev(sec int, name, tool, id, summary string) Event {
	return Event{TS: time.Date(2026, 1, 2, 3, 4, sec, 0, time.UTC).Format(time.RFC3339Nano), SessionID: "s", Name: name, ToolName: tool, ToolUseID: id, Target: summary}
}

func withAgent(e Event, agent string) Event { e.AgentID = agent; return e }

func withDur(e Event, ms int64) Event { e.DurationMs = ms; return e }

func TestNowPairsPreAndPostByToolUseID(t *testing.T) {
	events := []Event{
		tev(0, "UserPromptSubmit", "", "", ""),
		tev(1, "PreToolUse", "Read", "a", "x.go"),
		tev(1, "PreToolUse", "Grep", "b", "foo"), // parallel call
		withDur(tev(2, "PostToolUse", "Grep", "b", "foo"), 900),
	}
	s := Build(events, nil)
	if s.Now == nil || s.Now.ID != "a" || s.Now.Name != "Read" || s.Now.Target != "x.go" || s.Now.Done {
		t.Fatalf("now = %+v, want the Read still in flight", s.Now)
	}
	if s.Last == nil || s.Last.ID != "b" || !s.Last.Done || s.Last.DurMs != 900 {
		t.Errorf("last = %+v", s.Last)
	}

	events = append(events, tev(4, "PostToolUse", "Read", "a", ""))
	s = Build(events, nil)
	if s.Now != nil {
		t.Errorf("now = %+v after the matching Post", s.Now)
	}
	if s.Last.ID != "a" || s.Last.Target != "x.go" || s.Last.DurMs != 3000 { // no duration_ms: the Pre/Post gap
		t.Errorf("last = %+v", s.Last)
	}
	if len(s.Recent) != 2 {
		t.Errorf("recent = %d", len(s.Recent))
	}
}

func TestToolRowIsUpdatedInPlace(t *testing.T) {
	events := []Event{
		tev(1, "PreToolUse", "Bash", "a", "go test ./..."),
		tev(2, "PreToolUse", "Read", "b", "y.go"),
	}
	s := Build(events, nil)
	if len(s.Log) != 2 || s.Log[0].Tag != "run" || s.Log[0].Text != "Bash go test ./..." {
		t.Fatalf("log = %+v", s.Log)
	}
	events = append(events, withDur(tev(5, "PostToolUse", "Bash", "a", "go test ./..."), 3200), withDur(tev(5, "PostToolUseFailure", "Read", "b", "y.go"), 4))
	s = Build(events, nil)
	if len(s.Log) != 2 {
		t.Fatalf("a Post must not add a row: %+v", s.Log)
	}
	if s.Log[0].Text != "Bash go test ./... ✓ 3.2s" || s.Log[0].Tag != "ok" || s.Log[1].Text != "Read y.go ✗ 4ms" || s.Log[1].Tag != "fail" {
		t.Errorf("log = %+v", s.Log)
	}
	if s.ToolFailures != 1 {
		t.Errorf("tool failures = %d", s.ToolFailures)
	}
}

func TestPostWithoutPreStillLogs(t *testing.T) { // hooks installed for Post only
	s := Build([]Event{withDur(tev(5, "PostToolUse", "Bash", "a", "ls"), 2000)}, nil)
	if len(s.Log) != 1 || s.Log[0].Text != "Bash ls ✓ 2.0s" || s.Now != nil || s.Last == nil {
		t.Errorf("log = %+v now = %+v last = %+v", s.Log, s.Now, s.Last)
	}
	if got := s.Last.Start; !got.Equal(time.Date(2026, 1, 2, 3, 4, 3, 0, time.UTC)) {
		t.Errorf("start = %v", got)
	}
}

func TestSubagentActivityIsKeptPerAgent(t *testing.T) {
	start := tev(0, "SubagentStart", "", "", "")
	start.AgentID, start.AgentType = "ag1", "Explore"
	events := []Event{
		start,
		tev(1, "PreToolUse", "Edit", "m1", "main.go"),
		withAgent(tev(2, "PreToolUse", "Read", "s1", "a.go"), "ag1"),
		withAgent(tev(3, "PreToolUse", "Grep", "s2", "pat in ."), "ag1"),
		withAgent(tev(4, "PostToolUse", "Read", "s1", "a.go"), "ag1"),
	}
	s := Build(events, nil)
	if s.Now == nil || s.Now.ID != "m1" {
		t.Errorf("main now = %+v", s.Now)
	}
	a := s.Subagents[0]
	if a.Now == nil || a.Now.ID != "s2" || a.Last == nil || a.Last.ID != "s1" {
		t.Errorf("subagent now = %+v last = %+v", a.Now, a.Last)
	}
	if len(s.Subagents) != 1 {
		t.Errorf("a tool event must not create a subagent: %d", len(s.Subagents))
	}
	if got := s.Log[len(s.Log)-1].Who; got != "Explore ag1" {
		t.Errorf("who = %q", got)
	}

	stop := tev(6, "SubagentStop", "", "", "")
	stop.AgentID, stop.AgentType = "ag1", "Explore"
	s = Build(append(events, stop), nil)
	if s.Subagents[0].Now != nil {
		t.Errorf("a stopped agent still has a current tool: %+v", s.Subagents[0].Now)
	}
}

func TestStopEndsUnfinishedMainCalls(t *testing.T) {
	events := []Event{tev(0, "UserPromptSubmit", "", "", ""), tev(1, "PreToolUse", "Bash", "a", "sleep 99"), tev(2, "Stop", "", "", "")}
	s := Build(events, nil)
	if s.Now != nil || len(s.Log) != 1 || s.Log[0].Tag != "end" {
		t.Errorf("now = %+v log = %+v", s.Now, s.Log)
	}
}

func TestGroupTools(t *testing.T) {
	row := func(actor, text string) LogEntry {
		return LogEntry{Kind: "tool", Actor: actor, Who: "w" + actor, Text: text, Tag: "ok"}
	}
	prompt := LogEntry{Kind: "you", Who: "you", Text: "go"}
	in := []LogEntry{prompt, row("", "Read a"), row("", "Read b"), row("", "Read c"), row("x", "Grep d"), prompt, row("", "Edit e")}
	out := GroupTools(in)
	if len(out) != 5 {
		t.Fatalf("rows = %+v", out)
	}
	g := out[1]
	if g.Kind != "tools" || g.Text != "3 tool calls" || g.Count != 3 || len(g.Items) != 3 || g.Items[2].Text != "Read c" {
		t.Errorf("group = %+v", g)
	}
	if out[2].Kind != "tool" || out[2].Actor != "x" || out[4].Kind != "tool" { // a single call stays a plain row
		t.Errorf("single rows = %+v %+v", out[2], out[4])
	}

	var many []LogEntry
	for i := 0; i < 50; i++ {
		many = append(many, row("", "Read f"))
	}
	g = GroupTools(many)[0]
	if g.Count != 50 || len(g.Items) != maxGroupItems || !strings.HasSuffix(g.Text, "50 tool calls") {
		t.Errorf("big group: count %d items %d text %q", g.Count, len(g.Items), g.Text)
	}
}

func TestToolLabelAndDuration(t *testing.T) {
	if got := ToolLabel("mcp__srv__x", "srv/x"); got != "mcp srv/x" {
		t.Errorf("label = %q", got)
	}
	if got := ToolLabel("TodoWrite", ""); got != "TodoWrite" {
		t.Errorf("label = %q", got)
	}
	for ms, want := range map[int64]string{0: "0ms", 420: "420ms", 3200: "3.2s", 12_400: "12s", 65_000: "1m05s"} {
		if got := FmtDur(ms); got != want {
			t.Errorf("FmtDur(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestDuplicateDeliveryOfAnEventAddsNoRow(t *testing.T) { // the same hook registered twice (global + --settings)
	pre := tev(1, "PreToolUse", "Bash", "a", "ls")
	post := withDur(tev(2, "PostToolUse", "Bash", "a", "ls"), 5)
	s := Build([]Event{pre, pre, post, post}, nil)
	if len(s.Log) != 1 || len(s.Recent) != 1 || s.Now != nil || s.Log[0].Tag != "ok" {
		t.Errorf("log = %+v recent = %d now = %+v", s.Log, len(s.Recent), s.Now)
	}
}
