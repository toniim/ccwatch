package hook

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/state"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func convert(t *testing.T, payload string) (state.Event, string) {
	t.Helper()
	e, ok := Convert([]byte(payload), t0)
	if !ok {
		t.Fatalf("payload rejected: %s", payload)
	}
	b, _ := json.Marshal(e)
	return e, string(b)
}

func pre(tool, input string) string {
	return `{"session_id":"s1","cwd":"/work/app","hook_event_name":"PreToolUse","tool_name":"` + tool + `","tool_use_id":"tu1","tool_input":` + input + `}`
}

func TestTargetPerTool(t *testing.T) {
	long := strings.Repeat("é", 400)
	tests := []struct {
		name, tool, input, want string
	}{
		{"bash collapses whitespace", "Bash", `{"command":"go  test\n  ./..."}`, "go test ./..."},
		{"bash cut at 300 runes", "Bash", `{"command":"` + long + `"}`, strings.Repeat("é", 300)},
		{"read under cwd is relative", "Read", `{"file_path":"/work/app/internal/x.go"}`, "internal/x.go"},
		{"read outside cwd stays absolute", "Read", `{"file_path":"/etc/hosts"}`, "/etc/hosts"},
		{"read sibling dir that shares the prefix", "Read", `{"file_path":"/work/app2/x.go"}`, "/work/app2/x.go"},
		{"edit", "Edit", `{"file_path":"/work/app/a.go","old_string":"OLD","new_string":"NEW"}`, "a.go"},
		{"write", "Write", `{"file_path":"/work/app/b.txt","content":"BODY"}`, "b.txt"},
		{"multiedit", "MultiEdit", `{"file_path":"/work/app/c.go","edits":[{"old_string":"o","new_string":"n"}]}`, "c.go"},
		{"notebook", "NotebookEdit", `{"notebook_path":"/work/app/n.ipynb","new_source":"SRC"}`, "n.ipynb"},
		{"grep with path", "Grep", `{"pattern":"func Foo","path":"/work/app/internal"}`, "func Foo in internal"},
		{"glob without path", "Glob", `{"pattern":"**/*.go"}`, "**/*.go"},
		{"webfetch keeps host and path only", "WebFetch", `{"url":"https://example.org/docs/a?token=SECRET#frag","prompt":"P"}`, "example.org/docs/a"},
		{"webfetch bad url", "WebFetch", `{"url":"::"}`, ""},
		{"websearch cut at 120", "WebSearch", `{"query":"` + strings.Repeat("q", 200) + `"}`, strings.Repeat("q", 120)},
		{"agent", "Agent", `{"description":"find callers","subagent_type":"Explore","prompt":"LONG BRIEF"}`, "Explore: find callers"},
		{"task alias", "Task", `{"description":"find callers"}`, "find callers"},
		{"mcp is server/tool only", "mcp__srv__do_thing", `{"query":"SECRET","path":"/x"}`, "srv/do_thing"},
		{"unlisted tool has no summary", "TodoWrite", `{"todos":[{"content":"SECRET"}]}`, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, line := convert(t, pre(tc.tool, tc.input))
			if e.Target != tc.want {
				t.Errorf("target = %q, want %q", e.Target, tc.want)
			}
			if e.ToolName != tc.tool || e.ToolUseID != "tu1" {
				t.Errorf("tool = %q id = %q", e.ToolName, e.ToolUseID)
			}
			if e.ToolInput != nil || e.ToolResponse != nil {
				t.Errorf("a PreToolUse event must keep no tool input or response: %s", line)
			}
			if e.Cwd != "" {
				t.Errorf("cwd stored on a per-call line: %q", e.Cwd)
			}
			if strings.Contains(line, "SECRET") {
				t.Errorf("secret leaked into the event: %s", line)
			}
		})
	}
}

