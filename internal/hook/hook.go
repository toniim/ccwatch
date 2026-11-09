// Package hook turns a Claude Code hook payload into one compact event line.
package hook

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/toniim/ccwatch/internal/state"
)

const (
	maxStdin = 1 << 20
	maxText  = 300
)

type rawPayload struct {
	SessionID        string                 `json:"session_id"`
	Cwd              string                 `json:"cwd"`
	Name             string                 `json:"hook_event_name"`
	PromptID         string                 `json:"prompt_id"`
	AgentID          string                 `json:"agent_id"`
	AgentType        string                 `json:"agent_type"`
	Source           string                 `json:"source"`
	Reason           string                 `json:"reason"`
	ToolName         string                 `json:"tool_name"`
	ToolUseID        string                 `json:"tool_use_id"`
	DurationMs       int64                  `json:"duration_ms"`
	PermissionMode   string                 `json:"permission_mode"`
	ToolInput        *rawInput              `json:"tool_input"`
	ToolResponse     json.RawMessage        `json:"tool_response"`
	Error            string                 `json:"error"`
	IsInterrupt      bool                   `json:"is_interrupt"`
	LastAssistantMsg string                 `json:"last_assistant_message"`
	Prompt           string                 `json:"prompt"`
	BackgroundTasks  []state.BackgroundTask `json:"background_tasks"`
}

// lenStr / lenBool decode a JSON string / bool and ignore any other type, so a tool whose
// input uses an unexpected shape (an MCP tool, say) never makes the whole event unreadable.
type (
	lenStr  string
	lenBool bool
)

func (l *lenStr) UnmarshalJSON(b []byte) error {
	var s string
	if len(b) > 0 && b[0] == '"' && json.Unmarshal(b, &s) == nil {
		*l = lenStr(s)
	}
	return nil
}

func (l *lenBool) UnmarshalJSON(b []byte) error {
	*l = string(b) == "true"
	return nil
}

// rawInput is the part of a tool_input read from the payload. Only the fields
// the target allowlist names are decoded; every other field (file contents,
// old/new strings, ...) is skipped and never reaches memory or disk.
type rawInput struct {
	Command         lenStr  `json:"command"`
	Description     lenStr  `json:"description"`
	SubagentType    lenStr  `json:"subagent_type"`
	RunInBackground lenBool `json:"run_in_background"`
	Prompt          lenStr  `json:"prompt"`
	FilePath        lenStr  `json:"file_path"`
	NotebookPath    lenStr  `json:"notebook_path"`
	Pattern         lenStr  `json:"pattern"`
	Path            lenStr  `json:"path"`
	URL             lenStr  `json:"url"`
	Query           lenStr  `json:"query"`
}

type rawResponse struct {
	Status            string `json:"status"`
	IsAsync           bool   `json:"isAsync"`
	AgentID           string `json:"agentId"`
	AgentType         string `json:"agentType"`
	ResolvedModel     string `json:"resolvedModel"`
	TotalTokens       int64  `json:"totalTokens"`
	TotalDurationMs   int64  `json:"totalDurationMs"`
	TotalToolUseCount int64  `json:"totalToolUseCount"`
	Stdout            string `json:"stdout"`
	Content           []struct {
		Text string `json:"text"`
	} `json:"content"`
}

