// Package pipeline_test drives the real hook converter and OTLP handler with the
// captured payloads in testdata/ and asserts on the reduced session.
package pipeline_test

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/hook"
	"github.com/toniim/ccwatch/internal/line"
	"github.com/toniim/ccwatch/internal/otlp"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/tui"
	"github.com/toniim/ccwatch/internal/workers"
)

const (
	sessionID = "10000002-0000-4000-8000-000000000002"
	fgAgent   = "aa1b2c3d4e5f607890"
	bgAgent   = "aa2b2c3d4e5f607890"
)

func lines(t *testing.T, name string) [][]byte {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		if b := bytes.TrimSpace(sc.Bytes()); len(b) > 0 {
			out = append(out, append([]byte(nil), b...))
		}
	}
	return out
}

// ingest replays both fixtures into a fresh state dir through the real code paths.
func ingest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 11, 9, 7, 53, 0, 0, time.UTC)
	for i, l := range lines(t, "hooks-demo-session.jsonl") {
		if err := hook.Run(bytes.NewReader(l), dir, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(otlp.Handler(dir))
	defer srv.Close()
	for _, l := range lines(t, "otlp-logs-demo-session.jsonl") {
		resp, err := http.Post(srv.URL+"/v1/logs", "application/json", bytes.NewReader(l))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("status %d", resp.StatusCode)
		}
	}
	return dir
}

func TestReducerFromFixtures(t *testing.T) {
	dir := ingest(t)
	s := state.Load(dir, sessionID)

	if len(s.Subagents) != 2 {
		t.Fatalf("want 2 subagents, got %d", len(s.Subagents))
	}
	byID := map[string]*state.Subagent{}
	for _, a := range s.Subagents {
		byID[a.ID] = a
		if a.Running {
			t.Errorf("agent %s still running", a.ID)
		}
	}
	fg, bg := byID[fgAgent], byID[bgAgent]
	if fg == nil || bg == nil {
		t.Fatalf("missing agents: %v", byID)
	}
	// Σ of the two api_request records of the foreground agent in otlp-logs-demo-session.jsonl:
	// (2+168+0+53393) + (2+5+53393+7611) = 53563 + 61011
	if fg.Tokens != 114574 {
		t.Errorf("fg tokens from otel = %d, want 114574", fg.Tokens)
	}
	if bg.Description != "Run echo beta" || !bg.Background {
		t.Errorf("bg agent = %+v", bg)
	}
	if fg.Description != "Run echo alpha" || fg.Background {
		t.Errorf("fg agent = %+v", fg)
	}
	if fg.Model != "claude-haiku-5-5" || fg.Type != "general-purpose" {
		t.Errorf("fg model/type = %q/%q", fg.Model, fg.Type)
	}
	if fg.Summary == "" {
		t.Error("fg summary empty")
	}
	if s.TestsPass != 0 || s.TestsFail != 0 {
		t.Errorf("tests = %d/%d, want 0/0", s.TestsPass, s.TestsFail)
	}
	if s.ToolFailures != 1 {
		t.Errorf("tool failures = %d, want 1", s.ToolFailures)
	}
	if !s.Ended {
		t.Error("session should be ended")
	}
	if s.Model != "claude-haiku-5-5" {
		t.Errorf("main model = %q", s.Model)
	}
	// last main api_request: 2 + 67632 + 108
	if s.MainCtx != 67742 {
		t.Errorf("main ctx = %d, want 67742", s.MainCtx)
	}
	if s.CostUSD <= 0 {
		t.Error("cost not summed")
	}
	if s.Processed != fg.Tokens+bg.Tokens {
		t.Errorf("processed = %d", s.Processed)
	}
	// peak = max over api_requests of input+cache_read+cache_creation:
	// fg max(2+0+53393, 2+53393+7611) = 61006; bg max(2+0+51600.., 2+51600+7626) = 59228
	if fg.Peak != 61006 || bg.Peak != 59228 || s.PeakCtx != 61006 {
		t.Errorf("peaks fg=%d bg=%d session=%d", fg.Peak, bg.Peak, s.PeakCtx)
	}
	// final messages are "alpha" (5) and "beta" (4) chars: (5+4)/4 = 2
	if fg.ReturnedChars != 5 || bg.ReturnedChars != 4 || s.ReturnedEst != 2 {
		t.Errorf("returned chars fg=%d bg=%d est=%d", fg.ReturnedChars, bg.ReturnedChars, s.ReturnedEst)
	}
}

