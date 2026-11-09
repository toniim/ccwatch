package state

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var pruneNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func put(t *testing.T, dir, sub, name string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, sub, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := pruneNow.Add(-age)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestPruneOldSessionRemovedWholly(t *testing.T) {
	d := t.TempDir()
	old := 800 * time.Hour
	files := []string{put(t, d, "events", "s1.jsonl", old), put(t, d, "otel", "s1.jsonl", old), put(t, d, "ctx", "s1.json", old)}
	w := put(t, d, "", "workers.json", old)
	r := Prune(d, DefaultMaxAge, pruneNow, false)
	if len(r.Removed) != 3 || r.Bytes != 15 {
		t.Fatalf("removed %+v", r)
	}
	for _, f := range files {
		if exists(f) {
			t.Errorf("%s still there", f)
		}
	}
	if !exists(w) {
		t.Error("workers.json removed")
	}
}

func TestPruneKeepsSessionWithFreshFile(t *testing.T) {
	d := t.TempDir()
	e := put(t, d, "events", "s1.jsonl", 800*time.Hour)
	o := put(t, d, "otel", "s1.jsonl", time.Hour)
	if r := Prune(d, DefaultMaxAge, pruneNow, false); len(r.Removed) != 0 {
		t.Fatalf("removed %+v", r.Removed)
	}
	if !exists(e) || !exists(o) {
		t.Error("session files lost")
	}
}

func TestPruneOrphans(t *testing.T) {
	d := t.TempDir()
	oldCtx := put(t, d, "ctx", "a.json", 25*time.Hour)
	oldOtel := put(t, d, "otel", "b.jsonl", 30*time.Hour)
	young := put(t, d, "ctx", "c.json", 23*time.Hour)
	r := Prune(d, DefaultMaxAge, pruneNow, false)
	if len(r.Removed) != 2 || exists(oldCtx) || exists(oldOtel) || !exists(young) {
		t.Fatalf("removed %+v", r.Removed)
	}
}

func TestPruneLeavesForeignNamesAndSymlinks(t *testing.T) {
	d := t.TempDir()
	old := 900 * time.Hour
	odd := []string{
		put(t, d, "events", "bad name.jsonl", old),
		put(t, d, "events", "s1.txt", old),
		put(t, d, "ctx", "s1.abc123.tmp", old),
	}
	target := put(t, d, "", "outside.json", old)
	link := filepath.Join(d, "ctx", "lnk.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	r := Prune(d, DefaultMaxAge, pruneNow, false)
	if len(r.Removed) != 0 {
		t.Fatalf("removed %+v", r.Removed)
	}
	if len(r.Left) != 4 {
		t.Errorf("left %v", r.Left)
	}
	for _, p := range append(odd, target, link) {
		if !exists(p) {
			t.Errorf("%s was removed", p)
		}
	}
}

func TestPruneDryRun(t *testing.T) {
	d := t.TempDir()
	p := put(t, d, "events", "s1.jsonl", 800*time.Hour)
	r := Prune(d, DefaultMaxAge, pruneNow, true)
	if len(r.Removed) != 1 || !exists(p) {
		t.Fatalf("dry run: %+v exists=%v", r.Removed, exists(p))
	}
}

func TestPruneLoopRunsAtStart(t *testing.T) {
	d := t.TempDir()
	p := put(t, d, "events", "s1.jsonl", 800*time.Hour)
	stop := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	var lines []string
	go func() {
		PruneLoop(stop, d, DefaultMaxAge, time.Hour, func() time.Time { return pruneNow }, func(f string, a ...any) {
			mu.Lock()
			lines = append(lines, f)
			mu.Unlock()
		})
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for exists(p) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if exists(p) || len(lines) != 1 {
		t.Fatalf("exists=%v lines=%v", exists(p), lines)
	}
}
