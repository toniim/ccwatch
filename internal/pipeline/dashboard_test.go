package pipeline_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/agents"
	"github.com/toniim/ccwatch/internal/hook"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/tui"
	"github.com/toniim/ccwatch/internal/view"
	"github.com/toniim/ccwatch/internal/workers"
)

var update = flag.Bool("update", false, "rewrite golden frames")

const realisticID = "10000001-0000-4000-8000-000000000001"

func td(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "testdata"}, parts...)...)
}

func fixtureExt(t *testing.T) view.Ext {
	t.Helper()
	return view.Ext{
		Agents: agents.LoadDirs(td("claude", "agents")),
		Hooks:  agents.LoadHooks(td("claude", "settings.json")),
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := td("golden", name+".txt")
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/pipeline -update)", err)
	}
	if string(want) != got {
		t.Errorf("golden %s differs (run with -update to accept):\n--- got ---\n%s", name, got)
	}
}

func frame(d view.Dashboard, width int) string {
	return tui.Dashboard(d, tui.Options{Width: width, Loc: time.UTC})
}

func TestGoldenFrames(t *testing.T) {
	ext := fixtureExt(t)
	hooksDir := ingest(t)
	hooksD := view.Build(state.Load(hooksDir, sessionID), ext)
	realD := view.Build(state.Load(td("realistic"), realisticID), ext)

	for _, w := range []int{120, 80} {
		golden(t, "hooks-"+itoa(w), frame(hooksD, w))
		golden(t, "realistic-"+itoa(w), frame(realD, w))
	}

	// Orca: coordinator (Run + orca lane), worker (one-line link), nested (ignored)
	od, all := orcaFixture(t)
	for _, c := range []struct {
		name, id string
		width    int
	}{{"orca-coordinator-120", orcaCoord, 120}, {"orca-coordinator-80", orcaCoord, 80}, {"orca-worker-120", orcaWorker, 120}, {"orca-nested-80", orcaNested, 80}} {
		s := sessionByID(t, all, c.id)
		e := view.Link(s, all, od)
		e.Agents, e.Hooks = ext.Agents, ext.Hooks
		golden(t, c.name, frame(view.Build(s, e), c.width))
	}
}

const (
	orcaCoord  = "c0000000-0000-4000-8000-00000000c001"
	orcaWorker = "a1000000-0000-4000-8000-00000000a001"
	orcaNested = "d4e5f600-0000-4000-8000-00000000a002"
	orcaRun    = "run_000000000001"
)

