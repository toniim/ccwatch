// Package view turns a reduced session plus external context into the dashboard
// model that both the TUI and the web page render.
package view

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/toniim/ccwatch/internal/agents"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/workers"
)

// LogRows is the number of session-log rows kept.
const LogRows = 12

// Ext is everything outside the event files that the dashboard joins.
type Ext struct {
	Agents          map[string]agents.Def
	Hooks           agents.HookInfo
	Workers         []workers.Worker
	Run             *workers.Run
	ModelByTerminal map[string]string // main model of the worker session behind each Orca terminal
	WorkerLine      string            // "worker of <run> · task <title>" when this session is an Orca worker
	NestedLine      string            // set when this session is nested inside a worker's terminal
}

// AgentCard is one entry of the agents panel.
type AgentCard struct {
	Type    string   `json:"type"`
	File    string   `json:"file,omitempty"`
	Model   string   `json:"model"`
	Tools   []string `json:"tools,omitempty"`
	Builtin bool     `json:"builtin"`
}

// PlanItem is one checklist line of the main-session panel.
type PlanItem struct {
	Mark   string `json:"mark"` // ✓ | ✗ | | (running) | " " (not started)
	Text   string `json:"text"`
	Detail string `json:"detail"`
}

// Lane is one lane of the Task-tool section.
type Lane struct {
	Name   string   `json:"name"`
	Model  string   `json:"model"`
	Slots  []string `json:"slots"` // launched | running | done | failed
	Done   int      `json:"done"`
	Total  int      `json:"total"`
	Pct    int      `json:"pct"`
	Status string   `json:"status"` // queued | N running | done
}

// Back is the "back to main" section.
type Back struct {
	Processed int64   `json:"processed"`
	Received  int64   `json:"received"`
	ProcFrac  float64 `json:"procFrac"` // log scale, 0..1
	RecvFrac  float64 `json:"recvFrac"`
}

// Dashboard is the full view model.
type Dashboard struct {
	SessionID   string           `json:"sessionId"`
	Cwd         string           `json:"cwd"`
	Model       string           `json:"model"`
	Ended       bool             `json:"ended"`
	State       string           `json:"state"` // orchestrating | working | idle
	CostUSD     float64          `json:"costUsd"`
	Ctx         int64            `json:"ctx"`
	CtxSize     int64            `json:"ctxSize"`
	CtxKnown    bool             `json:"ctxKnown"` // false: window size unknown, show tokens only
	CtxPct      int              `json:"ctxPct"`
	Agents      []AgentCard      `json:"agents"`
	HooksLine   string           `json:"hooksLine"`
	HooksFound  bool             `json:"hooksFound"`
	Ccwatch     bool             `json:"ccwatchHooks"`
	PlanSource  string           `json:"planSource"` // prompts | orca run
	PlanTitle   string           `json:"planTitle"`
	Plan        []PlanItem       `json:"plan"`
	Lanes       []Lane           `json:"lanes"`
	Subagents   int              `json:"subagents"`
	Back        Back             `json:"back"`
	Log         []state.LogEntry `json:"log"`
	TestsPass   int              `json:"testsPass"`
	TestsFail   int              `json:"testsFail"`
	ToolFails   int              `json:"toolFailures"`
	Leverage    float64          `json:"leverage"`
	Run         *workers.Run     `json:"run,omitempty"`
	OrcaLane    *Lane            `json:"orcaLane,omitempty"`
	OrcaHeader  string           `json:"orcaHeader,omitempty"`
	WorkerLine  string           `json:"workerLine,omitempty"`
	NestedLine  string           `json:"nestedLine,omitempty"`
	Table       []TableRow       `json:"table"`
	LiveAgents  int              `json:"liveAgents"`
	ProcessedTk int64            `json:"processed"`
	PeakCtx     int64            `json:"peakCtx"`
}

// TableRow is one subagent row (Phase 1 table view).
type TableRow struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Background  bool   `json:"background"`
	Model       string `json:"model"`
	State       string `json:"state"` // running | done | failed
	Tokens      int64  `json:"tokens"`
	Peak        int64  `json:"peak"`
	DurationMs  int64  `json:"durationMs"`
	Summary     string `json:"summary"`
}

func slotOf(a *state.Subagent) string {
	switch {
	case a.Failed:
		return "failed"
	case a.Running && a.Requests == 0:
		return "launched"
	case a.Running:
		return "running"
	}
	return "done"
}

