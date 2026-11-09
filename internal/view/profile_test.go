package view

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/toniim/ccwatch/internal/state"
)

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentsPanelReadsProfileConfigDir(t *testing.T) {
	user := t.TempDir()
	def := t.TempDir()
	hooks := func(event string) string {
		return `{"hooks":{"` + event + `":[{"hooks":[{"type":"command","command":"x"}]}]}}`
	}
	writeFile(t, filepath.Join(def, "settings.json"), hooks("Stop"))
	writeFile(t, filepath.Join(user, ".config/claude-work/home/settings.json"), hooks("PreCompact"))
	writeFile(t, filepath.Join(user, ".config/claude-work/home/agents/workagent.md"), "---\nname: workagent\nmodel: sonnet\n---\n")

	src := NewSource(t.TempDir())
	src.OrcaOn, src.ClaudeHome, src.UserHome = false, def, user

	tagged := state.Session{ID: "a", Profile: "work", ConfigDir: ".config/claude-work/home"}
	ag, hk := src.slow(src.homeOf(tagged), "")
	if _, ok := ag["workagent"]; !ok {
		t.Errorf("profile agents not read: %v", ag)
	}
	if !hk.Found || hk.Counts["PreCompact"] != 1 || hk.Counts["Stop"] != 0 {
		t.Fatalf("profile settings.json not read: %+v", hk)
	}
	plain := state.Session{ID: "b"}
	ag2, hk2 := src.slow(src.homeOf(plain), "")
	if _, ok := ag2["workagent"]; ok {
		t.Errorf("default session leaked the profile's agents")
	}
	if hk2.Counts["Stop"] != 1 || hk2.Counts["PreCompact"] != 0 {
		t.Errorf("default session hooks = %v", hk2.Counts)
	}

	for _, bad := range []string{"../escape", "/etc", "a/../../b"} {
		if h := src.homeOf(state.Session{Profile: "x", ConfigDir: bad}); h != def {
			t.Errorf("config_dir %q resolved to %q, want the default home", bad, h)
		}
	}
	if h := src.homeOf(state.Session{ConfigDir: ".config/claude-work/home"}); h != def {
		t.Errorf("a session without a profile must use the default home, got %q", h)
	}
}
