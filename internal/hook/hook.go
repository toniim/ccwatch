// Package hook turns a Claude Code hook payload into one compact event line.
package hook

import (
	"encoding/json"
	"errors"
	"io"
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
	ToolInput        *state.ToolInput       `json:"tool_input"`
	ToolResponse     json.RawMessage        `json:"tool_response"`
	Error            string                 `json:"error"`
	IsInterrupt      bool                   `json:"is_interrupt"`
	LastAssistantMsg string                 `json:"last_assistant_message"`
	Prompt           string                 `json:"prompt"`
	BackgroundTasks  []state.BackgroundTask `json:"background_tasks"`
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
)

// OrcaTerminal returns $ORCA_TERMINAL_HANDLE when it has the term_<uuid> shape.
// No other ORCA_* variable is ever read: some of them are tokens.
func OrcaTerminal() string {
	if v := os.Getenv("ORCA_TERMINAL_HANDLE"); terminalRe.MatchString(v) {
		return v
	}
	return ""
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

// Convert builds the stored event from a raw hook payload.
func Convert(data []byte, now time.Time) (state.Event, bool) {
	var p rawPayload
	if json.Unmarshal(data, &p) != nil || !state.SafeID(p.SessionID) || p.Name == "" {
		return state.Event{}, false
	}
	e := state.Event{
		TS: now.Format(time.RFC3339Nano), SessionID: p.SessionID, Cwd: p.Cwd, Name: p.Name,
		PromptID: p.PromptID, AgentID: p.AgentID, AgentType: p.AgentType, Source: p.Source,
		Reason: p.Reason, ToolName: p.ToolName, ToolUseID: p.ToolUseID, DurationMs: p.DurationMs,
		PermissionMode: p.PermissionMode, Prompt: Truncate(p.Prompt, 200), Error: Truncate(p.Error, maxText), IsInterrupt: p.IsInterrupt,
		LastAssistantMsg: Truncate(p.LastAssistantMsg, maxText), LastAssistantChars: utf8.RuneCountInString(p.LastAssistantMsg), BackgroundTasks: p.BackgroundTasks,
	}
	if p.ToolInput != nil {
		in := *p.ToolInput
		in.Prompt = Truncate(in.Prompt, maxText)
		in.Command = Truncate(in.Command, 1000) // test detection needs the whole command prefix, not the text cap
		e.ToolInput = &in
	}
	if len(p.ToolResponse) > 0 && p.ToolResponse[0] == '{' {
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
			if p.ToolName == "Bash" && p.ToolInput != nil && strings.Contains(p.ToolInput.Command, "orca orchestration") {
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
