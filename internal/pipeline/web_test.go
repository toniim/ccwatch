package pipeline_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/hook"
	"github.com/toniim/ccwatch/internal/otlp"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/view"
	"github.com/toniim/ccwatch/internal/web"
)

// webServer starts the combined OTLP + web handler on a state dir holding the
// hooks fixture session (newer) and the realistic session (older).
func webServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := ingest(t)
	for _, sub := range []string{"events", "otel"} {
		os.MkdirAll(filepath.Join(dir, sub), 0o755)
		b, err := os.ReadFile(td("realistic", sub, realisticID+".jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, sub, realisticID+".jsonl")
		os.WriteFile(p, b, 0o644)
		os.Chtimes(p, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	}
	src := view.NewSource(dir)
	src.ClaudeHome, src.OrcaOn = td("claude"), false
	h := web.NewServer(src, "127.0.0.1:4319").Wrap(otlp.Handler(dir))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, dir
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func TestWebAPIs(t *testing.T) {
	srv, _ := webServer(t)

	var list []web.SessionInfo
	if code := getJSON(t, srv.URL+"/api/sessions", &list); code != 200 || len(list) != 2 {
		t.Fatalf("sessions: %d %+v", code, list)
	}
	if list[0].ID != sessionID || list[1].ID != realisticID || list[1].Subagents != 3 || !list[1].Ended || list[1].Model != "sonnet-5-5" ||
		list[1].State != "ended" || !strings.Contains(list[1].Label, " · ended · 3 agents done · sonnet-5-5") {
		t.Errorf("sessions order/content: %+v", list)
	}

	var d view.Dashboard
	if code := getJSON(t, srv.URL+"/api/state?session="+realisticID, &d); code != 200 {
		t.Fatalf("state: %d", code)
	}
	if d.SessionID != realisticID || len(d.Lanes) != 1 || d.Lanes[0].Name != "Explore" || d.Lanes[0].Total != 3 || d.Lanes[0].Pct != 100 ||
		len(d.Plan) != 1 || d.Plan[0].Detail != "Explore x3" || len(d.Agents) != 1 || d.Agents[0].File != "explore.md" || d.Back.Processed <= d.Back.Received || len(d.Log) != 11 || len(d.Table) != 3 {
		t.Errorf("state payload: %+v", d)
	}
	// the API serves exactly what the TUI model builds
	if want := view.Build(state.Load(td("realistic"), realisticID), view.Ext{Agents: fixtureExt(t).Agents, Hooks: fixtureExt(t).Hooks}); want.Lanes[0].Pct != d.Lanes[0].Pct || want.Ctx != d.Ctx {
		t.Errorf("api differs from view.Build: ctx %d vs %d", d.Ctx, want.Ctx)
	}

	var latest view.Dashboard
	getJSON(t, srv.URL+"/api/state", &latest)
	if latest.SessionID != sessionID {
		t.Errorf("default session = %s, want newest %s", latest.SessionID, sessionID)
	}
	var e map[string]string
	if code := getJSON(t, srv.URL+"/api/state?session=..%2Fevil", &e); code != 400 || e["error"] == "" {
		t.Errorf("bad id: %d %v", code, e)
	}
	if code := getJSON(t, srv.URL+"/api/state?session=nope", &e); code != 404 {
		t.Errorf("unknown id: %d", code)
	}
}

func TestWebPageAndHostGuard(t *testing.T) {
	srv, _ := webServer(t)
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || len(body) < 100 {
			t.Errorf("%s: %d, %d bytes", path, resp.StatusCode, len(body))
		}
		if path == "/" {
			for _, id := range []string{`id="tree"`, `id="side-toggle"`, `id="follow"`, `id="v-dash"`, `id="v-table"`, `id="lanes"`, `id="log"`} {
				if !strings.Contains(string(body), id) {
					t.Errorf("page lacks %s", id)
				}
			}
			page := string(body)
			if strings.Contains(page, "<select") {
				t.Error("the session picker <select> is gone; the sidebar tree replaces it")
			}
			for _, tag := range []string{
				`<link rel="preconnect" href="https://fonts.googleapis.com">`,
				`<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>`,
				`<link href="https://fonts.googleapis.com/css2?family=Iosevka+Charon+Mono:ital,wght@0,300;0,400;0,500;0,700;1,300;1,400;1,500;1,700&display=swap" rel="stylesheet">`,
			} {
				if !strings.Contains(page, tag) {
					t.Errorf("page lacks font tag %s", tag)
				}
			}
			if strings.Index(page, "fonts.googleapis.com/css2") > strings.Index(page, `href="/style.css"`) {
				t.Error("font stylesheet must come before style.css")
			}
			// only the Google Fonts hosts may appear as external references
			for _, m := range regexp.MustCompile(`https?://[^\s"'/]+`).FindAllString(page, -1) {
				if m != "https://fonts.googleapis.com" && m != "https://fonts.gstatic.com" {
					t.Errorf("page references external host %s", m)
				}
			}
		}
	}
	// DNS-rebinding guard: foreign Host header is refused for the UI and API ...
	for _, path := range []string{"/", "/api/state", "/api/sessions"} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Host = "evil.example"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s with foreign Host: %d, want 403", path, resp.StatusCode)
		}
	}
	// ... but OTLP keeps working whatever the Host says
	req, _ := http.NewRequest("POST", srv.URL+"/v1/logs", bytes.NewReader(lines(t, "otlp-logs-demo-session.jsonl")[3]))
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("otlp through combined handler: %v %v", resp, err)
	}
	resp.Body.Close()
	resp, _ = http.Post(srv.URL+"/nope", "application/json", strings.NewReader("x"))
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "{}" {
		t.Errorf("POST /nope: %d %q", resp.StatusCode, b)
	}
}

