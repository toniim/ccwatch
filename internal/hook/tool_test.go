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
		{"mcp is server/tool only", "mcp__srv__do_thing", `{"query":"Q","path":"/x"}`, "srv/do_thing"},
		{"unlisted tool has no target", "TodoWrite", `{"todos":[{"content":"ITEM"}]}`, ""},
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
			if e.ToolInput == nil || e.ToolResponse != nil || e.ToolOutput != nil {
				t.Errorf("a PreToolUse event keeps the input and no response: %s", line)
			}
			if e.Cwd != "" {
				t.Errorf("cwd stored on a per-call line: %q", e.Cwd)
			}
		})
	}
}

func TestEditStoresItsInputButNeverTheResponseBody(t *testing.T) {
	payload := `{"session_id":"s1","cwd":"/work/app","hook_event_name":"PostToolUse","tool_name":"Edit","tool_use_id":"tu1","duration_ms":12,` +
		`"tool_input":{"file_path":"/work/app/a.go","old_string":"IN-OLD","new_string":"IN-NEW"},` +
		`"tool_response":{"filePath":"/work/app/a.go","originalFile":"RESP-ORIGINAL","structuredPatch":[{"lines":["+RESP-PATCH"]}]}}`
	e, line := convert(t, payload)
	for _, leak := range []string{"RESP-ORIGINAL", "RESP-PATCH"} {
		if strings.Contains(line, leak) {
			t.Errorf("%q stored: %s", leak, line)
		}
	}
	if e.Target != "a.go" || e.DurationMs != 12 || e.ToolInput.Str("old_string") != "IN-OLD" || e.ToolInput.Str("new_string") != "IN-NEW" || e.ToolResponse != nil {
		t.Errorf("event = %+v", e)
	}
	if e.ToolOutput == nil || e.ToolOutput.Bytes != len(`{"filePath":"/work/app/a.go","originalFile":"RESP-ORIGINAL","structuredPatch":[{"lines":["+RESP-PATCH"]}]}`) || e.ToolOutput.Stdout != nil {
		t.Errorf("a non-Bash response keeps only its size: %+v", e.ToolOutput)
	}

	// the failure text of a non-Bash tool can quote the old string: it is dropped, the input stays
	fail := `{"session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Edit","tool_use_id":"tu2","error":"String to replace not found: ERR-QUOTE","tool_input":{"file_path":"a.go","old_string":"IN-OLD"}}`
	e, line = convert(t, fail)
	if strings.Contains(line, "ERR-QUOTE") || e.Error != "" || e.Name != "PostToolUseFailure" || e.ToolInput.Str("old_string") != "IN-OLD" {
		t.Errorf("failure event = %s", line)
	}
}

func TestBashAndAgentKeepTheirHandling(t *testing.T) {
	post := `{"session_id":"s1","cwd":"/work/app","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"tu1","duration_ms":3200,` +
		`"tool_input":{"command":"go test ./...","description":"run tests"},"tool_response":{"stdout":"ok  pkg 0.1s","stderr":"warn","interrupted":true}}`
	e, _ := convert(t, post)
	if e.Cwd != "/work/app" || e.ToolInput.Str("command") != "go test ./..." || e.ToolResponse == nil || e.ToolResponse.Text != "ok  pkg 0.1s" ||
		e.Target != "go test ./..." || e.DurationMs != 3200 {
		t.Errorf("bash post = %+v input=%+v resp=%+v", e, e.ToolInput, e.ToolResponse)
	}
	o := e.ToolOutput
	if o == nil || o.Stdout == nil || o.Stdout.Head != "ok  pkg 0.1s" || o.Stdout.Tail != "" || o.Stdout.Chars != 12 || o.Stderr == nil || o.Stderr.Head != "warn" || !o.Interrupted {
		t.Errorf("bash output = %+v", o)
	}
	agent := `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Agent","tool_use_id":"tu9","tool_input":{"description":"scan","subagent_type":"Explore","run_in_background":true,"prompt":"BRIEF"},` +
		`"tool_response":{"status":"completed","agentId":"a1","agentType":"Explore","totalTokens":5}}`
	e, _ = convert(t, agent)
	if !e.ToolInput.Bool("run_in_background") || e.ToolInput.Str("prompt") != "BRIEF" || e.ToolResponse == nil || e.ToolResponse.AgentID != "a1" || e.Target != "Explore: scan" {
		t.Errorf("agent post = %+v", e)
	}
}

