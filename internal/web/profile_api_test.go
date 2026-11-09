package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/toniim/ccwatch/internal/view"
)

func TestSessionsAndStateExposeProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, extra string) {
		ev := `{"ts":"2026-01-01T10:00:00Z","session_id":"` + id + `","cwd":"/work/demo","hook_event_name":"SessionStart"` + extra + `}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "events", id+".jsonl"), []byte(ev), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tagged01", `,"profile":"work","config_dir":".config/claude-work/home"`)
	write("plain001", ``)
	src := view.NewSource(dir)
	src.OrcaOn = false
	srv := httptest.NewServer(NewServer(src, "127.0.0.1:4319").Wrap(http.NotFoundHandler()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, s := range list {
		got[s["id"].(string)] = s["profile"]
		if s["id"] == "tagged01" && s["label"] != "tagged01 · active · no agents · ? · work · demo" {
			t.Errorf("label = %q", s["label"])
		}
	}
	if got["tagged01"] != "work" || got["plain001"] != "" {
		t.Errorf("profile field = %v", got)
	}

	resp2, err := http.Get(srv.URL + "/api/state?session=tagged01")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var d view.Dashboard
	if err := json.NewDecoder(resp2.Body).Decode(&d); err != nil || d.Profile != "work" {
		t.Errorf("dashboard profile = %q (%v)", d.Profile, err)
	}
}

func toolSession(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	ev := `{"ts":"2026-01-01T10:00:00Z","session_id":"s1","cwd":"/work/demo","hook_event_name":"SessionStart"}` + "\n" +
		`{"ts":"2026-01-01T10:00:01Z","session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"tu1","target":"go test ./...","tool_input":{"command":"go test ./...\necho done","description":"run","command_chars":999}}` + "\n" +
		`{"ts":"2026-01-01T10:00:02Z","session_id":"s1","hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"tu1","duration_ms":28,"tool_output":{"bytes":40,"stdout":{"head":"ok  pkg","chars":7},"interrupted":true}}` + "\n" +
		`{"ts":"2026-01-01T10:00:03Z","session_id":"s1","hook_event_name":"PostToolUseFailure","tool_name":"Read","tool_use_id":"tu2","duration_ms":3,"error":"no such file","tool_input":{"file_path":"/work/demo/a.go"}}` + "\n" +
		`{"ts":"2026-01-01T10:00:04Z","session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"hi"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events", "s1.jsonl"), []byte(ev), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestToolAPI(t *testing.T) {
	dir := t.TempDir()
	toolSession(t, dir)
	src := view.NewSource(dir)
	src.OrcaOn = false
	srv := httptest.NewServer(NewServer(src, "127.0.0.1:4319").Wrap(http.NotFoundHandler()))
	defer srv.Close()
	get := func(path string) (int, ToolDetail, http.Header) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var d ToolDetail
		_ = json.NewDecoder(resp.Body).Decode(&d)
		return resp.StatusCode, d, resp.Header
	}

	code, d, h := get("/api/tool?session=s1&key=t1")
	if code != 200 || d.Tool != "Bash" || d.Input.Str("command") != "go test ./...\necho done" || d.Duration != 28 || d.OK == nil || !*d.OK ||
		d.Output == nil || d.Output.Stdout == nil || d.Output.Stdout.Head != "ok  pkg" || !d.Output.Interrupted || d.Summary != "go test ./... …" {
		t.Errorf("bash detail = %d %+v", code, d)
	}
	if h.Get("Cache-Control") != "no-store" {
		t.Errorf("cache-control = %q", h.Get("Cache-Control"))
	}
	// the Post key (t2) resolves to the same call
	if code, d, _ = get("/api/tool?session=s1&key=t2"); code != 200 || d.Input.Str("command") == "" {
		t.Errorf("post key = %d %+v", code, d)
	}
	code, d, _ = get("/api/tool?session=s1&key=t3")
	if code != 200 || d.OK == nil || *d.OK || d.Error != "no such file" || d.Summary != "a.go" {
		t.Errorf("failed read = %d %+v", code, d)
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/tool?session=s1&key=t99", 404},  // beyond the log
		{"/api/tool?session=s1&key=t0", 404},   // not a tool event
		{"/api/tool?session=s1&key=t4", 404},   // a prompt event
		{"/api/tool?session=nope&key=t1", 404}, // unknown session
		{"/api/tool?session=s1&key=p1", 400},   // wrong key shape
		{"/api/tool?session=s1&key=t01", 400},  // not the canonical key
		{"/api/tool?session=s1&key=", 400},
		{"/api/tool?session=../x&key=t1", 400},
	} {
		if code, _, _ := get(tc.path); code != tc.want {
			t.Errorf("%s = %d, want %d", tc.path, code, tc.want)
		}
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/tool?session=s1&key=t1", nil)
	req.Host = "evil.example"
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 403 {
		t.Errorf("foreign Host header must be refused: %v %v", resp, err)
	}
}
