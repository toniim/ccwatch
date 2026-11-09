// Package line renders the one-line Claude Code status line.
package line

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/workers"
)

// Input is the subset of the statusLine stdin JSON that ccwatch reads.
type Input struct {
	SessionID string `json:"session_id"`
	Model     struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		UsedPercentage   *float64 `json:"used_percentage"`
		TotalInputTokens int64    `json:"total_input_tokens"`
		Size             int64    `json:"context_window_size"`
	} `json:"context_window"`
}

// Tokens formats a token count like 61k.
func Tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprint(n)
}

// Render builds the status line from the statusLine input, the reduced session
// (zero value when no state exists) and an optional worker list.
func Render(in Input, s state.Session, ws []workers.Worker, haveWorkers bool) string {
	model := in.Model.DisplayName
	if model == "" {
		model = s.Model
	}
	if model == "" {
		model = "?"
	}
	mode := "idle"
	if s.Live() > 0 {
		mode = "orchestrating"
	}
	workersCell := "workers -"
	if haveWorkers {
		shown := workers.Shown(ws, s.OrcaTerminal)
		live := 0
		for _, w := range shown {
			if w.Running() {
				live++
			}
		}
		workersCell = fmt.Sprintf("workers %d/%d", live, len(shown))
	}
	ctx := "ctx -"
	switch {
	case in.ContextWindow.UsedPercentage != nil:
		tok := in.ContextWindow.TotalInputTokens
		if tok == 0 {
			tok = s.MainCtx
		}
		ctx = fmt.Sprintf("ctx %s %.0f%%", Tokens(tok), *in.ContextWindow.UsedPercentage)
	case s.MainCtx > 0:
		ctx = "ctx " + Tokens(s.MainCtx)
	}
	return strings.Join([]string{
		model, mode,
		fmt.Sprintf("agents %d live", s.Live()),
		workersCell, ctx,
		fmt.Sprintf("tests %d/%d", s.TestsPass, s.TestsPass+s.TestsFail),
	}, " | ")
}

// Run reads the statusLine JSON from r and returns the line. It never fails:
// missing or broken state degrades to model/ctx only.
func Run(r io.Reader, dir string) string {
	var in Input
	data, _ := io.ReadAll(io.LimitReader(r, 1<<20))
	_ = json.Unmarshal(data, &in)
	var s state.Session
	if state.SafeID(in.SessionID) {
		state.SaveCtxWindow(dir, in.SessionID, in.ContextWindow.Size)
		s = state.Load(dir, in.SessionID)
	}
	ws, ok := workers.LoadCache(dir, 30*time.Second)
	return Render(in, s, ws, ok)
}