func frac(v, max int64) float64 {
	if max <= 0 || v <= 0 {
		return 0
	}
	return math.Log1p(float64(v)) / math.Log1p(float64(max))
}

func topModel(models []string) string {
	count := map[string]int{}
	best := ""
	for _, m := range models {
		if m == "" {
			continue
		}
		count[m]++
		if best == "" || count[m] > count[best] {
			best = m
		}
	}
	return best
}

func typeKey(t string) string {
	if t == "" {
		return "agent"
	}
	return t
}

// Build assembles the dashboard.
func Build(s state.Session, ext Ext) Dashboard {
	d := Dashboard{
		SessionID: s.ID, Cwd: s.Cwd, Model: s.Model, Ended: s.Ended, CostUSD: s.CostUSD,
		Ctx: s.MainCtx, CtxSize: s.CtxWindow, Subagents: len(s.Subagents), LiveAgents: s.Live(),
		TestsPass: s.TestsPass, TestsFail: s.TestsFail, ToolFails: s.ToolFailures,
		Leverage: s.Leverage(), Run: ext.Run, WorkerLine: ext.WorkerLine, NestedLine: ext.NestedLine, ProcessedTk: s.Processed, PeakCtx: s.PeakCtx,
		Plan: []PlanItem{}, Lanes: []Lane{}, Agents: []AgentCard{}, Table: []TableRow{},
	}
	// The window size is never assumed: it comes from `ccwatch line --record` data only.
	if d.CtxSize > 0 {
		d.CtxKnown = true
		d.CtxPct = int(math.Min(100, float64(d.Ctx)*100/float64(d.CtxSize)))
	}

	// lanes + agent cards, one per agent type in order of first appearance
	var order []string
	byType := map[string][]*state.Subagent{}
	for _, a := range s.Subagents {
		k := typeKey(a.Type)
		if _, ok := byType[k]; !ok {
			order = append(order, k)
		}
		byType[k] = append(byType[k], a)
		st := slotOf(a)
		state := st
		if st == "launched" {
			state = "running"
		}
		d.Table = append(d.Table, TableRow{ID: a.ID, Type: a.Type, Description: a.Description, Background: a.Background,
			Model: a.Model, State: state, Tokens: a.Tokens, Peak: a.Peak, DurationMs: a.DurationMs, Summary: a.Summary})
	}
	for _, k := range order {
		list := byType[k]
		l := Lane{Name: k, Total: len(list)}
		var models []string
		running, failed, launched := 0, 0, 0
		allLaunched := true
		for _, a := range list {
			sl := slotOf(a)
			l.Slots = append(l.Slots, sl)
			models = append(models, a.Model)
			switch sl {
			case "done":
				l.Done++
			case "running":
				running++
			case "launched":
				launched++
			case "failed":
				failed++
			}
			if sl != "launched" {
				allLaunched = false
			}
		}
		l.Done += failed // a failed launch is finished work for the bar
		l.Pct = l.Done * 100 / l.Total
		def, hasDef := lookup(ext.Agents, k)
		l.Model = topModel(models)
		if l.Model == "" && hasDef {
			l.Model = def.Model
		}
		if l.Model == "" {
			l.Model = "?"
		}
		switch {
		case allLaunched:
			l.Status = "queued"
		case running > 0:
			l.Status = fmt.Sprintf("%d running", running)
		case launched > 0:
			l.Status = fmt.Sprintf("%d queued", launched)
		default:
			l.Status = "done"
		}
		if failed > 0 {
			l.Status += fmt.Sprintf(" · %d failed", failed)
		}
		d.Lanes = append(d.Lanes, l)

		card := AgentCard{Type: k, Model: l.Model, Builtin: !hasDef}
		if hasDef {
			card.File, card.Tools = def.File, def.Tools
			if def.Model != "" {
				card.Model = def.Model
			}
		}
		d.Agents = append(d.Agents, card)
	}

	// Orca run: tasks replace the plan, and an "orca workers" lane is added
	orcaLive := false
	if ext.Run != nil {
		d.PlanSource = "orca run"
		d.PlanTitle = ext.Run.ID
		if ext.Run.Objective != "" {
			d.PlanTitle = ext.Run.Objective
		}
		lane := Lane{Name: "orca workers", Total: len(ext.Run.Tasks)}
		var models []string
		running := 0
		for _, t := range ext.Run.Tasks {
			item := PlanItem{Mark: taskMark(t.Status), Text: t.Title}
			if t.WorkerName != "" {
				item.Detail = "worker " + t.WorkerName
			}
			d.Plan = append(d.Plan, item)
			switch item.Mark {
			case "✓":
				lane.Slots, lane.Done = append(lane.Slots, "done"), lane.Done+1
			case "✗":
				lane.Slots, lane.Done = append(lane.Slots, "failed"), lane.Done+1
			case "|":
				lane.Slots, running, orcaLive = append(lane.Slots, "running"), running+1, true
			default:
				lane.Slots = append(lane.Slots, "launched")
			}
			if m := ext.ModelByTerminal[t.Terminal]; m != "" && t.Terminal != "" {
				models = append(models, m)
			}
		}
		if lane.Total > 0 {
			lane.Pct = lane.Done * 100 / lane.Total
			lane.Model = topModel(models)
			if lane.Model == "" {
				lane.Model = "?"
			}
			switch {
			case running > 0:
				lane.Status = fmt.Sprintf("%d running", running)
			case lane.Done == lane.Total:
				lane.Status = "done"
			default:
				lane.Status = "queued"
			}
			d.OrcaLane = &lane
			d.OrcaHeader = fmt.Sprintf("» Orca Run · %d workers · each in its own session «", lane.Total)
		}
	} else {
		d.PlanSource = "prompts"
		d.Plan = promptPlan(s)
	}

	if d.Plan == nil {
		d.Plan = []PlanItem{} // JSON [] not null
	}
	switch {
	case s.Ended:
		d.State = "idle"
	case s.Live() > 0 || orcaLive:
		d.State = "orchestrating"
	case s.Working:
		d.State = "working"
	default:
		d.State = "idle"
	}

	d.Back = Back{Processed: s.Processed, Received: s.ReturnedEst}
	max := s.Processed
	if s.ReturnedEst > max {
		max = s.ReturnedEst
	}
	d.Back.ProcFrac, d.Back.RecvFrac = frac(s.Processed, max), frac(s.ReturnedEst, max)

	d.Log = s.Log
	if len(d.Log) > LogRows {
		d.Log = d.Log[len(d.Log)-LogRows:]
	}
	if d.Log == nil {
		d.Log = []state.LogEntry{}
	}

	h := ext.Hooks
	d.HooksFound, d.Ccwatch = h.Found, h.Ccwatch
	switch {
	case !h.Found:
		d.HooksLine = "settings.json: not found"
	case len(h.Counts) == 0:
		d.HooksLine = "settings.json: no hooks"
	default:
		var parts []string
		for _, ev := range h.Events() {
			parts = append(parts, fmt.Sprintf("%s×%d", ev, h.Counts[ev]))
		}
		d.HooksLine = strings.Join(parts, " ")
	}
	return d
}

