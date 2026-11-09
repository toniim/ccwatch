package web

import (
	"testing"

	"github.com/toniim/ccwatch/internal/state"
)

func TestSessionLabel(t *testing.T) {
	agents := func(running ...bool) []*state.Subagent {
		var out []*state.Subagent
		for _, r := range running {
			out = append(out, &state.Subagent{Running: r})
		}
		return out
	}
	tests := []struct {
		name      string
		ss        state.Session
		wantState string
		want      string
	}{
		{"ended with one done agent", state.Session{Ended: true, Model: "model-a", Cwd: "/work/demo", Subagents: agents(false)},
			"ended", "aaaaaaaa · ended · 1 agent done · model-a · demo"},
		{"live agents", state.Session{Model: "model-b", Cwd: "/work/demo/", Subagents: agents(true, false, true)},
			"running", "aaaaaaaa · running · 2 of 3 agents running · model-b · demo"},
		{"one of one running", state.Session{Model: "model-b", Cwd: "/work/demo", Subagents: agents(true)},
			"running", "aaaaaaaa · running · 1 of 1 agent running · model-b · demo"},
		{"no agents, open", state.Session{Model: "model-b", Cwd: "/work/demo"},
			"active", "aaaaaaaa · active · no agents · model-b · demo"},
		{"done agents, not ended", state.Session{Model: "model-b", Cwd: "/work/demo", Subagents: agents(false, false, false)},
			"active", "aaaaaaaa · active · 3 agents done · model-b · demo"},
		{"profile tag before the dir", state.Session{Model: "model-b", Cwd: "/work/demo", Profile: "vb"},
			"active", "aaaaaaaa · active · no agents · model-b · vb · demo"},
		{"unknown model and dir", state.Session{Ended: true},
			"ended", "aaaaaaaa · ended · no agents · ?"},
	}
	for _, tc := range tests {
		st, got := sessionLabel("aaaaaaaa-0000-4000-8000-000000000000", tc.ss)
		if st != tc.wantState || got != tc.want {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.name, st, got, tc.wantState, tc.want)
		}
	}
}
