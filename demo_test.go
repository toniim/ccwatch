package main

import (
	"encoding/json"
	"testing"
)

func TestDemoSettingsRegisterEveryToolEvent(t *testing.T) {
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(demoSettings("/bin/ccwatch")), &s); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure"} {
		g := s.Hooks[ev]
		if len(g) != 1 || g[0].Matcher != "*" || len(g[0].Hooks) != 1 || !g[0].Hooks[0].Async || g[0].Hooks[0].Command != "/bin/ccwatch hook" {
			t.Errorf("%s = %+v", ev, g)
		}
	}
}
