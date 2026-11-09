// Package workers reads Orca worker state through the optional `orca` CLI.
package workers

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Worker is one entry of `orca orchestration worker-list --json`.
type Worker struct {
	DispatchID string `json:"dispatchId"`
	TaskID     string `json:"taskId,omitempty"`
	State      string `json:"state"`
	Liveness   string `json:"liveness"`
	Terminal   string `json:"terminal,omitempty"` // equals ORCA_TERMINAL_HANDLE of the worker's claude
	Workspace  string `json:"workspace"`          // "<id>::<path>"
}

// Path is the worktree path part of Workspace.
func (w Worker) Path() string {
	if i := strings.Index(w.Workspace, "::"); i >= 0 {
		return w.Workspace[i+2:]
	}
	return w.Workspace
}

// Name is the base name of the worktree path.
func (w Worker) Name() string { return filepath.Base(w.Path()) }

// Running reports a worker that is still alive.
func (w Worker) Running() bool { return w.Liveness == "live" }

// Parse decodes the worker-list JSON envelope.
func Parse(data []byte) ([]Worker, error) {
	var env struct {
		Result struct {
			Workers []Worker `json:"workers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return env.Result.Workers, nil
}

var terminalStates = map[string]bool{"succeeded": true, "failed": true, "stopped": true, "cancelled": true, "canceled": true, "completed": true}

// Active reports a worker that is live, or unverifiable with a non-terminal state.
func (w Worker) Active() bool {
	return w.Liveness == "live" || (w.Liveness == "unverifiable" && !terminalStates[w.State])
}

// Shown filters the default view: active workers plus the one running in terminal.
func Shown(ws []Worker, terminal string) []Worker {
	var out []Worker
	for _, w := range ws {
		if w.Active() || (terminal != "" && w.Terminal == terminal) {
			out = append(out, w)
		}
	}
	return out
}

// screenReader is the GNOME screen reader, which a bare `orca` lookup can hit.
const screenReader = "/usr/bin/orca"

// resolveOrca finds the Orca CLI: $CCWATCH_ORCA, $ORCA_CLI_COMMAND, the
// per-user install, then PATH unless that yields the GNOME screen reader.
func resolveOrca(getenv func(string) string, exists func(string) bool, lookPath func(string) (string, error)) (string, bool) {
	for _, k := range []string{"CCWATCH_ORCA", "ORCA_CLI_COMMAND"} {
		if v := getenv(k); v != "" {
			return v, true
		}
	}
	if home := getenv("HOME"); home != "" {
		if p := filepath.Join(home, ".config", "orca", "cli", "bin", "orca"); exists(p) {
			return p, true
		}
	}
	if p, err := lookPath("orca"); err == nil && p != screenReader {
		return p, true
	}
	return "", false
}

func orcaPath() (string, bool) {
	return resolveOrca(os.Getenv, func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && !st.IsDir()
	}, exec.LookPath)
}

// Available reports whether an Orca CLI can be resolved.
func Available() bool { _, ok := orcaPath(); return ok }

func orcaCmd(ctx context.Context, args ...string) ([]byte, error) {
	bin, ok := orcaPath()
	if !ok {
		return nil, exec.ErrNotFound
	}
	return exec.CommandContext(ctx, bin, args...).Output()
}

// Fetch runs `orca orchestration worker-list --json` (4 s cap).
func Fetch() ([]Worker, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := orcaCmd(ctx, "orchestration", "worker-list", "--json")
	if err != nil {
		return nil, err
	}
	return Parse(out)
}

type cache struct {
	TS      time.Time `json:"ts"`
	Workers []Worker  `json:"workers"`
}

// SaveCache stores the list so `ccwatch line` can show it without calling orca.
func SaveCache(dir string, ws []Worker) {
	b, err := json.Marshal(cache{TS: time.Now(), Workers: ws})
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	tmp := filepath.Join(dir, "workers.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, "workers.json"))
	}
}

// LoadCache returns the cached list if it is younger than maxAge.
func LoadCache(dir string, maxAge time.Duration) ([]Worker, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "workers.json"))
	if err != nil {
		return nil, false
	}
	var c cache
	if json.Unmarshal(b, &c) != nil || time.Since(c.TS) > maxAge {
		return nil, false
	}
	return c.Workers, true
}

// Task is one task of an Orca Run.
type Task struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Title      string `json:"title"`
	Dispatch   string `json:"dispatch,omitempty"`
	WorkerName string `json:"workerName,omitempty"` // workspace basename of its worker
	Terminal   string `json:"terminal,omitempty"`   // terminal of its worker (joins to a session)
}

// Run is an Orca orchestration Run with its tasks.
type Run struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
	Tasks     []Task `json:"tasks"`
}

func runOrca(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return orcaCmd(ctx, append([]string{"orchestration"}, args...)...)
}

// ParseRun builds a Run from task-list and run-list JSON plus the worker list.
func ParseRun(runID string, taskListJSON, runListJSON []byte, ws []Worker) (*Run, error) {
	var tl struct {
		Result struct {
			Tasks []struct {
				ID         string `json:"id"`
				Status     string `json:"status"`
				Title      string `json:"title"`
				DispatchID string `json:"dispatchId"`
			} `json:"tasks"`
		} `json:"result"`
	}
	if err := json.Unmarshal(taskListJSON, &tl); err != nil {
		return nil, err
	}
	run := &Run{ID: runID}
	var rl struct {
		Result struct {
			Runs []struct {
				ID        string `json:"id"`
				Objective string `json:"objective"`
			} `json:"runs"`
		} `json:"result"`
	}
	if json.Unmarshal(runListJSON, &rl) == nil {
		for _, r := range rl.Result.Runs {
			if r.ID == runID {
				run.Objective = r.Objective
			}
		}
	}
	byTask := map[string]Worker{}
	for _, w := range ws { // a live dispatch wins; otherwise the first listed (worker-list is newest first)
		if w.TaskID == "" {
			continue
		}
		if cur, ok := byTask[w.TaskID]; !ok || (!cur.Running() && w.Running()) {
			byTask[w.TaskID] = w
		}
	}
	for _, t := range tl.Result.Tasks {
		task := Task{ID: t.ID, Status: t.Status, Title: t.Title, Dispatch: t.DispatchID}
		if w, ok := byTask[t.ID]; ok {
			task.WorkerName = w.Name()
			task.Terminal = w.Terminal
			if task.Dispatch == "" {
				task.Dispatch = w.DispatchID
			}
		}
		run.Tasks = append(run.Tasks, task)
	}
	return run, nil
}

// RunOfDispatch returns the run id of a worker dispatch ("" when unknown).
func RunOfDispatch(dispatch string) string {
	out, err := runOrca("worker-show", "--dispatch", dispatch, "--json")
	if err != nil {
		return ""
	}
	var sh struct {
		Result struct {
			RunID string `json:"runId"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &sh) != nil {
		return ""
	}
	return sh.Result.RunID
}

// FetchRunTasks loads a run's tasks (task-list --run) and objective (run-list).
func FetchRunTasks(runID string, ws []Worker) *Run {
	tl, err := runOrca("task-list", "--run", runID, "--json")
	if err != nil {
		return nil
	}
	rl, _ := runOrca("run-list", "--limit", "100", "--json")
	run, err := ParseRun(runID, tl, rl, ws)
	if err != nil {
		return nil
	}
	return run
}
