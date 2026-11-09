package view

import "github.com/toniim/ccwatch/internal/state"

// RunRef names an Orca Run in the session tree.
type RunRef struct {
	ID        string `json:"id"`
	Objective string `json:"objective"`
}

// Rel is where one session sits in the tree.
type Rel struct {
	Role   string  `json:"role"`   // coordinator | worker | nested | standalone
	Parent string  `json:"parent"` // parent session id, "" for a root
	Run    *RunRef `json:"run"`    // the coordinator's current Run, or the Run a worker belongs to
}

// Relations classifies every session with the same joins Link uses:
//   - coordinator: owns run ids (found in its own `orca orchestration` Bash output);
//     its Run is the last id it saw;
//   - nested: shares a terminal with another session and is not interactive
//     (terminalOwner decides who owns the terminal); parent is the owner;
//   - worker: owns a terminal that worker-list names, for a dispatch of a known
//     Run; parent is the coordinator owning that Run, "" when none is known;
//   - standalone: everything else.
//
// objectives maps run id to its objective text.
func Relations(all []state.Session, od OrcaData, objectives map[string]string) map[string]Rel {
	groups := map[string][]state.Session{}
	for _, o := range all {
		if o.OrcaTerminal != "" {
			groups[o.OrcaTerminal] = append(groups[o.OrcaTerminal], o)
		}
	}
	owner := map[string]state.Session{}
	for term, g := range groups {
		owner[term] = terminalOwner(g)
	}
	coordOf := map[string]string{} // run id -> coordinator session id (the earliest start wins)
	startOf := map[string]state.Session{}
	for _, o := range all {
		for _, rid := range o.RunIDs {
			if cur, ok := startOf[rid]; !ok || o.StartTS.Before(cur.StartTS) || (o.StartTS.Equal(cur.StartTS) && o.ID < cur.ID) {
				startOf[rid], coordOf[rid] = o, o.ID
			}
		}
	}
	ref := func(id string) *RunRef { return &RunRef{ID: id, Objective: objectives[id]} }

	out := make(map[string]Rel, len(all))
	for _, s := range all {
		rel := Rel{Role: "standalone"}
		switch {
		case len(s.RunIDs) > 0:
			rel.Role, rel.Run = "coordinator", ref(s.RunIDs[len(s.RunIDs)-1])
		case s.OrcaTerminal != "" && !s.Interactive && owner[s.OrcaTerminal].ID != s.ID:
			rel.Role, rel.Parent = "nested", owner[s.OrcaTerminal].ID
		case s.OrcaTerminal != "":
			for _, w := range od.Workers {
				if w.Terminal != s.OrcaTerminal {
					continue
				}
				if rid := od.DispatchRun[w.DispatchID]; rid != "" {
					rel.Role, rel.Parent, rel.Run = "worker", coordOf[rid], ref(rid)
					break
				}
			}
		}
		out[s.ID] = rel
	}
	return out
}