var (
	terminalRe = regexp.MustCompile(`^term_[0-9a-fA-F-]{8,64}$`)
	runIDRe    = regexp.MustCompile(`run_[0-9a-f]{12}`)

	profileUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

// OrcaTerminal returns $ORCA_TERMINAL_HANDLE when it has the term_<uuid> shape.
// No other ORCA_* variable is ever read: some of them are tokens.
func OrcaTerminal() string {
	if v := os.Getenv("ORCA_TERMINAL_HANDLE"); terminalRe.MatchString(v) {
		return v
	}
	return ""
}

// Profile derives the short profile name and the $HOME-relative config dir from
// CLAUDE_CONFIG_DIR. Unset or equal to <home>/.claude is the default profile
// ("", ""). Otherwise the name is the last path element (the parent's name when
// that element is "home") without leading dots and a leading "claude-". The
// config dir is returned only when it lies under home. Nothing else is kept.
func Profile(configDir, home string) (name, rel string) {
	if configDir == "" {
		return "", ""
	}
	dir := filepath.Clean(configDir)
	if home != "" && dir == filepath.Join(home, ".claude") {
		return "", ""
	}
	base := filepath.Base(dir)
	if base == "home" {
		base = filepath.Base(filepath.Dir(dir))
	}
	base = strings.TrimLeft(base, ".")
	base = strings.TrimPrefix(base, "claude-")
	base = strings.TrimLeft(base, ".")
	if base == "" || base == "/" {
		return "", ""
	}
	name = profileUnsafe.ReplaceAllString(base, "-")
	if len(name) > 24 {
		name = name[:24]
	}
	if home != "" && filepath.IsAbs(home) {
		if r, err := filepath.Rel(home, dir); err == nil && state.ValidConfigDir(filepath.ToSlash(r)) {
			rel = filepath.ToSlash(r)
		}
	}
	return name, rel
}

// runIDs returns the distinct run ids in s, in order of appearance.
func runIDs(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range runIDRe.FindAllString(s, -1) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Truncate cuts s to at most n runes.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// setPrompt stores the whole prompt (bounded by the stdin cap) with its kind and length.
func setPrompt(e *state.Event, prompt string) {
	e.Prompt, e.PromptChars = prompt, utf8.RuneCountInString(prompt)
	if state.IsTaskNotification(prompt) {
		e.PromptKind = state.KindTaskNotification
		return
	}
	_, e.PromptKind = state.DisplayPrompt(prompt)
}

func isAgentTool(name string) bool { return name == "Agent" || name == "Task" }

// relPath shortens p to be relative to cwd when it lies under it.
func relPath(p, cwd string) string {
	if p == "" || cwd == "" || !filepath.IsAbs(p) || !filepath.IsAbs(cwd) {
		return p
	}
	r, err := filepath.Rel(cwd, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return p
	}
	return r
}

// Target is the allowlisted summary of what a tool call works on; nothing else of
// the input is ever kept. Tools outside the allowlist have no target.
func Target(tool string, in *rawInput, cwd string) string {
	if in == nil {
		if strings.HasPrefix(tool, "mcp__") {
			return mcpTarget(tool)
		}
		return ""
	}
	switch {
	case tool == "Bash":
		return Truncate(strings.Join(strings.Fields(Truncate(string(in.Command), 1000)), " "), maxText)
	case tool == "Read" || tool == "Edit" || tool == "Write" || tool == "MultiEdit":
		return Truncate(relPath(string(in.FilePath), cwd), maxText)
	case tool == "NotebookEdit":
		return Truncate(relPath(string(in.NotebookPath), cwd), maxText)
	case tool == "Grep" || tool == "Glob":
		t := Truncate(string(in.Pattern), 120)
		if in.Path != "" {
			t += " in " + relPath(string(in.Path), cwd)
		}
		return Truncate(t, maxText)
	case tool == "WebFetch":
		if u, err := url.Parse(string(in.URL)); err == nil && u.Host != "" {
			return Truncate(u.Host+u.Path, 200)
		}
		return ""
	case tool == "WebSearch":
		return Truncate(string(in.Query), 120)
	case isAgentTool(tool):
		d := Truncate(string(in.Description), 200)
		if in.SubagentType != "" {
			return Truncate(string(in.SubagentType)+": "+d, maxText)
		}
		return d
	case strings.HasPrefix(tool, "mcp__"):
		return mcpTarget(tool)
	}
	return ""
}

// mcpTarget turns mcp__<server>__<tool> into "<server>/<tool>".
func mcpTarget(tool string) string {
	rest := strings.TrimPrefix(tool, "mcp__")
	if server, name, ok := strings.Cut(rest, "__"); ok {
		return server + "/" + name
	}
	return rest
}

// Convert builds the stored event from a raw hook payload.
func Convert(data []byte, now time.Time) (state.Event, bool) {
	var p rawPayload
	if json.Unmarshal(data, &p) != nil || !state.SafeID(p.SessionID) || p.Name == "" {
		return state.Event{}, false
	}
	// Bash and Agent keep their existing handling (input subset on the Post event, response text).
	// Every other tool keeps only its target: no input, response or error text.
	full := p.ToolName == "Bash" || isAgentTool(p.ToolName)
	isTool := p.Name == "PreToolUse" || p.Name == "PostToolUse" || p.Name == "PostToolUseFailure"
	e := state.Event{
		TS: now.Format(time.RFC3339Nano), SessionID: p.SessionID, Cwd: p.Cwd, Name: p.Name,
		PromptID: p.PromptID, AgentID: p.AgentID, AgentType: p.AgentType, Source: p.Source,
		Reason: p.Reason, ToolName: p.ToolName, ToolUseID: p.ToolUseID, DurationMs: p.DurationMs,
		PermissionMode: p.PermissionMode, Error: Truncate(p.Error, maxText), IsInterrupt: p.IsInterrupt,
		LastAssistantMsg: Truncate(p.LastAssistantMsg, maxText), LastAssistantChars: utf8.RuneCountInString(p.LastAssistantMsg), BackgroundTasks: p.BackgroundTasks,
	}
	setPrompt(&e, p.Prompt)
	if isTool {
		e.Target = Target(p.ToolName, p.ToolInput, p.Cwd)
		if !full {
			e.Error = ""
		}
		if p.Name == "PreToolUse" || !full {
			e.Cwd = "" // the session's cwd comes from its other events; keep the per-call line small
		}
	}
	if p.ToolInput != nil && full && p.Name != "PreToolUse" {
		in := state.ToolInput{
			Command:     Truncate(string(p.ToolInput.Command), 1000), // test detection needs the whole command prefix, not the text cap
			Description: string(p.ToolInput.Description), SubagentType: string(p.ToolInput.SubagentType),
			RunInBackground: bool(p.ToolInput.RunInBackground), Prompt: Truncate(string(p.ToolInput.Prompt), maxText),
		}
		e.ToolInput = &in
	}
	if len(p.ToolResponse) > 0 && p.ToolResponse[0] == '{' && (full || !isTool) && p.Name != "PreToolUse" {
		var r rawResponse
		if json.Unmarshal(p.ToolResponse, &r) == nil {
			text := r.Stdout
			if text == "" {
				var parts []string
				for _, c := range r.Content {
					if c.Text != "" {
						parts = append(parts, c.Text)
					}
				}
				text = strings.Join(parts, "\n")
			}
			if p.ToolName == "Bash" && p.ToolInput != nil && strings.Contains(string(p.ToolInput.Command), "orca orchestration") {
				e.OrcaRuns = runIDs(r.Stdout) // from the full stdout, before the text is cut
			}
			e.ToolResponse = &state.ToolResponse{
				Status: r.Status, IsAsync: r.IsAsync, AgentID: r.AgentID, AgentType: r.AgentType,
				ResolvedModel: r.ResolvedModel, TotalTokens: r.TotalTokens, TotalDurationMs: r.TotalDurationMs,
				TotalToolUseCount: r.TotalToolUseCount, Text: Truncate(text, maxText),
			}
		}
	}
	return e, true
}

// StdinTimeout bounds how long the hook waits for Claude Code to finish writing.
var StdinTimeout = 2 * time.Second

func readWithDeadline(in io.Reader, d time.Duration) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(in, maxStdin))
		ch <- res{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(d):
		return nil, errors.New("stdin read timed out")
	}
}

// Run reads one payload from in and appends the event under dir. It returns an
// error for the caller to ignore: the hook must never fail Claude Code.
func Run(in io.Reader, dir string, now time.Time) error {
	data, err := readWithDeadline(in, StdinTimeout)
	if err != nil {
		return err
	}
	e, ok := Convert(data, now)
	if !ok {
		return nil
	}
	e.OrcaTerminal = OrcaTerminal()
	home, _ := os.UserHomeDir()
	e.Profile, e.ConfigDir = Profile(os.Getenv("CLAUDE_CONFIG_DIR"), home)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	evDir := filepath.Join(dir, "events")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(evDir, e.SessionID+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(line) // single O_APPEND write: atomic for lines this size
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
