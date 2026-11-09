package workers

import (
	"errors"
	"testing"
)

func TestResolveOrca(t *testing.T) {
	home := "/home/u"
	user := home + "/.config/orca/cli/bin/orca"
	look := func(p string) func(string) (string, error) {
		return func(string) (string, error) {
			if p == "" {
				return "", errors.New("nf")
			}
			return p, nil
		}
	}
	cases := []struct {
		name   string
		env    map[string]string
		exists bool
		path   string
		want   string
		ok     bool
	}{
		{"ccwatch env wins", map[string]string{"CCWATCH_ORCA": "/a", "ORCA_CLI_COMMAND": "/b", "HOME": home}, true, "/usr/bin/x", "/a", true},
		{"orca cli env", map[string]string{"ORCA_CLI_COMMAND": "/b", "HOME": home}, true, "", "/b", true},
		{"user install beats PATH", map[string]string{"HOME": home}, true, "/usr/local/bin/orca", user, true},
		{"PATH when no install", map[string]string{"HOME": home}, false, "/usr/local/bin/orca", "/usr/local/bin/orca", true},
		{"screen reader rejected", map[string]string{"HOME": home}, false, "/usr/bin/orca", "", false},
		{"nothing", map[string]string{"HOME": home}, false, "", "", false},
	}
	for _, c := range cases {
		got, ok := resolveOrca(func(k string) string { return c.env[k] }, func(string) bool { return c.exists }, look(c.path))
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %q,%v want %q,%v", c.name, got, ok, c.want, c.ok)
		}
	}
}