func TestStoredInputCapsEveryStringAndKeepsTheLength(t *testing.T) {
	big := strings.Repeat("é", 12000) // 24000 bytes
	in := `{"file_path":"/work/app/a.go","content":"` + big + `","limit":40,"edits":[{"old_string":"` + big + `"}],"flag":true}`
	e, line := convert(t, pre("Write", in))
	got := e.ToolInput.Str("content")
	if len(got) > maxField || len(got) < maxField-1 || !strings.HasPrefix(big, got) {
		t.Fatalf("content kept %d bytes", len(got))
	}
	if n, _ := e.ToolInput["content_chars"].(int); n != 12000 {
		t.Errorf("content_chars = %v", e.ToolInput["content_chars"])
	}
	if _, ok := e.ToolInput["file_path_chars"]; ok {
		t.Error("an uncut field got a _chars sibling")
	}
	nested := e.ToolInput["edits"].([]any)[0].(map[string]any)
	if len(nested["old_string"].(string)) > maxField || nested["old_string_chars"] != 12000 {
		t.Errorf("nested edit = %d bytes, chars %v", len(nested["old_string"].(string)), nested["old_string_chars"])
	}
	if !strings.Contains(line, `"limit":40`) || !strings.Contains(line, `"flag":true`) {
		t.Errorf("scalars lost: %s", line)
	}
}

func TestBashOutputPreviewIsHeadAndTail(t *testing.T) {
	out := strings.Repeat("a", 1024) + strings.Repeat("m", 3000) + strings.Repeat("z", 1024)
	e, line := convert(t, `{"session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"tu1","tool_input":{"command":"x"},"tool_response":{"stdout":"`+out+`"}}`)
	p := e.ToolOutput.Stdout
	if p.Head != strings.Repeat("a", 1024) || p.Tail != strings.Repeat("z", 1024) || p.Chars != 5048 {
		t.Errorf("preview head %d tail %d chars %d", len(p.Head), len(p.Tail), p.Chars)
	}
	if strings.Contains(line, "mmmm") {
		t.Error("the middle of the output was stored")
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

func readEvents(t *testing.T, dir, id string) []state.Event {
	t.Helper()
	return state.LoadEvents(dir, id)
}

func TestRunWritesInputOnPreAndOnPostOnlyWithoutPre(t *testing.T) {
	dir := t.TempDir()
	mk := func(ev, id string) string {
		return `{"session_id":"s1","cwd":"/work/app","hook_event_name":"` + ev + `","tool_name":"Bash","tool_use_id":"` + id + `","tool_input":{"command":"ls"},"tool_response":{"stdout":"x"}}`
	}
	for _, p := range []string{mk("PreToolUse", "tuA"), mk("PostToolUse", "tuA"), mk("PostToolUse", "tuB")} {
		if err := Run(strings.NewReader(p), dir, t0); err != nil {
			t.Fatal(err)
		}
	}
	evs := readEvents(t, dir, "s1")
	if len(evs) != 3 {
		t.Fatalf("%d events", len(evs))
	}
	if evs[0].ToolInput.Str("command") != "ls" || evs[1].ToolInput != nil || evs[2].ToolInput.Str("command") != "ls" {
		t.Errorf("inputs: pre %v, post-with-pre %v, post-without-pre %v", evs[0].ToolInput, evs[1].ToolInput, evs[2].ToolInput)
	}
	if evs[1].ToolOutput == nil || evs[1].ToolOutput.Stdout == nil {
		t.Errorf("the Post keeps its output: %+v", evs[1].ToolOutput)
	}
}

func TestMarshalLineShrinksAnEventOverTheCap(t *testing.T) {
	in := state.ToolInput{}
	for i := 0; i < 70; i++ { // 70 capped fields of 16 KiB: more than the stdin cap in all
		in["f"+strings.Repeat("x", i)] = strings.Repeat("a", maxField)
	}
	e := state.Event{SessionID: "s1", Name: "PreToolUse", ToolName: "X", ToolInput: in}
	line, err := marshalLine(e)
	if err != nil || len(line) > maxLine || strings.Count(string(line), "\n") != 1 {
		t.Fatalf("line of %d bytes, %d lines, err %v", len(line), strings.Count(string(line), "\n"), err)
	}
	if !strings.Contains(string(line), `"tool_input"`) || strings.Contains(string(line), strings.Repeat("a", maxFieldTiny+1)) {
		t.Errorf("expected strings cut to %d bytes", maxFieldTiny)
	}
}
