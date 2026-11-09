package state

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Subagent is one subagent joined from hooks and OTLP.
type Subagent struct {
	ID            string
	Type          string
	Description   string
	Model         string
	Background    bool
	Running       bool
	Tokens        int64 // processed: Σ input+output+cache_read+cache_creation over its api_requests
	Peak          int64 // largest single-request context: max input+cache_read+cache_creation
	ReturnedChars int64 // full length of its final message
	DurationMs    int64
	Summary       string
	Started       time.Time
	PromptID      string
	Requests      int   // api_requests seen from OTLP
	Failed        bool  // the Agent tool call itself failed
	Now           *Tool // its in-flight tool call (latest PreToolUse without a Post), nil when none
	Last          *Tool // its last finished tool call
}

// LogEntry is one row of the session log.
type LogEntry struct {
	TS        time.Time  `json:"ts"`
	Kind      string     `json:"kind"` // you | agent | test | model | notice | tool | tools
	Who       string     `json:"who"`
	Text      string     `json:"text"`
	Tag       string     `json:"tag,omitempty"`
	Key       string     `json:"key,omitempty"`        // prompt rows: p<event index>, the handle of /api/prompt
	FullChars int        `json:"full_chars,omitempty"` // prompt rows: rune length of the display text when Text is only a preview
	Actor     string     `json:"actor,omitempty"`      // tool rows: agent_id of the subagent, "" = main session
	DurMs     int64      `json:"dur_ms,omitempty"`     // tool rows: duration once finished
	Count     int        `json:"count,omitempty"`      // tools rows: number of calls grouped
	Items     []LogEntry `json:"items,omitempty"`      // tools rows: the grouped tool rows (the last ones)
}

// Session is the reduced view of one Claude Code session.
type Session struct {
	ID           string
	Cwd          string
	Model        string // last main-agent model seen in OTLP
	Profile      string // first non-empty profile name seen ("" = default profile)
	ConfigDir    string // first valid $HOME-relative config dir seen
	Ended        bool
	Subagents    []*Subagent
	MainCtx      int64 // input+cache_read+cache_creation of last main api_request
	CostUSD      float64
	TestsPass    int
	TestsFail    int
	ToolFailures int
	Processed    int64     // Σ subagent Tokens
	PeakCtx      int64     // max subagent Peak
	ReturnedEst  int64     // Σ full final-message chars / 4
	Working      bool      // a prompt was submitted and no Stop followed yet
	OrcaTerminal string    // terminal handle the session ran in (first seen)
	Interactive  bool      // a main api_request had query_source repl_main_thread*
	SDK          bool      // a main api_request had query_source sdk (claude -p)
	CtxWindow    int64     // context_window_size recorded by `ccwatch line` (0 = unknown)
	StartTS      time.Time // earliest SessionStart (else first event)
	RunIDs       []string  // Orca run ids this session created or listed (Bash stdout)
	Now          *Tool     // main session's in-flight tool call (latest PreToolUse without a Post)
	Last         *Tool     // main session's last finished tool call
	Recent       []Tool    // the last RecentTools finished tool calls of every actor, oldest first
	Log          []LogEntry
}

// Live counts running subagents.
func (s Session) Live() int {
	n := 0
	for _, a := range s.Subagents {
		if a.Running {
			n++
		}
	}
	return n
}

// Leverage is Processed / ReturnedEst (0 when nothing returned).
func (s Session) Leverage() float64 {
	if s.ReturnedEst == 0 {
		return 0
	}
	return float64(s.Processed) / float64(s.ReturnedEst)
}

// IsTestCommand reports whether a Bash command runs a test suite.
func IsTestCommand(cmd string) bool { _, ok := TestCommand(cmd); return ok }

// PromptKey is the row key of the prompt stored as events[i].
func PromptKey(i int) string { return "p" + strconv.Itoa(i) }

// promptEntry builds the log row of the UserPromptSubmit stored as events[i]:
// a one-line preview; the full text is fetched by key.
func promptEntry(ts time.Time, e Event, i int) LogEntry {
	kind, text, status := PromptView(e)
	if kind == KindTaskNotification {
		if text == "" {
			text = "background task"
		}
		return LogEntry{TS: ts, Kind: "notice", Who: "notice", Text: preview(text), Tag: status}
	}
	le := LogEntry{TS: ts, Kind: "you", Who: "you", Key: PromptKey(i)}
	if kind == KindOrcaTask {
		le.Tag = KindOrcaTask
		if strings.TrimSpace(text) == "" {
			le.Text = OrcaTaskPrefix + OrcaNoBrief
			return le
		}
		le.Text = OrcaTaskPrefix + preview(text)
	} else {
		le.Text = preview(text)
	}
	if t := strings.TrimSpace(text); t != FirstLine(t) || utf8.RuneCountInString(t) > PreviewRunes {
		le.FullChars = utf8.RuneCountInString(t)
	}
	return le
}