func TestEditContentNeverStored(t *testing.T) {
	payload := `{"session_id":"s1","cwd":"/work/app","hook_event_name":"PostToolUse","tool_name":"Edit","tool_use_id":"tu1","duration_ms":12,` +
		`"tool_input":{"file_path":"/work/app/a.go","old_string":"OLD-TEXT","new_string":"NEW-TEXT","content":"FILE-BODY"},` +
		`"tool_response":{"filePath":"/work/app/a.go","oldString":"OLD-TEXT","newString":"NEW-TEXT","originalFile":"FILE-BODY","structuredPatch":[{"lines":["+PATCH-LINE"]}]}}`
	e, line := convert(t, payload)
	for _, leak := range []string{"OLD-TEXT", "NEW-TEXT", "FILE-BODY", "PATCH-LINE"} {
		if strings.Contains(line, leak) {
			t.Errorf("%q stored: %s", leak, line)
		}
	}
	if e.Target != "a.go" || e.DurationMs != 12 || e.ToolInput != nil || e.ToolResponse != nil {
		t.Errorf("event = %+v", e)
	}

	// the failure text of a non-Bash tool can quote the old string: it is dropped too
	fail := `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Edit","tool_use_id":"tu2","error":"String to replace not found: OLD-TEXT","tool_input":{"file_path":"a.go","old_string":"OLD-TEXT"}}`
	e, line = convert(t, fail)
	if strings.Contains(line, "OLD-TEXT") || e.Error != "" || e.Name != "PostToolUseFailure" {
		t.Errorf("failure event = %s", line)
	}
}

func TestBashAndAgentKeepTheirHandling(t *testing.T) {
	post := `{"session_id":"s1","cwd":"/work/app","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"tu1","duration_ms":3200,` +
		`"tool_input":{"command":"go test ./...","description":"run tests"},"tool_response":{"stdout":"ok  pkg 0.1s"}}`
	e, _ := convert(t, post)
	if e.Cwd != "/work/app" || e.ToolInput == nil || e.ToolInput.Command != "go test ./..." || e.ToolResponse == nil || e.ToolResponse.Text != "ok  pkg 0.1s" ||
		e.Target != "go test ./..." || e.DurationMs != 3200 {
		t.Errorf("bash post = %+v input=%+v resp=%+v", e, e.ToolInput, e.ToolResponse)
	}
	agent := `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Agent","tool_use_id":"tu9","tool_input":{"description":"scan","subagent_type":"Explore","run_in_background":true,"prompt":"BRIEF"},` +
		`"tool_response":{"status":"completed","agentId":"a1","agentType":"Explore","totalTokens":5}}`
	e, _ = convert(t, agent)
	if e.ToolInput == nil || !e.ToolInput.RunInBackground || e.ToolInput.Prompt != "BRIEF" || e.ToolResponse == nil || e.ToolResponse.AgentID != "a1" || e.Target != "Explore: scan" {
		t.Errorf("agent post = %+v", e)
	}
}

func TestToolEventSurvivesOddInputShapes(t *testing.T) {
	// an MCP tool may use any JSON type for any field name: the event must still be stored
	e, _ := convert(t, pre("mcp__srv__q", `{"query":{"a":1},"path":["x"],"url":7,"command":["ls"],"file_path":false,"run_in_background":"yes"}`))
	if e.Target != "srv/q" || e.ToolName != "mcp__srv__q" {
		t.Errorf("event = %+v", e)
	}
	e, _ = convert(t, pre("Read", `{"file_path":["x"]}`))
	if e.Target != "" {
		t.Errorf("non-string file_path gave target %q", e.Target)
	}
}

func TestSubagentToolEventKeepsAgent(t *testing.T) {
	e, _ := convert(t, `{"session_id":"s1","agent_id":"ag1","agent_type":"Explore","hook_event_name":"PreToolUse","tool_name":"Grep","tool_use_id":"tu1","tool_input":{"pattern":"x"}}`)
	if e.AgentID != "ag1" || e.AgentType != "Explore" || e.Target != "x" {
		t.Errorf("event = %+v", e)
	}
}
