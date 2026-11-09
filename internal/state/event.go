// Package state holds the on-disk event model and the pure reducer that joins
// hook events with OTLP records into a Session.
package state

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Dir returns the state directory: $CCWATCH_STATE or ${XDG_STATE_HOME:-~/.local/state}/ccwatch.
func Dir() string {
	if d := os.Getenv("CCWATCH_STATE"); d != "" {
		return d
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "ccwatch")
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// SafeID reports whether id may be used as a file name component.
func SafeID(id string) bool { return safeID.MatchString(id) && id != "." && id != ".." }

// ToolInput is the stored tool_input of a tool call: the JSON object as the tool
// received it, each string field capped by the hook (with "<field>_chars" holding
// the full length when cut). Events written before the full input was stored hold
// only command, description, subagent_type, run_in_background and prompt, which
// read the same way.
type ToolInput map[string]any

// Str returns the string field k, "" when it is absent or not a string.
func (in ToolInput) Str(k string) string { s, _ := in[k].(string); return s }

// Bool returns the bool field k.
func (in ToolInput) Bool(k string) bool { b, _ := in[k].(bool); return b }

// Preview is the head and tail of a long text with its full rune length.
type Preview struct {
	Head  string `json:"head,omitempty"`
	Tail  string `json:"tail,omitempty"` // empty when Head holds the whole text
	Chars int    `json:"chars"`
}

// ToolOutput is what is kept of a tool_response: its size for every tool, and for
// Bash head+tail previews of stdout and stderr. No other response body is stored.
type ToolOutput struct {
	Bytes       int      `json:"bytes,omitempty"`
	Stdout      *Preview `json:"stdout,omitempty"`
	Stderr      *Preview `json:"stderr,omitempty"`
	Interrupted bool     `json:"interrupted,omitempty"`
}

// ToolResponse is the kept subset of a hook's tool_response.
type ToolResponse struct {
	Status            string `json:"status,omitempty"`
	IsAsync           bool   `json:"isAsync,omitempty"`
	AgentID           string `json:"agentId,omitempty"`
	AgentType         string `json:"agentType,omitempty"`
	ResolvedModel     string `json:"resolvedModel,omitempty"`
	TotalTokens       int64  `json:"totalTokens,omitempty"`
	TotalDurationMs   int64  `json:"totalDurationMs,omitempty"`
	TotalToolUseCount int64  `json:"totalToolUseCount,omitempty"`
	Text              string `json:"text,omitempty"`
}

// BackgroundTask is one entry of a Stop/SubagentStop background_tasks list.
type BackgroundTask struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type,omitempty"`
	Status      string `json:"status,omitempty"`
	Description string `json:"description,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
}

// Event is one line of events/<session_id>.jsonl.
type Event struct {
	TS                 string           `json:"ts"`
	SessionID          string           `json:"session_id"`
	Cwd                string           `json:"cwd,omitempty"`
	Name               string           `json:"hook_event_name"`
	PromptID           string           `json:"prompt_id,omitempty"`
	AgentID            string           `json:"agent_id,omitempty"`
	AgentType          string           `json:"agent_type,omitempty"`
	Source             string           `json:"source,omitempty"`
	Reason             string           `json:"reason,omitempty"`
	ToolName           string           `json:"tool_name,omitempty"`
	ToolUseID          string           `json:"tool_use_id,omitempty"`
	Target             string           `json:"target,omitempty"` // allowlisted summary of what the tool works on, see hook.Target
	DurationMs         int64            `json:"duration_ms,omitempty"`
	ToolInput          ToolInput        `json:"tool_input,omitempty"`
	ToolOutput         *ToolOutput      `json:"tool_output,omitempty"`
	ToolResponse       *ToolResponse    `json:"tool_response,omitempty"`
	Error              string           `json:"error,omitempty"`
	IsInterrupt        bool             `json:"is_interrupt,omitempty"`
	LastAssistantMsg   string           `json:"last_assistant_message,omitempty"`
	Prompt             string           `json:"prompt,omitempty"`                       // UserPromptSubmit text, in full
	PromptKind         string           `json:"prompt_kind,omitempty"`                  // orca_task | paste | task_notification
	PromptChars        int              `json:"prompt_chars,omitempty"`                 // rune length of the prompt
	OrcaTerminal       string           `json:"orca_terminal,omitempty"`                // $ORCA_TERMINAL_HANDLE of the hook process
	OrcaRuns           []string         `json:"orca_runs,omitempty"`                    // run ids in the stdout of an `orca orchestration` Bash call
	LastAssistantChars int              `json:"last_assistant_message_chars,omitempty"` // rune length before truncation
	BackgroundTasks    []BackgroundTask `json:"background_tasks,omitempty"`
	PermissionMode     string           `json:"permission_mode,omitempty"`
	Profile            string           `json:"profile,omitempty"`    // short profile name derived from $CLAUDE_CONFIG_DIR ("" = default profile)
	ConfigDir          string           `json:"config_dir,omitempty"` // that config dir relative to $HOME, only when it is under $HOME
}

// ValidConfigDir reports whether rel is a safe $HOME-relative config dir: not
// empty, not absolute, no ".." element, already clean.
func ValidConfigDir(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) || filepath.Clean(rel) != rel || rel == "." {
		return false
	}
	for _, p := range strings.Split(rel, "/") {
		if p == ".." {
			return false
		}
	}
	return true
}

// Record is one flattened line of otel/<session.id>.jsonl.
type Record struct {
	Event string         `json:"event"`
	Attrs map[string]any `json:"attrs"`
}

func (r Record) str(k string) string {
	s, _ := r.Attrs[k].(string)
	return s
}

func (r Record) num(k string) int64 {
	switch v := r.Attrs[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case string:
		var n int64
		for _, c := range v {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int64(c-'0')
		}
		return n
	}
	return 0
}

func (r Record) float(k string) float64 {
	f, _ := r.Attrs[k].(float64)
	return f
}

// RelPath shortens p to be relative to cwd when it lies under it.
func RelPath(p, cwd string) string {
	if p == "" || cwd == "" || !filepath.IsAbs(p) || !filepath.IsAbs(cwd) {
		return p
	}
	r, err := filepath.Rel(cwd, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return p
	}
	return r
}