// preview is the first line of s cut to PreviewRunes runes.
func preview(s string) string {
	l := FirstLine(s)
	if r := []rune(l); len(r) > PreviewRunes {
		return string(r[:PreviewRunes])
	}
	return l
}

var mdStrip = strings.NewReplacer("#", "", "*", "", "`", "")

// FirstLine returns the first line of s that is not blank or a markdown
// heading, with #, * and backticks stripped. If every line is a heading, the
// first heading is used.
func FirstLine(s string) string {
	heading := ""
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "#") {
			if heading == "" {
				heading = strings.TrimSpace(mdStrip.Replace(l))
			}
			continue
		}
		if l = strings.TrimSpace(mdStrip.Replace(l)); l != "" {
			return l
		}
	}
	return heading
}

var runIDRe = regexp.MustCompile(`run_[0-9a-f]{12}`)

func addUnique(l []string, v string) []string {
	for _, x := range l {
		if x == v {
			return l
		}
	}
	return append(l, v)
}

func id4(id string) string {
	if len(id) > 4 {
		return id[:4]
	}
	return id
}

// Build joins hook events and OTLP records (both in arrival order) into a Session.
func Build(events []Event, otel []Record) Session {
	var s Session
	byID := map[string]*Subagent{}
	get := func(id string) *Subagent {
		if a, ok := byID[id]; ok {
			return a
		}
		a := &Subagent{ID: id, Running: true}
		byID[id] = a
		s.Subagents = append(s.Subagents, a)
		return a
	}
	// tokens from otel per agent; fallbacks from hooks / subagent_completed
	otelTokens := map[string]int64{}
	otelPeak := map[string]int64{}
	fallback := map[string]int64{}

	// An internal agent (prompt suggestion) fires SubagentStop with no agent_type
	// and no SubagentStart / Agent tool call: it is not a subagent.
	known := map[string]bool{}
	for _, e := range events {
		switch {
		case e.Name == "SubagentStart" && e.AgentID != "":
			known[e.AgentID] = true
		case e.ToolName == "Agent" && e.ToolResponse != nil && e.ToolResponse.AgentID != "":
			known[e.ToolResponse.AgentID] = true
		}
	}
	phantom := map[string]bool{}
	for _, e := range events {
		if e.Name == "SubagentStop" && e.AgentType == "" && !known[e.AgentID] {
			phantom[e.AgentID] = true
		}
	}

	open := map[string]*Tool{}                  // tool_use_id -> call without a Post yet
	openRow := map[string]int{}                 // tool_use_id -> its row in s.Log
	lastBy := map[string]*Tool{}                // actor -> last finished call
	finished := map[string]bool{}               // tool_use_id -> its Post was seen (two hook registrations deliver every event twice)
	closeOpen := func(match func(*Tool) bool) { // the turn / agent ended: its unfinished calls will never get a Post
		for id, t := range open {
			if match(t) {
				s.Log[openRow[id]].Tag = "end"
				delete(open, id)
				delete(openRow, id)
			}
		}
	}

	sawStart := false
	for ei, e := range events {
		if s.ID == "" {
			s.ID = e.SessionID
		}
		if e.Cwd != "" {
			s.Cwd = e.Cwd
		}
		ts, _ := time.Parse(time.RFC3339Nano, e.TS)
		if s.OrcaTerminal == "" {
			s.OrcaTerminal = e.OrcaTerminal
		}
		if s.Profile == "" {
			s.Profile = e.Profile
		}
		if s.ConfigDir == "" && ValidConfigDir(e.ConfigDir) {
			s.ConfigDir = e.ConfigDir
		}
		if e.Name == "SessionStart" && (!sawStart || ts.Before(s.StartTS)) {
			s.StartTS, sawStart = ts, true
		} else if !sawStart && s.StartTS.IsZero() {
			s.StartTS = ts
		}
		if e.ToolName == "Bash" && e.ToolInput != nil && strings.Contains(e.ToolInput.Command, "orca orchestration") {
			ids := e.OrcaRuns
			if e.ToolResponse != nil {
				ids = append(ids, runIDRe.FindAllString(e.ToolResponse.Text, -1)...)
			}
			for _, id := range ids {
				s.RunIDs = addUnique(s.RunIDs, id)
			}
		}
		switch e.Name {
		case "SessionStart":
			s.Ended = false // a resumed session starts again
		case "SessionEnd":
			s.Ended = true
			closeOpen(func(*Tool) bool { return true })
		case "PreToolUse":
			if e.ToolUseID != "" && e.ToolName != "" && open[e.ToolUseID] == nil && !finished[e.ToolUseID] {
				t := &Tool{ID: e.ToolUseID, Actor: e.AgentID, Name: e.ToolName, Target: e.Target, Start: ts}
				open[t.ID], openRow[t.ID] = t, len(s.Log)
				s.Log = append(s.Log, toolEntry(t))
			}
		case "UserPromptSubmit":
			s.Working = true
			closeOpen(func(t *Tool) bool { return t.Actor == "" })
			if e.Prompt != "" || e.PromptKind != "" || e.PromptChars > 0 {
				s.Log = append(s.Log, promptEntry(ts, e, ei))
			}
		case "Stop":
			s.Working = false
			closeOpen(func(t *Tool) bool { return t.Actor == "" })
			if t := FirstLine(e.LastAssistantMsg); t != "" {
				s.Log = append(s.Log, LogEntry{TS: ts, Kind: "model", Text: t})
			}
		case "SubagentStart":
			a := get(e.AgentID)
			a.Type = pick(a.Type, e.AgentType)
			a.Started = ts
			a.PromptID = pick(a.PromptID, e.PromptID)
			s.Log = append(s.Log, LogEntry{TS: ts, Kind: "agent", Who: a.Type + " " + id4(a.ID), Text: "started", Tag: "start"})
		case "SubagentStop":
			if phantom[e.AgentID] {
				continue
			}
			a := get(e.AgentID)
			a.Type = pick(a.Type, e.AgentType)
			a.Running = false
			closeOpen(func(t *Tool) bool { return t.Actor == e.AgentID })
			a.Summary = FirstLine(e.LastAssistantMsg)
			s.Log = append(s.Log, LogEntry{TS: ts, Kind: "agent", Who: a.Type + " " + id4(a.ID), Text: a.Summary, Tag: "done"})
			a.ReturnedChars = int64(e.LastAssistantChars)
			if a.ReturnedChars == 0 {
				a.ReturnedChars = int64(utf8.RuneCountInString(e.LastAssistantMsg))
			}
		case "PostToolUse", "PostToolUseFailure":
			if e.ToolName != "" && !(e.ToolUseID != "" && finished[e.ToolUseID]) {
				if e.ToolUseID != "" {
					finished[e.ToolUseID] = true
				}
				t := open[e.ToolUseID]
				row, paired := openRow[e.ToolUseID]
				if t == nil {
					t = &Tool{ID: e.ToolUseID, Actor: e.AgentID, Name: e.ToolName, Start: ts}
					if e.DurationMs > 0 {
						t.Start = ts.Add(-time.Duration(e.DurationMs) * time.Millisecond)
					}
				}
				t.Target = pick(e.Target, t.Target)
				t.Done, t.Failed, t.DurMs = true, e.Name == "PostToolUseFailure", e.DurationMs
				if t.DurMs == 0 && paired && !ts.IsZero() && ts.After(t.Start) {
					t.DurMs = ts.Sub(t.Start).Milliseconds()
				}
				if paired {
					s.Log[row] = toolEntry(t)
				} else {
					le := toolEntry(t)
					le.TS = ts // no Pre seen: the row sits where the Post arrived
					s.Log = append(s.Log, le)
				}
				delete(open, e.ToolUseID)
				delete(openRow, e.ToolUseID)
				lastBy[t.Actor] = t
				s.Recent = append(s.Recent, *t)
				if len(s.Recent) > RecentTools {
					s.Recent = s.Recent[len(s.Recent)-RecentTools:]
				}
			}
			if e.Name == "PostToolUseFailure" && e.ToolName == "Agent" {
				a := &Subagent{ID: "failed-" + e.ToolUseID, Failed: true, PromptID: e.PromptID, Started: ts}
				if e.ToolInput != nil {
					a.Type, a.Description = e.ToolInput.SubagentType, e.ToolInput.Description
				}
				s.Subagents = append(s.Subagents, a)
				s.Log = append(s.Log, LogEntry{TS: ts, Kind: "agent", Who: a.Type, Text: FirstLine(e.Error), Tag: "failed"})
			}
			if e.ToolName == "Agent" && e.ToolResponse != nil && e.ToolResponse.AgentID != "" {
				r := e.ToolResponse
				a := get(r.AgentID)
				a.PromptID = pick(a.PromptID, e.PromptID)
				if e.ToolInput != nil {
					a.Description = pick(a.Description, e.ToolInput.Description)
					a.Background = e.ToolInput.RunInBackground
					a.Type = pick(a.Type, e.ToolInput.SubagentType)
				}
				a.Type = pick(a.Type, r.AgentType)
				a.Model = pick(a.Model, r.ResolvedModel)
				if r.Status == "completed" {
					a.Running = false
					fallback[a.ID] = r.TotalTokens
					a.DurationMs = r.TotalDurationMs
				}
			}
			if e.ToolName == "Bash" && e.ToolInput != nil {
				if shown, ok := TestCommand(e.ToolInput.Command); ok {
					tag := "pass"
					if e.Name == "PostToolUse" {
						s.TestsPass++
					} else {
						s.TestsFail++
						tag = "fail"
					}
					s.Log = append(s.Log, LogEntry{TS: ts, Kind: "test", Who: "tests", Text: shown, Tag: tag})
				}
			}
			if e.Name == "PostToolUseFailure" {
				s.ToolFailures++
			}
		}
	}

	for _, r := range otel {
		if s.ID == "" {
			s.ID = r.str("session.id")
		}
		switch r.Event {
		case "api_request":
			total := r.num("input_tokens") + r.num("output_tokens") + r.num("cache_read_tokens") + r.num("cache_creation_tokens")
			s.CostUSD += r.float("cost_usd")
			if id := r.str("agent_id"); phantom[id] {
				continue // cost counted above; an internal agent is not a lane
			} else if id != "" {
				otelTokens[id] += total
				get(id).Requests++
				if ctx := r.num("input_tokens") + r.num("cache_read_tokens") + r.num("cache_creation_tokens"); ctx > otelPeak[id] {
					otelPeak[id] = ctx
				}
				a := get(id)
				a.Model = pick(a.Model, r.str("model"))
			} else {
				s.Model = pick(r.str("model"), s.Model)
				switch qs := r.str("query_source"); {
				case qs == "sdk":
					s.SDK = true
				case strings.HasPrefix(qs, "repl_main_thread"):
					s.Interactive = true
				}
				s.MainCtx = r.num("input_tokens") + r.num("cache_read_tokens") + r.num("cache_creation_tokens")
			}
		case "subagent_completed":
			id := r.str("agent_id")
			if id == "" {
				continue
			}
			a := get(id)
			a.Running = false
			a.Type = pick(a.Type, r.str("agent_type"))
			a.Model = pick(a.Model, r.str("final_model"), r.str("model"))
			if a.DurationMs == 0 {
				a.DurationMs = r.num("duration_ms")
			}
			if fallback[id] == 0 {
				fallback[id] = r.num("total_tokens")
			}
		}
	}

	nowBy := map[string]*Tool{}
	for _, t := range open {
		if n := nowBy[t.Actor]; n == nil || t.Start.After(n.Start) {
			nowBy[t.Actor] = t
		}
	}
	s.Now, s.Last = nowBy[""], lastBy[""]
	for _, a := range s.Subagents {
		a.Now, a.Last = nowBy[a.ID], lastBy[a.ID]
	}
	for i := range s.Log {
		switch s.Log[i].Kind {
		case "model":
			s.Log[i].Who = pick(s.Model, "model")
		case "tool":
			s.Log[i].Who = "main"
			if id := s.Log[i].Actor; id != "" {
				s.Log[i].Who = id4(id)
				if a := byID[id]; a != nil {
					s.Log[i].Who = pick(a.Type, "agent") + " " + id4(id)
				}
			}
		}
	}
	var returnedChars int64
	for _, a := range s.Subagents {
		if t, ok := otelTokens[a.ID]; ok {
			a.Tokens = t
			a.Peak = otelPeak[a.ID]
		} else {
			a.Tokens = fallback[a.ID]
			a.Peak = fallback[a.ID] // hook totalTokens: best available without otel
		}
		s.Processed += a.Tokens
		if a.Peak > s.PeakCtx {
			s.PeakCtx = a.Peak
		}
		returnedChars += a.ReturnedChars
	}
	s.ReturnedEst = returnedChars / 4
	return s
}

func pick(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
