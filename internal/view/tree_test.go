package view

import (
	"testing"
	"time"

	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/workers"
)

func TestRelations(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	all := []state.Session{
		{ID: "coord", StartTS: at(0), OrcaTerminal: "term_c", Interactive: true, RunIDs: []string{"run_000000000001", "run_000000000002"}},
		{ID: "work1", StartTS: at(1), OrcaTerminal: "term_w1", Interactive: true},
		{ID: "work1-sdk", StartTS: at(2), OrcaTerminal: "term_w1", SDK: true},
		{ID: "work2", StartTS: at(3), OrcaTerminal: "term_w2", Interactive: true},
		{ID: "alone", StartTS: at(4), OrcaTerminal: "term_x", Interactive: true},
		{ID: "plain", StartTS: at(5)},
	}
	od := OrcaData{
		Workers: []workers.Worker{
			{DispatchID: "ctx_1", Terminal: "term_w1"},
			{DispatchID: "ctx_2", Terminal: "term_w2"},
		},
		DispatchRun: map[string]string{"ctx_1": "run_000000000002", "ctx_2": "run_00000000ffff"}, // second run has no known coordinator
	}
	obj := map[string]string{"run_000000000002": "ship the thing", "run_00000000ffff": "other thing"}
	got := Relations(all, od, obj)

	want := map[string]Rel{
		"coord":     {Role: "coordinator", Run: &RunRef{ID: "run_000000000002", Objective: "ship the thing"}},
		"work1":     {Role: "worker", Parent: "coord", Run: &RunRef{ID: "run_000000000002", Objective: "ship the thing"}},
		"work1-sdk": {Role: "nested", Parent: "work1"},
		"work2":     {Role: "worker", Run: &RunRef{ID: "run_00000000ffff", Objective: "other thing"}},
		"alone":     {Role: "standalone"},
		"plain":     {Role: "standalone"},
	}
	for id, w := range want {
		g := got[id]
		if g.Role != w.Role || g.Parent != w.Parent || (g.Run == nil) != (w.Run == nil) || (w.Run != nil && *g.Run != *w.Run) {
			t.Errorf("%s: got %+v run=%v, want %+v run=%v", id, g, g.Run, w, w.Run)
		}
	}
}

func TestRelationsWithoutOrca(t *testing.T) {
	all := []state.Session{
		{ID: "a", OrcaTerminal: "term_a", Interactive: true},
		{ID: "b", OrcaTerminal: "term_b", Interactive: true, RunIDs: []string{"run_000000000001"}},
	}
	got := Relations(all, OrcaData{}, nil)
	if got["a"].Role != "standalone" || got["b"].Role != "coordinator" || got["b"].Run.Objective != "" {
		t.Errorf("got %+v %+v", got["a"], got["b"])
	}
}
