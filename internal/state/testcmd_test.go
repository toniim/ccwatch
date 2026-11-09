package state

import (
	"strings"
	"testing"
	"time"
)

const orcaDispatch = `/usr/local/bin/orca orchestration worker-start --from term_x --spec "Fix the detector; keep go vet + go test green && pytest passes" --worktree current --json`

func TestTestCommandSimpleCommands(t *testing.T) {
	yes := map[string]string{
		"go test ./...":                          "go test ./...",
		"cd x && go test -run A":                 "go test -run A",
		"FOO=bar GOFLAGS=-v go test ./...":       "FOO=bar GOFLAGS=-v go test ./...",
		"timeout 60 go test ./...":               "timeout 60 go test ./...",
		"sudo env X=1 time go -C sub test ./...": "sudo env X=1 time go -C sub test ./...",
		"echo hi\npytest -q":                     "pytest -q",
		"python3 -m pytest x":                    "python3 -m pytest x",
		"npm run test":                           "npm run test",
		"npx playwright test":                    "npx playwright test",
		"make test; echo done":                   "make test",
		"cargo test | tail -5":                   "cargo test",
		`echo "a;b" && go test`:                  "go test",
	}
	for in, want := range yes {
		got, ok := TestCommand(in)
		if !ok || got != want {
			t.Errorf("%q: got %q,%v want %q", in, got, ok, want)
		}
	}
	no := []string{
		orcaDispatch,
		`echo "go test"`,
		`echo 'a && go test'`,
		`echo go\ test`,
		"grep 'go test' x.md",
		"git commit -m 'run go test'",
		"go build ./...",
		"echo go testing",
	}
	for _, c := range no {
		if got, ok := TestCommand(c); ok {
			t.Errorf("%q must not match, got %q", c, got)
		}
	}
}

func TestTestCommandTruncatesTo120(t *testing.T) {
	got, ok := TestCommand("go test " + strings.Repeat("./pkg ", 60))
	if !ok || len([]rune(got)) != 120 {
		t.Fatalf("len=%d ok=%v", len([]rune(got)), ok)
	}
}

func TestBuildIgnoresQuotedRunnerInOrcaDispatch(t *testing.T) {
	evs := []Event{
		{SessionID: "s", Name: "PostToolUse", ToolName: "Bash", ToolInput: &ToolInput{Command: orcaDispatch}},
		{SessionID: "s", Name: "PostToolUse", ToolName: "Bash", ToolInput: &ToolInput{Command: "cd x && go test ./..."}},
	}
	s := Build(evs, nil)
	if s.TestsPass != 1 || s.TestsFail != 0 {
		t.Fatalf("tests = %d/%d, want 1/0", s.TestsPass, s.TestsFail)
	}
	var rows []LogEntry
	for _, l := range s.Log {
		if l.Kind == "test" {
			rows = append(rows, l)
		}
	}
	if len(rows) != 1 || rows[0].Text != "go test ./..." {
		t.Fatalf("rows = %+v", rows)
	}
	_ = time.Now
}