func TestReducerFallbackTokensWithoutOtel(t *testing.T) {
	dir := ingest(t)
	s := state.Build(state.LoadEvents(dir, sessionID), nil)
	for _, a := range s.Subagents {
		if a.ID == fgAgent && (a.Tokens != 61011 || a.Peak != 61011) {
			t.Errorf("fallback tokens/peak = %d/%d, want hook totalTokens 61011", a.Tokens, a.Peak)
		}
	}
}

func TestServeFiltersAndNeverFails(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(otlp.Handler(dir))
	defer srv.Close()
	// every kept line is flattened; hook_* / plugin_* / etc. are dropped
	for _, l := range lines(t, "otlp-logs-demo-session.jsonl") {
		http.Post(srv.URL+"/v1/logs", "application/json", bytes.NewReader(l))
	}
	b, err := os.ReadFile(filepath.Join(dir, "otel", sessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(got) != 10 { // 8 api_request + 2 subagent_completed in the fixture
		t.Errorf("records kept = %d, want 10", len(got))
	}
	for _, l := range got {
		if !strings.Contains(l, `"event":"api_request"`) && !strings.Contains(l, `"event":"subagent_completed"`) {
			t.Errorf("unexpected record kept: %.80s", l)
		}
		for _, banned := range []string{`"user.`, `"organization.`, `"anthropic.`} {
			if strings.Contains(l, banned) {
				t.Errorf("identity attr %s kept", banned)
			}
		}
	}
	// garbage in, 200 {} out
	for _, path := range []string{"/v1/logs", "/v1/metrics", "/nope"} {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader("not json"))
		if err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || body.String() != "{}" {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body.String())
		}
	}
	metrics, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "otlp-metrics-sample.json"))
	resp, _ := http.Post(srv.URL+"/v1/metrics", "application/json", bytes.NewReader(metrics))
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("metrics status %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, "otel", "metrics.jsonl")); err == nil {
		t.Error("metrics must be discarded")
	}
}

func TestHookTruncatesAndSkipsGarbage(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 1000)
	payload := `{"session_id":"s1","hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore","last_assistant_message":"` + long + `"}`
	if err := hook.Run(strings.NewReader(payload), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	evs := state.LoadEvents(dir, "s1")
	if len(evs) != 1 || len(evs[0].LastAssistantMsg) != 300 || evs[0].TS == "" {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].LastAssistantChars != 1000 {
		t.Errorf("full length not recorded: %d", evs[0].LastAssistantChars)
	}
	// the reducer sizes the returned estimate from the full length, not the 300-char text
	if est := state.Build(evs, nil).ReturnedEst; est != 250 {
		t.Errorf("returned est = %d, want 250", est)
	}
	stop := `{"session_id":"s1","hook_event_name":"Stop","last_assistant_message":"héllo wörld"}`
	hook.Run(strings.NewReader(stop), dir, time.Now())
	if evs = state.LoadEvents(dir, "s1"); evs[len(evs)-1].LastAssistantChars != 11 {
		t.Errorf("Stop chars (runes) = %d, want 11", evs[len(evs)-1].LastAssistantChars)
	}
	for _, bad := range []string{"", "not json", `{"session_id":"../evil","hook_event_name":"Stop"}`, `{"hook_event_name":"Stop"}`} {
		if err := hook.Run(strings.NewReader(bad), dir, time.Now()); err != nil {
			t.Errorf("%q: %v", bad, err)
		}
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "events"))
	if len(ents) != 1 {
		t.Errorf("garbage created files: %v", ents)
	}
}

