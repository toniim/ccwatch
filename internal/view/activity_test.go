package view

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/state"
)

func aev(sec int, name, tool, id, summary string) state.Event {
	return state.Event{TS: time.Date(2026, 1, 2, 3, 4, sec, 0, time.UTC).Format(time.RFC3339Nano), SessionID: "s", Name: name, ToolName: tool, ToolUseID: id, Target: summary}
}

func TestMainNowRunningThenLastWhenIdle(t *testing.T) {
	prompt := aev(0, "UserPromptSubmit", "", "", "")
	events := []state.Event{prompt, aev(1, "PreToolUse", "Edit", "a", "internal/web/app.js")}
	d := Build(state.Build(events, nil), Ext{})
	if d.Now == nil || !d.Now.Running || d.Now.Text != "Edit internal/web/app.js" || d.Now.Start == "" {
		t.Fatalf("now = %+v", d.Now)
	}

	done := aev(5, "PostToolUse", "Edit", "a", "")
	done.DurationMs = 4200
	events = append(events, done, aev(6, "Stop", "", "", ""))
	d = Build(state.Build(events, nil), Ext{})
	if d.State != "idle" || d.Now == nil || d.Now.Running || d.Now.DurMs != 4200 || d.Now.Text != "Edit internal/web/app.js" {
		t.Errorf("idle now = %+v state %s", d.Now, d.State)
	}

	// an unfinished call of a session that is idle is not shown as running
	stale := []state.Event{aev(1, "PreToolUse", "Bash", "z", "sleep 9")}
	if d = Build(state.Build(stale, nil), Ext{}); d.Now != nil { // no prompt seen: idle, and no finished call to fall back to
		t.Errorf("idle session shows a running call: %+v", d.Now)
	}
	if d = Build(state.Build(nil, nil), Ext{}); d.Now != nil {
		t.Errorf("empty session now = %+v", d.Now)
	}
}

func TestLaneShowsRunningAgentTool(t *testing.T) {
	start := func(sec int, id string) state.Event {
		e := aev(sec, "SubagentStart", "", "", "")
		e.AgentID, e.AgentType = id, "Explore"
		return e
	}
	tool := func(sec int, id, tool, tid, summary string) state.Event {
		e := aev(sec, "PreToolUse", tool, tid, summary)
		e.AgentID = id
		return e
	}
	events := []state.Event{
		start(0, "ag1"), start(1, "ag2"), start(2, "ag3"),
		tool(3, "ag1", "Read", "t1", "a.go"),
		tool(4, "ag2", "Grep", "t2", "foo in ."),
	}
	stop := aev(5, "SubagentStop", "", "", "")
	stop.AgentID, stop.AgentType = "ag3", "Explore"
	events = append(events, stop)
	d := Build(state.Build(events, nil), Ext{})
	if len(d.Lanes) != 1 {
		t.Fatalf("lanes = %+v", d.Lanes)
	}
	l := d.Lanes[0]
	if len(l.Titles) != 3 || l.Titles[0] != "running · Read a.go" || l.Titles[1] != "running · Grep foo in ." || l.Titles[2] != "done" {
		t.Errorf("titles = %q", l.Titles)
	}
	if l.Now == nil || !l.Now.Running || l.Now.Text != "Grep foo in ." || l.Now.More != 1 {
		t.Errorf("lane now = %+v", l.Now)
	}
	if d.Now != nil {
		t.Errorf("main now = %+v: a subagent's call is not the main session's", d.Now)
	}
}

func TestLogKeepsRowsWithoutToolsAndGroupsTheRest(t *testing.T) {
	var events []state.Event
	sec := 0
	add := func(e state.Event) { events = append(events, e) }
	for i := 0; i < 15; i++ { // 15 model rows, each followed by 3 tool calls
		sec++
		stop := aev(sec%60, "Stop", "", "", "")
		stop.LastAssistantMsg = fmt.Sprintf("answer %d", i)
		add(stop)
		for j := 0; j < 3; j++ {
			id := fmt.Sprintf("t%d-%d", i, j)
			add(aev(sec%60, "PreToolUse", "Read", id, "f.go"))
			add(aev(sec%60, "PostToolUse", "Read", id, ""))
		}
	}
	d := Build(state.Build(events, nil), Ext{})
	models, groups := 0, 0
	for _, r := range d.Log {
		switch r.Kind {
		case "model":
			models++
		case "tools":
			groups++
			if r.Text != "3 tool calls" || len(r.Items) != 3 {
				t.Errorf("group = %+v", r)
			}
		}
	}
	if models != LogRows || groups == 0 || len(d.Log) > maxLogRows {
		t.Errorf("rows %d: models %d groups %d", len(d.Log), models, groups)
	}
}

func TestNowAndLastAreOneLineWithTheFullCommandAsTitle(t *testing.T) {
	cmd := "go test ./...\n  && echo done\n  && echo again"
	pre := aev(1, "PreToolUse", "Bash", "a", "")
	pre.ToolInput = state.ToolInput{"command": cmd}
	d := Build(state.Build([]state.Event{aev(0, "UserPromptSubmit", "", "", ""), pre}, nil), Ext{})
	if d.Now == nil || strings.Contains(d.Now.Text, "\n") || d.Now.Text != "Bash go test ./... …" || d.Now.Title != "Bash "+cmd {
		t.Fatalf("now = %+v", d.Now)
	}
	post := aev(3, "PostToolUse", "Bash", "a", "")
	d = Build(state.Build([]state.Event{aev(0, "UserPromptSubmit", "", "", ""), pre, post, aev(4, "Stop", "", "", "")}, nil), Ext{})
	if d.Now == nil || d.Now.Running || strings.Contains(d.Now.Text, "\n") || d.Now.Text != "Bash go test ./... …" || d.Now.Title != "Bash "+cmd {
		t.Errorf("last = %+v", d.Now)
	}
	// a short call needs no title
	short := aev(1, "PreToolUse", "Read", "b", "a.go")
	d = Build(state.Build([]state.Event{aev(0, "UserPromptSubmit", "", "", ""), short}, nil), Ext{})
	if d.Now == nil || d.Now.Title != "" {
		t.Errorf("short now = %+v", d.Now)
	}
}
