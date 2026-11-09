package pipeline_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/hook"
	"github.com/toniim/ccwatch/internal/otlp"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/view"
	"github.com/toniim/ccwatch/internal/web"
)

const orcaBoiler = "You are working inside Orca, a multi-agent IDE. You are a dispatched worker.\n" +
	"=== CLI COMMANDS ===\nBOILERPLATE-A\n=== AFTER YOU SEND worker_done ===\nBOILERPLATE-B\n=== SUB-DISPATCH ===\nBOILERPLATE-C\n---\n"

func orcaPrompt(task string) string {
	return "Please carry out this task from my Orca coordinator by following the brief I pasted below.\n\n" +
		"<pasted_content id=\"c1\">\n" + orcaBoiler + "=== TASK ===\n" + task + "\n</pasted_content id=\"c1\">"
}

const notification = "<task-notification>\n<task-id>t1</task-id>\n<status>completed</status>\n<summary>Agent \"Scout X\" finished</summary>\n<result>RESULT-BODY</result>\n</task-notification>"

// storedPrompts runs prompts through the hook into one session and returns the dir.
func storedPrompts(t *testing.T, prompts ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, p := range prompts {
		b, _ := json.Marshal(map[string]string{"session_id": "s1", "hook_event_name": "UserPromptSubmit", "prompt": p})
		if err := hook.Run(strings.NewReader(string(b)), dir, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHookStoresWholePrompt(t *testing.T) {
	long := orcaPrompt(strings.Repeat("é", 5000))
	paste := "look:\n<pasted_content id=\"p1\">\nalpha\nbeta\n</pasted_content id=\"p1\">"
	dir := storedPrompts(t, long, paste, "please delegate", notification)
	evs := state.LoadEvents(dir, "s1")
	if len(evs) != 4 {
		t.Fatalf("events = %d", len(evs))
	}
	want := []struct{ prompt, kind string }{{long, "orca_task"}, {paste, "paste"}, {"please delegate", ""}, {notification, "task_notification"}}
	for i, w := range want {
		if evs[i].Prompt != w.prompt || evs[i].PromptKind != w.kind || evs[i].PromptChars != len([]rune(w.prompt)) {
			t.Errorf("event %d: kind=%q chars=%d (want kind %q, %d)", i, evs[i].PromptKind, evs[i].PromptChars, w.kind, len([]rune(w.prompt)))
		}
	}
}

func logOf(t *testing.T, e state.Event) state.LogEntry {
	t.Helper()
	s := state.Build([]state.Event{e}, nil)
	if len(s.Log) != 1 {
		t.Fatalf("log = %+v", s.Log)
	}
	return s.Log[0]
}

func TestPromptLogRows(t *testing.T) {
	ev := func(p string) state.Event { return state.Event{Name: "UserPromptSubmit", SessionID: "s1", Prompt: p} }
	r := logOf(t, ev(orcaPrompt("\nFix the widget.\nSecond line.")))
	if r.Kind != "you" || r.Tag != "orca_task" || r.Text != "[orca task] Fix the widget." || r.Key != "p0" || r.FullChars != len("Fix the widget.\nSecond line.") {
		t.Errorf("orca row = %+v", r)
	}
	r = logOf(t, ev("look:\n<pasted_content id=\"p1\">\nalpha\nbeta\n</pasted_content id=\"p1\">"))
	if r.Kind != "you" || r.Text != "alpha" || r.FullChars != len("alpha\nbeta") {
		t.Errorf("paste row = %+v", r)
	}
	r = logOf(t, ev("please delegate"))
	if r.Kind != "you" || r.Text != "please delegate" || r.FullChars != 0 || r.Key == "" {
		t.Errorf("plain row = %+v", r)
	}
	r = logOf(t, ev(notification))
	if r.Kind != "notice" || r.Text != `Agent "Scout X" finished` || r.Tag != "completed" || r.Key != "" {
		t.Errorf("notice row = %+v", r)
	}
	r = logOf(t, ev(strings.Repeat("x", 900)))
	if len([]rune(r.Text)) != 300 || r.FullChars != 900 {
		t.Errorf("long row: text=%d full=%d", len([]rune(r.Text)), r.FullChars)
	}
}

// Events stored before the full prompt was kept hold 200 chars of the raw prompt.
func TestPromptLogRowsOldEvents(t *testing.T) {
	old := []rune(orcaPrompt("neutral task"))
	e := state.Event{Name: "UserPromptSubmit", SessionID: "s1", Prompt: string(old[:200])}
	if r := logOf(t, e); r.Kind != "you" || r.Text != "[orca task] (brief not captured)" || r.FullChars != 0 {
		t.Errorf("old orca row = %+v", r)
	}
	e.Prompt = "<task-notification>\n<status>completed</status>\n<summary>Agent \"Scout X\" finished</summary>"
	if r := logOf(t, e); r.Kind != "notice" || r.Text != `Agent "Scout X" finished` {
		t.Errorf("old notice row = %+v", r)
	}
}

func promptServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	src := view.NewSource(dir)
	src.ClaudeHome, src.OrcaOn = td("claude"), false
	srv := httptest.NewServer(web.NewServer(src, "127.0.0.1:4319").Wrap(otlp.Handler(dir)))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebPromptAPI(t *testing.T) {
	task := "Neutral task line one.\n" + strings.TrimSpace(strings.Repeat("detail é ", 560)) // > 5,000 runes
	full := orcaPrompt(task)
	if n := len([]rune(full)); n < 5000 {
		t.Fatalf("fixture too short: %d", n)
	}
	dir := storedPrompts(t, full, notification)
	srv := promptServer(t, dir)

	var d view.Dashboard
	if code := getJSON(t, srv.URL+"/api/state?session=s1", &d); code != 200 || len(d.Log) != 2 {
		t.Fatalf("state: %d %+v", code, d.Log)
	}
	row := d.Log[0]
	if row.Text != "[orca task] Neutral task line one." || row.FullChars != len([]rune(task)) || row.Key == "" {
		t.Fatalf("row = %+v", row)
	}
	var raw []byte
	raw, _ = json.Marshal(d)
	if strings.Contains(string(raw), "BOILERPLATE") || len(raw) > 4000 {
		t.Errorf("/api/state must carry previews only (%d bytes)", len(raw))
	}

	type resp struct {
		Text  string `json:"text"`
		Kind  string `json:"kind"`
		Raw   bool   `json:"raw"`
		Chars int    `json:"chars"`
	}
	q := func(extra string) (int, resp) {
		var r resp
		code := getJSON(t, srv.URL+"/api/prompt?session=s1&key="+url.QueryEscape(row.Key)+extra, &r)
		return code, r
	}
	if code, r := q(""); code != 200 || r.Text != task || r.Kind != "orca_task" || r.Raw || r.Chars != len([]rune(task)) {
		t.Errorf("display: %d kind=%s chars=%d text ok=%v", code, r.Kind, r.Chars, r.Text == task)
	}
	if code, r := q("&raw=1"); code != 200 || r.Text != full || !r.Raw || !strings.Contains(r.Text, "BOILERPLATE-B") {
		t.Errorf("raw: %d raw=%v", code, r.Raw)
	}

	for _, c := range []struct {
		query string
		want  int
	}{
		{"session=s1&key=p99", 404},
		{"session=s1&key=nope", 404},
		{"session=s1&key=p-1", 404},
		{"session=s1&key=p1x", 404},
		{"session=s1", 404},
		{"session=s1&key=p1", 200}, // the notification is a stored prompt too
		{"session=..%2Fx&key=p0", 400},
		{"session=missing&key=p0", 404},
	} {
		var junk map[string]any
		if code := getJSON(t, srv.URL+"/api/prompt?"+c.query, &junk); code != c.want {
			t.Errorf("%s: %d, want %d", c.query, code, c.want)
		}
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/prompt?session=s1&key=p0", nil)
	req.Host = "evil.example"
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 403 {
		t.Errorf("host guard: %v %v", res, err)
	}
	res, _ := http.Get(srv.URL + "/api/prompt?session=s1&key=p0")
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("cache-control = %q", res.Header.Get("Cache-Control"))
	}
	res.Body.Close()
}
