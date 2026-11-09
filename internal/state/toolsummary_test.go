package state

import (
	"strings"
	"testing"
)

func TestToolSummaryPerTool(t *testing.T) {
	long := strings.Repeat("x", 400)
	tests := []struct {
		name, tool string
		in         ToolInput
		target     string
		want       string
	}{
		{"bash first line", "Bash", ToolInput{"command": "go test ./...\necho done"}, "", "go test ./... …"},
		{"bash single line", "Bash", ToolInput{"command": "  ls -la  "}, "", "ls -la"},
		{"bash cut", "Bash", ToolInput{"command": long}, "", strings.Repeat("x", 300) + "…"},
		{"read relative", "Read", ToolInput{"file_path": "/work/app/internal/a.go"}, "", "internal/a.go"},
		{"read outside cwd", "Read", ToolInput{"file_path": "/etc/hosts"}, "", "/etc/hosts"},
		{"read offset limit", "Read", ToolInput{"file_path": "/work/app/a.go", "offset": 120.0, "limit": 40.0}, "", "a.go:120+40"},
		{"read limit only", "Read", ToolInput{"file_path": "/work/app/a.go", "limit": 40.0}, "", "a.go:0+40"},
		{"edit lines", "Edit", ToolInput{"file_path": "/work/app/README.md", "old_string": "a\nb\nc", "new_string": "1\n2\n3\n4\n5"}, "", "README.md −3 +5 lines"},
		{"multiedit sums", "MultiEdit", ToolInput{"file_path": "/work/app/m.go", "edits": []any{
			map[string]any{"old_string": "a", "new_string": "b\nc"}, map[string]any{"old_string": "d\ne", "new_string": "f"}}}, "", "m.go −3 +3 lines"},
		{"write lines", "Write", ToolInput{"file_path": "/work/app/w.txt", "content": "l1\nl2\nl3\n"}, "", "w.txt 3 lines"},
		{"grep flags", "Grep", ToolInput{"pattern": "func Foo", "path": "/work/app/internal", "glob": "*.go", "type": "go"}, "", "func Foo path=internal glob=*.go type=go"},
		{"grep pattern only", "Grep", ToolInput{"pattern": "TODO"}, "", "TODO"},
		{"glob with path", "Glob", ToolInput{"pattern": "**/*.go", "path": "/work/app/cmd"}, "", "**/*.go in cmd"},
		{"webfetch", "WebFetch", ToolInput{"url": "https://example.org/docs?a=1", "prompt": "P"}, "", "https://example.org/docs?a=1"},
		{"websearch", "WebSearch", ToolInput{"query": "go embed"}, "", "go embed"},
		{"agent", "Agent", ToolInput{"subagent_type": "Explore", "description": "find callers", "prompt": "BRIEF"}, "", "Explore: find callers"},
		{"task alias without type", "Task", ToolInput{"description": "find callers"}, "", "find callers"},
		{"task tool", "TaskUpdate", ToolInput{"taskId": "7", "status": "completed"}, "", "#7 completed"},
		{"task create", "TaskCreate", ToolInput{"subject": "Write tests", "description": "D"}, "", "Write tests"},
		{"mcp scalars", "mcp__srv__do_thing", ToolInput{"query": "q", "limit": 5.0, "nested": map[string]any{"a": 1.0}}, "", "srv.do_thing limit=5 query=q"},
		{"other tool scalars", "TodoWrite", ToolInput{"merge": true, "todos": []any{"x"}}, "", "merge=true"},
		{"empty input falls back to target", "Bash", nil, "go vet ./...", "go vet ./..."},
		{"input without summary falls back to target", "Grep", ToolInput{"pattern": ""}, "old target", "old target"},
		{"legacy tool_input command", "Bash", ToolInput{"command": "go build ./...", "description": "build"}, "", "go build ./..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ToolSummary(tc.tool, tc.in, tc.target, "/work/app"); got != tc.want {
				t.Errorf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLegacyEventsShowTheirCommand(t *testing.T) {
	// written before the full input was stored: only a Post, its tool_input holds just the command
	post := withDur(tev(2, "PostToolUse", "Bash", "old", ""), 28)
	post.ToolInput = ToolInput{"command": "make build\nsecond line", "description": "run"}
	s := Build([]Event{post}, nil)
	if len(s.Log) != 1 || s.Log[0].Text != "Bash make build … ✓ 28ms" || s.Log[0].Tool != "Bash" || s.Log[0].Summary != "make build …" {
		t.Errorf("log = %+v", s.Log)
	}
	if !strings.HasPrefix(s.Log[0].Key, "t") {
		t.Errorf("tool row has no key: %+v", s.Log[0])
	}
}

func TestPostWithoutInputUsesThePreInput(t *testing.T) {
	pre := tev(1, "PreToolUse", "Bash", "a", "go test")
	pre.ToolInput = ToolInput{"command": "go test ./..."}
	post := withDur(tev(2, "PostToolUse", "Bash", "a", "go test"), 5)
	s := Build([]Event{tev(0, "UserPromptSubmit", "", "", ""), pre, post}, nil)
	if s.TestsPass != 1 {
		t.Errorf("test detection must read the command of the Pre: pass=%d", s.TestsPass)
	}
	if s.Log[len(s.Log)-1].Kind == "" || s.Last == nil || s.Last.Summary != "go test ./..." || s.Last.Input != nil {
		t.Errorf("last = %+v", s.Last)
	}
	// the row key is the Pre event's index
	for _, r := range s.Log {
		if r.Kind == "tool" && r.Key != "t1" {
			t.Errorf("key = %q, want t1", r.Key)
		}
	}
}
