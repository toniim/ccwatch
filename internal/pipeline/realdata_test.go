package pipeline_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/hook"
	"github.com/toniim/ccwatch/internal/line"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/view"
	"github.com/toniim/ccwatch/internal/workers"
)

// testdata/coordinator is the shape found on a real interactive coordinator:
// the parent (query_source repl_main_thread:outputStyle:custom, first recorded
// SessionStart is a later "resume") shares ORCA_TERMINAL_HANDLE with a claude -p
// child (query_source sdk) that has the EARLIER SessionStart; the parent also
// received a prompt-suggestion SubagentStop (no agent_type, no SubagentStart) and
// has a 257.8k main context.
const (
	coordParent = "b0000000-0000-4000-8000-00000000b001"
	coordChild  = "c1c1c1c1-0000-4000-8000-00000000c0c1"
)

func coordFixture(t *testing.T) (string, []state.Session) {
	t.Helper()
	dir := td("coordinator")
	var all []state.Session
	for _, id := range state.Sessions(dir) {
		all = append(all, state.Load(dir, id))
	}
	if len(all) != 2 {
		t.Fatalf("fixture sessions = %d", len(all))
	}
	return dir, all
}

func coordWorkers(t *testing.T) []workers.Worker {
	t.Helper()
	b, err := os.ReadFile(td("coordinator", "worker-list.json"))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workers.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestNestingFollowsQuerySource(t *testing.T) {
	_, all := coordFixture(t)
	parent, child := sessionByID(t, all, coordParent), sessionByID(t, all, coordChild)
	if !parent.Interactive || parent.SDK || !child.SDK || child.Interactive {
		t.Fatalf("query_source: parent interactive=%v sdk=%v, child interactive=%v sdk=%v", parent.Interactive, parent.SDK, child.Interactive, child.SDK)
	}
	if !child.StartTS.Before(parent.StartTS) {
		t.Fatal("fixture must have the child's SessionStart before the parent's")
	}
	od := view.OrcaData{Workers: coordWorkers(t)} // this terminal is not a worker's
	pd := view.Build(parent, view.Link(parent, all, od))
	cd := view.Build(child, view.Link(child, all, od))
	if pd.NestedLine != "" {
		t.Errorf("interactive parent shown as nested: %q", pd.NestedLine)
	}
	if want := "shares terminal with " + coordParent[:8]; !strings.HasPrefix(cd.NestedLine, want) || strings.Contains(cd.NestedLine, "worker") {
		t.Errorf("child line = %q, want %q… without the word worker", cd.NestedLine, want)
	}
	// the same pair on a terminal that worker-list names: the child is nested in a worker session
	ws := coordWorkers(t)
	ws[0].Terminal = parent.OrcaTerminal
	od = view.OrcaData{Workers: ws}
	cd = view.Build(child, view.Link(child, all, od))
	if !strings.HasPrefix(cd.NestedLine, "nested in worker session "+coordParent[:8]) {
		t.Errorf("child on a worker terminal = %q", cd.NestedLine)
	}
	if pd = view.Build(parent, view.Link(parent, all, od)); pd.NestedLine != "" || pd.WorkerLine == "" {
		t.Errorf("owner on a worker terminal: nested %q worker %q", pd.NestedLine, pd.WorkerLine)
	}
}

func TestPhantomSubagentStopIgnored(t *testing.T) {
	dir, _ := coordFixture(t)
	s := state.Load(dir, coordParent)
	if len(s.Subagents) != 1 || s.Subagents[0].ID != "aaaa1111bbbb2222c" || s.Live() != 0 {
		t.Fatalf("subagents = %+v", s.Subagents)
	}
	for _, e := range s.Log {
		if e.Kind == "agent" && strings.Contains(e.Text, "suggest") || strings.Contains(e.Text, "run the tests again") {
			t.Errorf("phantom in the session log: %+v", e)
		}
	}
	if s.ReturnedEst != 100 { // 400 chars from the real agent only, not the 40 of the suggestion
		t.Errorf("returned est = %d, want 100", s.ReturnedEst)
	}
	d := view.Build(s, view.Ext{})
	if len(d.Lanes) != 1 || d.Lanes[0].Total != 1 || len(d.Lanes[0].Slots) != 1 || len(d.Agents) != 1 || len(d.Plan) != 1 || len(d.Table) != 1 {
		t.Errorf("phantom reached the view: lanes %+v agents %d plan %+v table %d", d.Lanes, len(d.Agents), d.Plan, len(d.Table))
	}
	if d.Back.Received != 100 {
		t.Errorf("main received = %d", d.Back.Received)
	}
	// real payload through the hook converter: agent_type is JSON null
	tmp := t.TempDir()
	payload := `{"session_id":"s1","hook_event_name":"SubagentStop","agent_id":"a7f3c0de99aa11bb2","agent_type":null,"last_assistant_message":"run the tests again"}`
	if err := hook.Run(strings.NewReader(payload), tmp, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := state.Load(tmp, "s1"); len(got.Subagents) != 0 {
		t.Errorf("null agent_type created %+v", got.Subagents)
	}
	// a typeless stop that does have an Agent tool call (foreground: PostToolUse comes after) stays
	evs := state.LoadEvents(dir, coordParent)
	var kept []state.Event
	for _, e := range evs {
		if e.Name != "SubagentStart" {
			kept = append(kept, e)
		}
	}
	for i := range kept {
		if kept[i].Name == "SubagentStop" && kept[i].AgentID == "aaaa1111bbbb2222c" {
			kept[i].AgentType = ""
		}
	}
	if got := state.Build(kept, nil); len(got.Subagents) != 1 {
		t.Errorf("an agent known from its PostToolUse(Agent) was dropped: %+v", got.Subagents)
	}
}

func TestContextWindowNeverAssumed(t *testing.T) {
	src := td("coordinator")
	dir := t.TempDir() // copy so the test can add the recorded window
	for _, sub := range []string{"events", "otel"} {
		os.MkdirAll(filepath.Join(dir, sub), 0o755)
		ents, _ := os.ReadDir(filepath.Join(src, sub))
		for _, e := range ents {
			b, _ := os.ReadFile(filepath.Join(src, sub, e.Name()))
			os.WriteFile(filepath.Join(dir, sub, e.Name()), b, 0o644)
		}
	}
	s := state.Load(dir, coordParent)
	if s.MainCtx != 257_803 {
		t.Fatalf("main ctx = %d", s.MainCtx)
	}
	// no recorded window: tokens and "window unknown", no percentage, no bar
	d := view.Build(s, view.Ext{})
	f := frame(d, 120)
	if d.CtxPct != 0 || strings.Contains(f, "200.0k") || !strings.Contains(f, "257.8k tok") || !strings.Contains(f, "window unknown") {
		t.Errorf("unknown window: size %d pct %d\n%s", d.CtxSize, d.CtxPct, f)
	}
	if l := ctxLines(f); strings.Contains(l, "%") || strings.Contains(l, "█") {
		t.Errorf("percentage or bar without a window:\n%s", l)
	}
	// `ccwatch line` records the window it is given
	in := `{"session_id":"` + coordParent + `","model":{"display_name":"Opus"},"context_window":{"context_window_size":1000000,"used_percentage":26,"total_input_tokens":257803}}`
	out := line.Run(strings.NewReader(in), dir)
	if !strings.Contains(out, "ctx 258k 26%") {
		t.Errorf("line = %q", out)
	}
	if got := state.LoadCtxWindow(dir, coordParent); got != 1_000_000 {
		t.Fatalf("recorded window = %d", got)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "ctx"))
	if len(ents) != 1 || ents[0].Name() != coordParent+".json" {
		t.Errorf("ctx dir (atomic write must leave no temp file): %v", ents)
	}
	s = state.Load(dir, coordParent)
	d = view.Build(s, view.Ext{})
	f = frame(d, 120)
	if d.CtxSize != 1_000_000 || d.CtxPct != 25 || !strings.Contains(ctxLines(f), " 25%") || !strings.Contains(f, "of 1.0M window") || strings.Contains(f, "window unknown") {
		t.Errorf("known window: size %d pct %d\n%s", d.CtxSize, d.CtxPct, f)
	}
	// a statusLine call without a size leaves the record alone; another session has none
	line.Run(strings.NewReader(`{"session_id":"`+coordParent+`"}`), dir)
	if state.LoadCtxWindow(dir, coordParent) != 1_000_000 || state.LoadCtxWindow(dir, coordChild) != 0 {
		t.Error("record changed or leaked to another session")
	}
}

// ctxLines returns the "main context" row and the two rows under it.
func ctxLines(f string) string {
	rows := strings.Split(f, "\n")
	for i, r := range rows {
		if strings.Contains(r, "main context") {
			return strings.Join(rows[i:min(i+3, len(rows))], "\n")
		}
	}
	return ""
}
