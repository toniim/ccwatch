package view

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/toniim/ccwatch/internal/agents"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/workers"
)

const slowEvery = 5 * time.Second

type orcaEntry struct {
	od       OrcaData
	at       time.Time
	inflight bool
}

type allCache struct {
	list []state.Session
	at   time.Time
}

// Source joins the state dir with the slow external inputs (agent files,
// settings.json, Orca) and caches the slow ones for 5 s. It never blocks on Orca
// unless Sync is set.
type Source struct {
	Dir        string
	ClaudeHome string // default $CCWATCH_CLAUDE_HOME or ~/.claude
	OrcaOn     bool
	Sync       bool // fetch Orca inline (for one-shot frames)

	mu     sync.Mutex
	orca   map[string]*orcaEntry // by session id
	all    allCache
	agents map[string]agents.Def
	agCwd  string
	hooks  agents.HookInfo
	slowAt time.Time
}

// NewSource builds a Source for dir; orca is used only when the CLI is on PATH.
func NewSource(dir string) *Source {
	home := os.Getenv("CCWATCH_CLAUDE_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".claude")
	}
	return &Source{Dir: dir, ClaudeHome: home, OrcaOn: workers.Available(), orca: map[string]*orcaEntry{}}
}

func (src *Source) slow(cwd string) (map[string]agents.Def, agents.HookInfo) {
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.agents == nil || src.agCwd != cwd || time.Since(src.slowAt) > slowEvery {
		src.agents = agents.LoadDirs(filepath.Join(src.ClaudeHome, "agents"), filepath.Join(cwd, ".claude", "agents"))
		src.hooks = agents.LoadHooks(filepath.Join(src.ClaudeHome, "settings.json"))
		src.agCwd, src.slowAt = cwd, time.Now()
	}
	return src.agents, src.hooks
}

// allSessions returns every known session, cached for 2 s.
func (src *Source) allSessions() []state.Session {
	src.mu.Lock()
	defer src.mu.Unlock()
	if time.Since(src.all.at) > 2*time.Second || src.all.list == nil {
		var l []state.Session
		for _, id := range state.Sessions(src.Dir) {
			l = append(l, state.Load(src.Dir, id))
		}
		src.all = allCache{l, time.Now()}
	}
	return src.all.list
}

func fetchOrca(s state.Session) (OrcaData, bool) {
	ws, err := workers.Fetch()
	if err != nil {
		return OrcaData{}, false
	}
	od := OrcaData{Workers: ws, Runs: map[string]*workers.Run{}, DispatchRun: map[string]string{}}
	if n := len(s.RunIDs); n > 0 {
		od.Runs[s.RunIDs[n-1]] = workers.FetchRunTasks(s.RunIDs[n-1], ws)
	}
	if s.OrcaTerminal != "" {
		for _, w := range ws {
			if w.Terminal != s.OrcaTerminal {
				continue
			}
			if rid := workers.RunOfDispatch(w.DispatchID); rid != "" {
				od.DispatchRun[w.DispatchID] = rid
				if _, ok := od.Runs[rid]; !ok {
					od.Runs[rid] = workers.FetchRunTasks(rid, ws)
				}
			}
		}
	}
	return od, true
}

// orcaFor returns cached Orca data for a session and starts a refresh when stale.
func (src *Source) orcaFor(s state.Session) OrcaData {
	if !src.OrcaOn {
		return OrcaData{}
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	e := src.orca[s.ID]
	if e == nil {
		e = &orcaEntry{}
		src.orca[s.ID] = e
	}
	if !e.inflight && time.Since(e.at) > slowEvery {
		e.inflight = true
		fetch := func() {
			od, ok := fetchOrca(s)
			if ok {
				workers.SaveCache(src.Dir, od.Workers)
			}
			src.mu.Lock()
			if ok {
				e.od = od
			}
			e.at, e.inflight = time.Now(), false
			src.mu.Unlock()
		}
		if src.Sync {
			src.mu.Unlock()
			fetch()
			src.mu.Lock()
		} else {
			go fetch()
		}
	}
	return e.od
}

// Workers returns the cached Orca worker list.
func (src *Source) Workers(s state.Session) []workers.Worker { return src.orcaFor(s).Workers }

// Dashboard loads one session and builds its view model.
func (src *Source) Dashboard(id string) Dashboard { return src.Build(state.Load(src.Dir, id)) }

// Build builds the view model for an already reduced session.
func (src *Source) Build(s state.Session) Dashboard {
	ag, hooks := src.slow(s.Cwd)
	od := src.orcaFor(s)
	all := src.allSessions()
	merged := make([]state.Session, 0, len(all)+1)
	for _, o := range all {
		if o.ID != s.ID {
			merged = append(merged, o)
		}
	}
	merged = append(merged, s)
	ext := Link(s, merged, od)
	ext.Agents, ext.Hooks = ag, hooks
	return Build(s, ext)
}