type sseEvent struct{ name, data string }

func readSSE(ctx context.Context, url string) (<-chan sseEvent, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan sseEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		var name string
		for sc.Scan() {
			l := sc.Text()
			switch {
			case strings.HasPrefix(l, "event: "):
				name = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				ch <- sseEvent{name, strings.TrimPrefix(l, "data: ")}
			}
		}
	}()
	return ch, nil
}

func waitState(t *testing.T, ch <-chan sseEvent, pred func(view.Dashboard) bool) view.Dashboard {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("stream closed")
			}
			if ev.name != "state" {
				continue
			}
			var d view.Dashboard
			if err := json.Unmarshal([]byte(ev.data), &d); err != nil {
				t.Fatal(err)
			}
			if pred(d) {
				return d
			}
		case <-timeout:
			t.Fatal("no matching state event within 5 s")
		}
	}
}

func TestWebStreamPushesChanges(t *testing.T) {
	srv, dir := webServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := readSSE(ctx, srv.URL+"/api/stream?session=latest")
	if err != nil {
		t.Fatal(err)
	}
	first := waitState(t, ch, func(d view.Dashboard) bool { return d.SessionID == sessionID })
	if len(first.Lanes) != 1 || len(first.Lanes[0].Slots) != 2 || first.LiveAgents != 0 {
		t.Fatalf("first state: %+v", first.Lanes)
	}
	// append a new subagent through the real hook while the stream is open
	resume := `{"session_id":"` + sessionID + `","hook_event_name":"SessionStart","source":"resume"}`
	if err := hook.Run(strings.NewReader(resume), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"` + sessionID + `","hook_event_name":"SubagentStart","agent_id":"afeed0000live","agent_type":"general-purpose","prompt_id":"live"}`
	if err := hook.Run(strings.NewReader(payload), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := waitState(t, ch, func(d view.Dashboard) bool { return len(d.Lanes) == 1 && len(d.Lanes[0].Slots) == 3 })
	t.Logf("append -> pushed state in %v", time.Since(start))
	if got.Lanes[0].Slots[2] != "launched" || got.Lanes[0].Status != "1 queued" || got.LiveAgents != 1 || got.State != "orchestrating" {
		t.Errorf("pushed state: lanes=%+v live=%d state=%s", got.Lanes, got.LiveAgents, got.State)
	}
}

func TestWebStreamSessionsEvent(t *testing.T) {
	srv, _ := webServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := readSSE(ctx, srv.URL+"/api/stream?session="+realisticID)
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	var gotState, gotList bool
	for !(gotState && gotList) {
		select {
		case ev := <-ch:
			if ev.name == "sessions" {
				var l []web.SessionInfo
				if json.Unmarshal([]byte(ev.data), &l) != nil || len(l) != 2 {
					t.Fatalf("sessions event: %s", ev.data)
				}
				gotList = true
			}
			if ev.name == "state" {
				var d view.Dashboard
				json.Unmarshal([]byte(ev.data), &d)
				if d.SessionID != realisticID {
					t.Fatalf("pinned stream sent %s", d.SessionID)
				}
				gotState = true
			}
		case <-timeout:
			t.Fatalf("state=%v list=%v", gotState, gotList)
		}
	}
}

// A session with no subagents must still serialise every list as [] (the page
// iterates them; null broke the render once).
func TestWebStateListsAreNeverNull(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "events"), 0o755)
	hook.Run(strings.NewReader(`{"session_id":"empty1","hook_event_name":"SessionStart","source":"startup"}`), dir, time.Now())
	src := view.NewSource(dir)
	src.ClaudeHome, src.OrcaOn = td("claude"), false
	srv := httptest.NewServer(web.NewServer(src, "127.0.0.1:4319").Wrap(otlp.Handler(dir)))
	defer srv.Close()
	var raw map[string]json.RawMessage
	if code := getJSON(t, srv.URL+"/api/state?session=empty1", &raw); code != 200 {
		t.Fatalf("state: %d", code)
	}
	for _, k := range []string{"agents", "plan", "lanes", "log", "table"} {
		if string(raw[k]) != "[]" {
			t.Errorf("%s = %s, want []", k, raw[k])
		}
	}
}
