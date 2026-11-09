package view

import "testing"

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 subagents", 1: "1 subagent", 3: "3 subagents"} {
		if got := Plural(n, "subagent"); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
}