func lookup(defs map[string]agents.Def, t string) (agents.Def, bool) {
	if d, ok := defs[t]; ok {
		return d, true
	}
	for k, d := range defs {
		if strings.EqualFold(k, t) {
			return d, true
		}
	}
	return agents.Def{}, false
}

func taskMark(status string) string {
	switch status {
	case "completed", "succeeded", "done":
		return "✓"
	case "failed", "error", "stopped":
		return "✗"
	case "dispatched", "running", "in_progress", "active":
		return "|"
	}
	return " "
}

// promptPlan groups Agent launches per prompt_id: "<first description> -> Explore x3".
func promptPlan(s state.Session) []PlanItem {
	type group struct {
		first   string
		types   []string
		counts  map[string]int
		running int
		failed  int
		done    int
		n       int
	}
	var order []string
	groups := map[string]*group{}
	for _, a := range s.Subagents {
		g, ok := groups[a.PromptID]
		if !ok {
			g = &group{counts: map[string]int{}}
			groups[a.PromptID] = g
			order = append(order, a.PromptID)
		}
		if g.first == "" {
			g.first = a.Description
		}
		t := typeKey(a.Type)
		if g.counts[t] == 0 {
			g.types = append(g.types, t)
		}
		g.counts[t]++
		g.n++
		switch slotOf(a) {
		case "running", "launched":
			g.running++
		case "failed":
			g.failed++
		default:
			g.done++
		}
	}
	var items []PlanItem
	for _, id := range order {
		g := groups[id]
		var parts []string
		for _, t := range g.types {
			parts = append(parts, fmt.Sprintf("%s x%d", t, g.counts[t]))
		}
		mark := "✓"
		switch {
		case g.running > 0:
			mark = "|"
		case g.failed > 0:
			mark = "✗"
		}
		text := g.first
		if text == "" {
			text = "subagent launches"
		}
		items = append(items, PlanItem{Mark: mark, Text: text, Detail: strings.Join(parts, " + ")})
	}
	return items
}