func sessionByID(t *testing.T, all []state.Session, id string) state.Session {
	t.Helper()
	for _, s := range all {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %s not in fixture", id)
	return state.Session{}
}

// orcaFixture loads testdata/orca/state plus the canned Orca CLI answers.
func orcaFixture(t *testing.T) (view.OrcaData, []state.Session) {
	t.Helper()
	read := func(n string) []byte {
		b, err := os.ReadFile(td("orca", n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	ws, err := workers.Parse(read("worker-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	run, err := workers.ParseRun(orcaRun, read("task-list.json"), read("run-list.json"), ws)
	if err != nil {
		t.Fatal(err)
	}
	var show struct {
		Result struct {
			RunID string `json:"runId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(read("worker-show-ctx_aaa111.json"), &show); err != nil {
		t.Fatal(err)
	}
	od := view.OrcaData{Workers: ws, Runs: map[string]*workers.Run{orcaRun: run}, DispatchRun: map[string]string{"ctx_aaa111": show.Result.RunID}}
	var all []state.Session
	for _, id := range state.Sessions(td("orca", "state")) {
		all = append(all, state.Load(td("orca", "state"), id))
	}
	if len(all) != 3 {
		t.Fatalf("fixture sessions = %d", len(all))
	}
	return od, all
}

func TestOrcaJoinByTerminal(t *testing.T) {
	od, all := orcaFixture(t)
	build := func(id string) view.Dashboard {
		s := sessionByID(t, all, id)
		return view.Build(s, view.Link(s, all, od))
	}
	coord, worker, nested := build(orcaCoord), build(orcaWorker), build(orcaNested)

	// the coordinator owns the run id from its own Bash stdout (cwd plays no part)
	if s := sessionByID(t, all, orcaCoord); len(s.RunIDs) != 1 || s.RunIDs[0] != orcaRun {
		t.Fatalf("coordinator run ids = %v", s.RunIDs)
	}
	if coord.Run == nil || coord.PlanSource != "orca run" || len(coord.Plan) != 3 || coord.Plan[0].Detail != "worker explore-bug" || coord.Plan[1].Mark != "|" {
		t.Errorf("coordinator run/plan: %+v %+v", coord.Run, coord.Plan)
	}
	// Task tool lane (its own Explore subagent) and orca lane are separate
	if len(coord.Lanes) != 1 || coord.Lanes[0].Name != "Explore" {
		t.Errorf("task lanes = %+v", coord.Lanes)
	}
	l := coord.OrcaLane
	if l == nil || l.Name != "orca workers" || l.Total != 3 || l.Done != 1 || l.Status != "1 running" {
		t.Fatalf("orca lane = %+v", l)
	}
	// model = the worker session's model; the nested haiku session on the same terminal is ignored
	if l.Model != "claude-sonnet-5-5" {
		t.Errorf("orca lane model = %q, want claude-sonnet-5-5 (worker session, not nested haiku)", l.Model)
	}
	if coord.OrcaHeader != "» Orca Run · 3 workers · each in its own session «" {
		t.Errorf("header = %q", coord.OrcaHeader)
	}
	if coord.WorkerLine != "" || coord.NestedLine != "" {
		t.Errorf("coordinator must not look like a worker: %q %q", coord.WorkerLine, coord.NestedLine)
	}

	// the worker session: one line, no Run panel, no orca lane
	if worker.WorkerLine != "worker of "+orcaRun+" · task Map the token refresh bug" {
		t.Errorf("worker line = %q", worker.WorkerLine)
	}
	if worker.Run != nil || worker.OrcaLane != nil || worker.PlanSource == "orca run" || worker.NestedLine != "" {
		t.Errorf("worker shows run panel: %+v", worker)
	}
	// the nested session joins nothing
	if nested.WorkerLine != "" || nested.Run != nil || nested.OrcaLane != nil || !strings.Contains(nested.NestedLine, "a1000000") {
		t.Errorf("nested = worker %q run %v nested %q", nested.WorkerLine, nested.Run, nested.NestedLine)
	}
	// a session with no terminal and no run ids is untouched
	plain := view.Build(state.Session{ID: "p"}, view.Link(state.Session{ID: "p"}, all, od))
	if plain.Run != nil || plain.OrcaLane != nil || plain.WorkerLine != "" || plain.NestedLine != "" {
		t.Errorf("plain session joined: %+v", plain)
	}
	// the earliest SessionStart wins even when the nested session sorts first
	swapped := append([]state.Session(nil), all...)
	for i := range swapped {
		if swapped[i].ID == orcaNested {
			swapped[i].ID = "0-nested" // sorts before the worker id
		}
	}
	s := sessionByID(t, swapped, orcaWorker)
	if view.Link(s, swapped, od).WorkerLine == "" {
		t.Error("worker lost its link when a later session sorts first by id")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestDashboardModel(t *testing.T) {
	ext := fixtureExt(t)
	d := view.Build(state.Load(td("realistic"), realisticID), ext)
	if d.State != "idle" || !d.Ended {
		t.Errorf("state = %s ended=%v", d.State, d.Ended)
	}
	if len(d.Lanes) != 1 || d.Lanes[0].Name != "Explore" || d.Lanes[0].Total != 3 || d.Lanes[0].Pct != 100 || d.Lanes[0].Status != "done" {
		t.Fatalf("lanes = %+v", d.Lanes)
	}
	if len(d.Agents) != 1 || d.Agents[0].File != "explore.md" || d.Agents[0].Model != "haiku" || len(d.Agents[0].Tools) != 4 {
		t.Errorf("agents = %+v", d.Agents)
	}
	if len(d.Plan) != 1 || d.Plan[0].Mark != "✓" || d.Plan[0].Text != "Token refresh analysis" || d.Plan[0].Detail != "Explore x3" {
		t.Errorf("plan = %+v", d.Plan)
	}
	if d.Back.Processed <= d.Back.Received || d.Back.ProcFrac != 1 || d.Back.RecvFrac <= 0 || d.Back.RecvFrac >= 1 {
		t.Errorf("back = %+v", d.Back)
	}
	if len(d.Log) != 11 {
		t.Errorf("log rows = %d", len(d.Log))
	}
	if !d.HooksFound || d.Ccwatch != true || !strings.Contains(d.HooksLine, "Stop×2") {
		t.Errorf("hooks = %q found=%v ccwatch=%v", d.HooksLine, d.HooksFound, d.Ccwatch)
	}
}

// ev builds a hook event with a fixed timestamp base.
func ev(sec int, name string) state.Event {
	return state.Event{TS: time.Date(2026, 11, 9, 10, 0, sec, 0, time.UTC).Format(time.RFC3339Nano), SessionID: "s", Name: name, PromptID: "p1"}
}

func TestLaneSlotStatesAndWorkingState(t *testing.T) {
	start := func(sec int, id, typ string) state.Event {
		e := ev(sec, "SubagentStart")
		e.AgentID, e.AgentType = id, typ
		return e
	}
	stop := func(sec int, id, typ, msg string) state.Event {
		e := ev(sec, "SubagentStop")
		e.AgentID, e.AgentType, e.LastAssistantMsg = id, typ, msg
		return e
	}
	failed := ev(5, "PostToolUseFailure")
	failed.ToolName, failed.ToolUseID, failed.Error = "Agent", "tu1", "Agent failed"
	failed.ToolInput = &state.ToolInput{SubagentType: "Explore", Description: "broken"}
	prompt := ev(0, "UserPromptSubmit")
	prompt.Prompt = "# do it\nplease delegate"

	evs := []state.Event{prompt, start(1, "a1", "Explore"), start(2, "a2", "Explore"), start(3, "a3", "Explore"), stop(4, "a1", "Explore", "## Heading\n\n**done** with `code`"), failed}
	// a2 has api requests (running); a3 has none (launched); a1 stopped (done)
	otel := []state.Record{{Event: "api_request", Attrs: map[string]any{"session.id": "s", "agent_id": "a2", "model": "claude-haiku-5-5", "input_tokens": float64(10)}}}
	s := state.Build(evs, otel)
	if !s.Working {
		t.Error("prompt submitted, no Stop: session should be working")
	}
	if got := s.Subagents[0].Summary; got != "done with code" {
		t.Errorf("summary = %q (headings skipped, markdown stripped)", got)
	}
	d := view.Build(s, view.Ext{})
	if len(d.Lanes) != 1 {
		t.Fatalf("lanes = %+v", d.Lanes)
	}
	l := d.Lanes[0]
	if strings.Join(l.Slots, ",") != "done,running,launched,failed" || l.Total != 4 || l.Done != 2 || l.Pct != 50 || l.Status != "1 running · 1 failed" {
		t.Errorf("lane = %+v", l)
	}
	if d.State != "orchestrating" {
		t.Errorf("state = %s", d.State)
	}
	if len(d.Plan) != 1 || d.Plan[0].Mark != "|" || d.Plan[0].Text != "broken" {
		t.Errorf("plan = %+v", d.Plan)
	}
	if s.Log[0].Kind != "you" || s.Log[0].Text != "please delegate" {
		t.Errorf("you row = %+v", s.Log[0])
	}
	// only queued
	only := state.Build([]state.Event{start(1, "b1", "Fix")}, nil)
	if l := view.Build(only, view.Ext{}).Lanes[0]; l.Status != "queued" {
		t.Errorf("launched-only lane status = %q, want queued", l.Status)
	}
}

func TestFrontmatterForms(t *testing.T) {
	defs := agents.LoadDirs(td("claude", "agents"))
	if d := defs["Explore"]; d.Model != "haiku" || strings.Join(d.Tools, "|") != "Glob|Grep|Read|Bash" {
		t.Errorf("comma string: %+v", d)
	}
	if d := defs["fixer"]; strings.Join(d.Tools, "|") != "Read|Edit|Bash" {
		t.Errorf("flow list: %+v", d)
	}
	if d := defs["checker"]; d.Model != "sonnet" || strings.Join(d.Tools, "|") != "Read|Bash" {
		t.Errorf("block list / quoted model: %+v", d)
	}
	h := agents.LoadHooks(td("claude", "settings.json"))
	if !h.Found || !h.Ccwatch || h.Counts["Stop"] != 2 || h.Counts["PostToolUse"] != 1 {
		t.Errorf("hooks = %+v", h)
	}
	if h := agents.LoadHooks(td("claude", "missing.json")); h.Found {
		t.Error("missing settings must not be found")
	}
}

func TestParseRun(t *testing.T) {
	read := func(n string) []byte { b, _ := os.ReadFile(td("orca", n)); return b }
	ws, _ := workers.Parse(read("worker-list.json"))
	run, err := workers.ParseRun(orcaRun, read("task-list.json"), read("run-list.json"), ws)
	if err != nil {
		t.Fatal(err)
	}
	if run.Objective != "Fix the token refresh bug" || len(run.Tasks) != 3 || run.Tasks[1].WorkerName != "fix-refresh" || run.Tasks[0].Terminal != "term_a1a1a1a1-0000-4000-8000-0000000000a1" {
		t.Errorf("run = %+v", run)
	}
	d := view.Build(state.Session{ID: "s"}, view.Ext{Run: run, ModelByTerminal: map[string]string{"term_b2b2b2b2-0000-4000-8000-0000000000b2": "claude-sonnet-5-5"}})
	if d.PlanSource != "orca run" || len(d.Plan) != 3 || d.Plan[0].Mark != "✓" || d.Plan[1].Mark != "|" || d.Plan[1].Detail != "worker fix-refresh" || d.Plan[2].Mark != " " {
		t.Errorf("plan = %+v", d.Plan)
	}
	if d.State != "orchestrating" {
		t.Errorf("a live task means orchestrating, got %s", d.State)
	}
	if ended := view.Build(state.Session{ID: "s", Ended: true}, view.Ext{Run: run}); ended.State != "idle" {
		t.Errorf("an ended session is idle even with a live Orca task, got %s", ended.State)
	}
	resumed := []state.Event{ev(0, "SessionEnd"), ev(1, "SessionStart")}
	if state.Build(resumed, nil).Ended {
		t.Error("SessionStart after SessionEnd must reopen the session")
	}
	// task-notification prompts are injected by Claude Code, not typed by the user
	n := ev(0, "UserPromptSubmit")
	n.Prompt = "<task-notification>\nbackground task done"
	if s := state.Build([]state.Event{n}, nil); len(s.Log) != 0 || !s.Working {
		t.Errorf("task-notification: log=%v working=%v", s.Log, s.Working)
	}
	if len(d.Lanes) != 0 {
		t.Errorf("orca lane must not be in the Task tool lanes: %+v", d.Lanes)
	}
	l := *d.OrcaLane
	if l.Name != "orca workers" || l.Done != 1 || l.Total != 3 || l.Status != "1 running" || l.Model != "claude-sonnet-5-5" {
		t.Errorf("orca lane = %+v", l)
	}
}

func TestHookStdinDeadline(t *testing.T) {
	old := hook.StdinTimeout
	hook.StdinTimeout = 100 * time.Millisecond
	defer func() { hook.StdinTimeout = old }()
	pr, pw, _ := os.Pipe() // never written, never closed
	defer pr.Close()
	defer pw.Close()
	start := time.Now()
	if err := hook.Run(pr, t.TempDir(), time.Now()); err == nil {
		t.Error("expected a timeout error")
	}
	if time.Since(start) > time.Second {
		t.Errorf("hook blocked %v", time.Since(start))
	}
}

func TestHookKeepsPrompt(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("p", 500)
	hook.Run(strings.NewReader(`{"session_id":"s9","hook_event_name":"UserPromptSubmit","prompt":"`+long+`"}`), dir, time.Now())
	evs := state.LoadEvents(dir, "s9")
	if len(evs) != 1 || len(evs[0].Prompt) != 200 {
		t.Fatalf("events = %+v", evs)
	}
}

func TestDashboardHeightShedsLogRows(t *testing.T) {
	d := view.Build(state.Load(td("realistic"), realisticID), fixtureExt(t))
	full := tui.Dashboard(d, tui.Options{Width: 120, Loc: time.UTC})
	short := tui.Dashboard(d, tui.Options{Width: 120, Height: 30, Loc: time.UTC})
	if n := strings.Count(short, "\n"); n > 30 {
		t.Errorf("frame has %d lines for height 30", n)
	}
	if !strings.Contains(short, "ccwatch  session") || !strings.Contains(short, "session log") {
		t.Errorf("header or log lost:\n%s", short)
	}
	if strings.Count(full, "\n") <= strings.Count(short, "\n") {
		t.Error("short frame should be shorter")
	}
}

func TestHookRecordsOnlyTheTerminalHandle(t *testing.T) {
	const handle = "term_20000001-0000-4000-8000-000000000001"
	t.Setenv("ORCA_TERMINAL_HANDLE", handle)
	t.Setenv("ORCA_USER_DATA_PATH", "/secret/path/marker")
	t.Setenv("ORCA_TOKEN_FOR_TEST", "tok-s3cret-marker")
	dir := t.TempDir()
	if err := hook.Run(strings.NewReader(`{"session_id":"o1","hook_event_name":"SessionStart","source":"startup"}`), dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "events", "o1.jsonl"))
	if evs := state.LoadEvents(dir, "o1"); len(evs) != 1 || evs[0].OrcaTerminal != handle {
		t.Fatalf("events = %+v", evs)
	}
	for _, leak := range []string{"s3cret", "secret/path", "ORCA_"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("event line leaks %q: %s", leak, raw)
		}
	}
	// wrong shape or unset: nothing recorded
	for _, v := range []string{"", "not-a-handle", "term_", "term_x; rm -rf"} {
		t.Setenv("ORCA_TERMINAL_HANDLE", v)
		hook.Run(strings.NewReader(`{"session_id":"o2","hook_event_name":"Stop"}`), dir, time.Now())
	}
	for _, e := range state.LoadEvents(dir, "o2") {
		if e.OrcaTerminal != "" {
			t.Errorf("recorded %q for a bad handle", e.OrcaTerminal)
		}
	}
}

func TestHookExtractsRunIDsFromFullStdout(t *testing.T) {
	dir := t.TempDir()
	// the run id sits after the 300-char text cut
	stdout := strings.Repeat("x", 400) + ` {"id":"run_000000000002"} again run_000000000002 and run_000000000003`
	mk := func(cmd string) string {
		b, _ := json.Marshal(map[string]any{"session_id": "r1", "hook_event_name": "PostToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": cmd}, "tool_response": map[string]any{"stdout": stdout}})
		return string(b)
	}
	hook.Run(strings.NewReader(mk("orca orchestration run-list --json")), dir, time.Now())
	hook.Run(strings.NewReader(mk("echo run_000000000004")), dir, time.Now())
	evs := state.LoadEvents(dir, "r1")
	if len(evs) != 2 || strings.Join(evs[0].OrcaRuns, ",") != "run_000000000002,run_000000000003" || len(evs[1].OrcaRuns) != 0 {
		t.Fatalf("orca_runs = %+v / %+v", evs[0].OrcaRuns, evs[1].OrcaRuns)
	}
	if s := state.Build(evs, nil); strings.Join(s.RunIDs, ",") != "run_000000000002,run_000000000003" {
		t.Errorf("session run ids = %v (only `orca orchestration` commands count)", s.RunIDs)
	}
}
