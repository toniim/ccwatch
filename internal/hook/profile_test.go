package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/state"
)

func TestProfileDerivation(t *testing.T) {
	const home = "/home/u"
	tests := []struct {
		name, dir, wantName, wantRel string
	}{
		{"unset", "", "", ""},
		{"default dir", home + "/.claude", "", ""},
		{"default dir, trailing slash", home + "/.claude/", "", ""},
		{"launcher home dir", home + "/.config/claude-work/home", "work", ".config/claude-work/home"},
		{"dotted claude-work", home + "/.claude-work", "work", ".claude-work"},
		{"outside home", "/opt/x", "x", ""},
		{"outside home, home element", "/opt/y/home", "y", ""},
		{"escapes home with ..", home + "/../etc/z", "z", ""},
		{"unsafe characters", home + "/.config/a b$c", "a-b-c", ".config/a b$c"},
	}
	for _, tc := range tests {
		n, r := Profile(tc.dir, home)
		if n != tc.wantName || r != tc.wantRel {
			t.Errorf("%s: Profile(%q) = (%q, %q), want (%q, %q)", tc.name, tc.dir, n, r, tc.wantName, tc.wantRel)
		}
	}
}

func TestRunRecordsOnlyDerivedProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".config", "claude-work", "home"))
	dir := t.TempDir()
	payload := `{"session_id":"s1","hook_event_name":"SessionStart"}`
	if err := Run(bytes.NewBufferString(payload), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "events", "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var e state.Event
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if e.Profile != "work" || e.ConfigDir != ".config/claude-work/home" {
		t.Errorf("event = profile %q config_dir %q", e.Profile, e.ConfigDir)
	}
	if bytes.Contains(b, []byte(home)) {
		t.Errorf("stored line contains the home path: %s", b)
	}
}

func TestRunDefaultProfileStoresNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	dir := t.TempDir()
	if err := Run(bytes.NewBufferString(`{"session_id":"s1","hook_event_name":"SessionStart"}`), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "events", "s1.jsonl"))
	if bytes.Contains(b, []byte(`"profile"`)) || bytes.Contains(b, []byte(`"config_dir"`)) {
		t.Errorf("default profile stored a tag: %s", b)
	}
}
