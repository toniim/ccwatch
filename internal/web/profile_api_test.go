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