func TestTestDetection(t *testing.T) {
	yes := []string{"go test ./...", "cd x && go test -run A", "pytest -q", "npm test", "npm run test", "npx vitest run", "npx playwright test", "cargo test"}
	no := []string{"echo ok", "false", "go build ./...", "echo go testing"}
	for _, c := range yes {
		if !state.IsTestCommand(c) {
			t.Errorf("%q should be a test command", c)
		}
	}
	for _, c := range no {
		if state.IsTestCommand(c) {
			t.Errorf("%q should not be a test command", c)
		}
	}
	evs := []state.Event{
		{SessionID: "s", Name: "PostToolUse", ToolName: "Bash", ToolInput: state.ToolInput{"command": "go test ./..."}},
		{SessionID: "s", Name: "PostToolUseFailure", ToolName: "Bash", ToolInput: state.ToolInput{"command": "pytest"}},
	}
	s := state.Build(evs, nil)
	if s.TestsPass != 1 || s.TestsFail != 1 || s.ToolFailures != 1 {
		t.Errorf("tests = %d/%d failures %d", s.TestsPass, s.TestsFail, s.ToolFailures)
	}
}

func TestLineAndTUIRender(t *testing.T) {
	dir := ingest(t)
	in := `{"session_id":"` + sessionID + `","model":{"display_name":"Haiku 5.5"},"context_window":{"used_percentage":31,"total_input_tokens":61000}}`
	got := line.Run(strings.NewReader(in), dir)
	want := "Haiku 5.5 | idle | agents 0 live | workers - | ctx 61k 31% | tests 0/0"
	if got != want {
		t.Errorf("line = %q\nwant   %q", got, want)
	}
	// missing state still prints model + ctx
	got = line.Run(strings.NewReader(`{"session_id":"nope","model":{"display_name":"M"},"context_window":{"used_percentage":5,"total_input_tokens":9000}}`), dir)
	if !strings.HasPrefix(got, "M | idle | agents 0 live") || !strings.Contains(got, "ctx 9k 5%") {
		t.Errorf("degraded line = %q", got)
	}
	if got := line.Run(strings.NewReader("garbage"), dir); !strings.HasPrefix(got, "? | idle") {
		t.Errorf("garbage line = %q", got)
	}

	frame := tui.Table(state.Load(dir, sessionID), nil, false, false, 120, 1, 1)
	for _, want := range []string{"Subagents (2, 0 live)", "Run echo alpha", "Run echo beta (bg)", fgAgent[:8], "subagents processed", "peak ctx 61k", "PEAK", "tool failures 1"} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame missing %q:\n%s", want, frame)
		}
	}
}

func TestLineLatency(t *testing.T) {
	dir := ingest(t)
	in := `{"session_id":"` + sessionID + `","model":{"display_name":"H"}}`
	start := time.Now()
	for i := 0; i < 20; i++ {
		line.Run(strings.NewReader(in), dir)
	}
	if per := time.Since(start) / 20; per > 50*time.Millisecond {
		t.Errorf("line in-process %v > 50ms", per)
	}
}

func TestWorkersDefaultFilter(t *testing.T) {
	const term = "term_aaaaaaaa-0000-4000-8000-000000000001"
	ws := []workers.Worker{
		{DispatchID: "live", State: "ready", Liveness: "live", Workspace: "id::/w/other"},
		{DispatchID: "done", State: "succeeded", Liveness: "exited", Workspace: "id::/w/old"},
		{DispatchID: "unv-done", State: "succeeded", Liveness: "unverifiable", Workspace: "id::/w/old"},
		{DispatchID: "unv-open", State: "ready", Liveness: "unverifiable", Workspace: "id::/w/x"},
		{DispatchID: "mine", State: "stopped", Liveness: "exited", Terminal: term, Workspace: "id::/w/this"},
	}
	var got []string
	for _, w := range workers.Shown(ws, term) {
		got = append(got, w.DispatchID)
	}
	if strings.Join(got, ",") != "live,unv-open,mine" {
		t.Errorf("shown = %v", got)
	}
	s := state.Session{OrcaTerminal: term}
	if l := line.Render(line.Input{}, s, ws, true); !strings.Contains(l, "workers 1/3") {
		t.Errorf("line = %q, want workers 1/3 (live/shown)", l)
	}
	dir := ingest(t)
	if f := tui.Table(state.Load(dir, sessionID), ws, true, false, 120, 1, 1); !strings.Contains(f, "Orca workers (2 shown of 5)") || strings.Contains(f, "old") {
		t.Errorf("default frame:\n%s", f)
	}
	if f := tui.Table(state.Load(dir, sessionID), ws, true, true, 120, 1, 1); !strings.Contains(f, "Orca workers (5 shown of 5)") {
		t.Errorf("--all-workers frame:\n%s", f)
	}
}