// OrcaData is what the Orca CLI told us, gathered outside the pure join.
type OrcaData struct {
	Workers     []workers.Worker
	Runs        map[string]*workers.Run // run id -> tasks
	DispatchRun map[string]string       // worker dispatch id -> run id
}

// terminalOwner picks the session that owns a terminal among those sharing it.
// With otel evidence an interactive session (query_source repl_main_thread*)
// owns it, because a `claude -p` (query_source sdk) started from its Bash shell
// inherits the same handle even when its SessionStart was recorded earlier. Without
// that evidence the earliest SessionStart owns it.
func terminalOwner(group []state.Session) state.Session {
	earliest := func(pick func(state.Session) bool) (state.Session, bool) {
		var f state.Session
		ok := false
		for _, o := range group {
			if !pick(o) {
				continue
			}
			if !ok || o.StartTS.Before(f.StartTS) || (o.StartTS.Equal(f.StartTS) && o.ID < f.ID) {
				f, ok = o, true
			}
		}
		return f, ok
	}
	if f, ok := earliest(func(o state.Session) bool { return o.Interactive }); ok {
		return f
	}
	f, _ := earliest(func(state.Session) bool { return true })
	return f
}

// Link joins one session with Orca by terminal handle. all is every known session.
//   - nested session: shares its terminal with another session and is a `claude -p`
//     (main query_source sdk) next to an interactive one; with no otel evidence the
//     later SessionStart is the nested one. It is never joined to Orca;
//   - worker session: the owner of a terminal that worker-list names;
//   - coordinator session: owns the run ids found in its own `orca orchestration`
//     Bash output; only it gets the Run checklist and the orca lane.
func Link(s state.Session, all []state.Session, od OrcaData) Ext {
	ext := Ext{Workers: od.Workers, ModelByTerminal: map[string]string{}}
	groups := map[string][]state.Session{}
	for _, o := range all {
		if o.OrcaTerminal != "" {
			groups[o.OrcaTerminal] = append(groups[o.OrcaTerminal], o)
		}
	}
	owner := map[string]state.Session{}
	for term, g := range groups {
		f := terminalOwner(g)
		owner[term] = f
		if f.Model != "" {
			ext.ModelByTerminal[term] = f.Model
		}
	}
	if n := len(s.RunIDs); n > 0 {
		ext.Run = od.Runs[s.RunIDs[n-1]]
	}
	if s.OrcaTerminal != "" {
		f := owner[s.OrcaTerminal]
		nested := f.ID != s.ID
		if s.Interactive {
			nested = false
		}
		if nested {
			who := f.ID[:min(8, len(f.ID))]
			inWorkerList := false
			for _, w := range od.Workers {
				if w.Terminal == s.OrcaTerminal {
					inWorkerList = true
				}
			}
			if inWorkerList {
				ext.NestedLine = "nested in worker session " + who + " (same terminal) · not joined to Orca"
			} else {
				ext.NestedLine = "shares terminal with " + who + " · not joined to Orca"
			}
		} else {
			for _, w := range od.Workers {
				if w.Terminal != s.OrcaTerminal {
					continue
				}
				line := "worker " + w.DispatchID
				if runID := od.DispatchRun[w.DispatchID]; runID != "" {
					line = "worker of " + runID
					if run := od.Runs[runID]; run != nil {
						for _, t := range run.Tasks {
							if t.ID == w.TaskID {
								line += " · task " + t.Title
							}
						}
					}
				}
				ext.WorkerLine = line
				break
			}
		}
	}
	return ext
}

// Clock formats a log timestamp as HH:MM:SS in loc.
func Clock(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.In(loc).Format("15:04:05")
}
