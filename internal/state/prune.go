package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultMaxAge matches Claude Code's default cleanupPeriodDays (30 days),
// after which the transcripts are gone too.
const DefaultMaxAge = 720 * time.Hour

// OrphanAge is how old a ctx/otel file with no events file must be to go.
const OrphanAge = 24 * time.Hour

// Removed is one file pruned (or, in a dry run, that would be).
type Removed struct {
	Path string
	Size int64
}

// PruneResult reports what Prune did.
type PruneResult struct {
	Removed []Removed
	Bytes   int64
	Left    []string // entries ignored: unexpected names, non-regular files, errors
}

// pruneKinds are the per-session files: sub directory and extension.
var pruneKinds = []struct{ sub, ext string }{
	{"events", ".jsonl"},
	{"otel", ".jsonl"},
	{"ctx", ".json"},
}

type pruneFile struct {
	path  string
	size  int64
	mtime time.Time
}

// Prune deletes the files of sessions that went quiet. A session's last
// activity is the newest mtime among its events/otel/ctx files; all its files
// go when that is older than maxAge. A session with no events file (ctx written
// by the status line, otel without hooks) goes once it is older than OrphanAge
// (or maxAge, if shorter). workers.json, symlinks and files whose name is not a
// safe session id plus the expected extension are never touched. With dry set
// nothing is deleted.
func Prune(dir string, maxAge time.Duration, now time.Time, dry bool) PruneResult {
	var res PruneResult
	if maxAge <= 0 {
		return res
	}
	type session struct {
		files     []pruneFile
		hasEvents bool
	}
	sessions := map[string]*session{}
	for _, k := range pruneKinds {
		subdir := filepath.Join(dir, k.sub)
		ents, err := os.ReadDir(subdir)
		if err != nil {
			if !os.IsNotExist(err) {
				res.Left = append(res.Left, fmt.Sprintf("%s: %v", subdir, err))
			}
			continue
		}
		for _, e := range ents {
			p := filepath.Join(subdir, e.Name())
			id, ok := strings.CutSuffix(e.Name(), k.ext)
			if !ok || !SafeID(id) {
				res.Left = append(res.Left, p+": not a session file")
				continue
			}
			info, err := os.Lstat(p)
			if err != nil || !info.Mode().IsRegular() {
				res.Left = append(res.Left, p+": not a regular file")
				continue
			}
			s := sessions[id]
			if s == nil {
				s = &session{}
				sessions[id] = s
			}
			s.hasEvents = s.hasEvents || k.sub == "events"
			s.files = append(s.files, pruneFile{p, info.Size(), info.ModTime()})
		}
	}
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := sessions[id]
		var last time.Time
		for _, f := range s.files {
			if f.mtime.After(last) {
				last = f.mtime
			}
		}
		limit := maxAge
		if !s.hasEvents && OrphanAge < limit {
			limit = OrphanAge
		}
		if now.Sub(last) <= limit {
			continue
		}
		for _, f := range s.files {
			if !dry {
				if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
					res.Left = append(res.Left, fmt.Sprintf("%s: %v", f.path, err))
					continue
				}
			}
			res.Removed = append(res.Removed, Removed{f.path, f.size})
			res.Bytes += f.size
		}
	}
	return res
}

// PruneLoop prunes once immediately, then every interval, until stop closes.
// logf is called with one summary line whenever something was removed.
func PruneLoop(stop <-chan struct{}, dir string, maxAge, every time.Duration, now func() time.Time, logf func(string, ...any)) {
	run := func() {
		if r := Prune(dir, maxAge, now(), false); len(r.Removed) > 0 {
			logf("ccwatch serve: pruned %d files (%d bytes) older than %s from %s", len(r.Removed), r.Bytes, maxAge, dir)
		}
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			run()
		}
	}
}
