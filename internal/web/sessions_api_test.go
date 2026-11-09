package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/view"
)

func writeSession(t *testing.T, dir, id, term string, interactive bool, runs []string) {
	t.Helper()
	for _, sub := range []string{"events", "otel"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ev := fmt.Sprintf(`{"ts":"2026-01-01T10:00:00Z","session_id":%q,"cwd":"/work/demo","hook_event_name":"SessionStart","orca_terminal":%q}`+"\n", id, term)
	if len(runs) > 0 {
		ev += fmt.Sprintf(`{"ts":"2026-01-01T10:00:01Z","session_id":%q,"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"orca orchestration run-list"},"orca_runs":[%q]}`+"\n", id, runs[0])
	}
	if err := os.WriteFile(filepath.Join(dir, "events", id+".jsonl"), []byte(ev), 0o644); err != nil {
		t.Fatal(err)
	}
	qs := "sdk"
	if interactive {
		qs = "repl_main_thread"
	}
	ot := fmt.Sprintf(`{"event":"api_request","attrs":{"session.id":%q,"model":"model-a","query_source":%q}}`+"\n", id, qs)
	if err := os.WriteFile(filepath.Join(dir, "otel", id+".jsonl"), []byte(ot), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionsTreeFieldsAndLatency(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "coord0001", "term_c", true, []string{"run_000000000001"})
	writeSession(t, dir, "main0001", "term_m", true, nil)
	writeSession(t, dir, "main0001-sdk", "term_m", false, nil)
	writeSession(t, dir, "solo0001", "", true, nil)
	for i := 0; i < 46; i++ { // 50 sessions in all
		writeSession(t, dir, fmt.Sprintf("filler%02d", i), "", true, nil)
	}
	src := view.NewSource(dir)
	src.OrcaOn = false
	srv := httptest.NewServer(NewServer(src, "127.0.0.1:4319").Wrap(http.NotFoundHandler()))
	defer srv.Close()

	var list []SessionInfo
	var took time.Duration
	for i := 0; i < 3; i++ { // best of three, the first call warms the file cache
		start := time.Now()
		resp, err := http.Get(srv.URL + "/api/sessions")
		if err != nil {
			t.Fatal(err)
		}
		list = nil
		if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if d := time.Since(start); i == 0 || d < took {
			took = d
		}
	}
	if len(list) != 50 {
		t.Fatalf("got %d sessions", len(list))
	}
	t.Logf("/api/sessions, 50 sessions: %v", took)
	if took > 50*time.Millisecond {
		t.Errorf("/api/sessions for 50 sessions took %v, want < 50ms", took)
	}
	by := map[string]SessionInfo{}
	for _, s := range list {
		by[s.ID] = s
	}
	if c := by["coord0001"]; c.Role != "coordinator" || c.Parent != "" || c.Run == nil || c.Run.ID != "run_000000000001" {
		t.Errorf("coordinator: %+v run=%v", c, c.Run)
	}
	if n := by["main0001-sdk"]; n.Role != "nested" || n.Parent != "main0001" || n.Run != nil {
		t.Errorf("nested: %+v", n)
	}
	if s := by["solo0001"]; s.Role != "standalone" || s.Parent != "" || s.Run != nil {
		t.Errorf("standalone: %+v", s)
	}
	if m := by["main0001"]; m.Role != "standalone" {
		t.Errorf("owner of a shared terminal: %+v", m)
	}
}
