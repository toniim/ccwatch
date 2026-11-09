package view

import "testing"

func TestShortModel(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5":              "opus-5-5",
		"claude-haiku-5-5":             "haiku-5-5",
		"claude-sonnet-5-5":            "sonnet-5-5",
		"claude-haiku-5-5-20251001":    "haiku-5-5",
		"claude-opus-5-5[1m]":          "opus-5-5[1m]",
		"claude-opus-5-5-20251001[1m]": "opus-5-5[1m]",
		"haiku":                        "haiku",
		"opus-5-5":                     "opus-5-5",
		"?":                            "?",
		"":                             "",
	} {
		if got := ShortModel(in); got != want {
			t.Errorf("ShortModel(%q) = %q, want %q", in, got, want)
		}
	}
}
