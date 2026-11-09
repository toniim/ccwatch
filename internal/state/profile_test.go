package state

import "testing"

func TestValidConfigDir(t *testing.T) {
	for in, want := range map[string]bool{
		".config/claude-vb/home": true,
		".claude-work":           true,
		"":                       false,
		".":                      false,
		"/etc":                   false,
		"/home/u/.claude-work":   false,
		"../x":                   false,
		"a/../../x":              false,
		"a/..":                   false,
		"a//b":                   false,
	} {
		if got := ValidConfigDir(in); got != want {
			t.Errorf("ValidConfigDir(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestReduceProfileFirstNonEmpty(t *testing.T) {
	s := Build([]Event{
		{TS: "2026-01-01T10:00:00Z", SessionID: "s1", Name: "SessionStart"},
		{TS: "2026-01-01T10:00:01Z", SessionID: "s1", Name: "UserPromptSubmit", Profile: "vb", ConfigDir: "../evil"},
		{TS: "2026-01-01T10:00:02Z", SessionID: "s1", Name: "Stop", Profile: "other", ConfigDir: ".config/claude-vb/home"},
	}, nil)
	if s.Profile != "vb" {
		t.Errorf("Profile = %q, want vb", s.Profile)
	}
	if s.ConfigDir != ".config/claude-vb/home" {
		t.Errorf("ConfigDir = %q: an invalid value must be skipped", s.ConfigDir)
	}
	if d := Build([]Event{{TS: "2026-01-01T10:00:00Z", SessionID: "s2", Name: "SessionStart"}}, nil); d.Profile != "" || d.ConfigDir != "" {
		t.Errorf("default profile: %q %q", d.Profile, d.ConfigDir)
	}
}
