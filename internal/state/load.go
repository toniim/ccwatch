package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func readLines(path string, fn func([]byte)) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			fn(line)
		}
		if err != nil {
			return
		}
	}
}

// LoadEvents reads events/<id>.jsonl, skipping malformed lines.
func LoadEvents(dir, id string) []Event {
	var out []Event
	readLines(filepath.Join(dir, "events", id+".jsonl"), func(b []byte) {
		var e Event
		if json.Unmarshal(b, &e) == nil {
			out = append(out, e)
		}
	})
	return out
}

// LoadOtel reads otel/<id>.jsonl, skipping malformed lines.
func LoadOtel(dir, id string) []Record {
	var out []Record
	readLines(filepath.Join(dir, "otel", id+".jsonl"), func(b []byte) {
		var r Record
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	})
	return out
}

// Load reduces the stored state of one session.
func Load(dir, id string) Session {
	s := Build(LoadEvents(dir, id), LoadOtel(dir, id))
	if s.ID == "" {
		s.ID = id
	}
	s.CtxWindow = LoadCtxWindow(dir, id)
	return s
}

// SaveCtxWindow records a session's context_window_size (from the statusLine
// JSON) in <dir>/ctx/<id>.json. It skips the write when the value is unchanged
// and writes atomically (temp file + rename).
func SaveCtxWindow(dir, id string, size int64) {
	if size <= 0 || !SafeID(id) || LoadCtxWindow(dir, id) == size {
		return
	}
	sub := filepath.Join(dir, "ctx")
	if os.MkdirAll(sub, 0o755) != nil {
		return
	}
	b, _ := json.Marshal(map[string]int64{"size": size})
	tmp, err := os.CreateTemp(sub, id+".*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), filepath.Join(sub, id+".json")) != nil {
		_ = os.Remove(tmp.Name())
	}
}

// LoadCtxWindow returns the recorded context window size, 0 when unknown.
func LoadCtxWindow(dir, id string) int64 {
	if !SafeID(id) {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(dir, "ctx", id+".json"))
	if err != nil {
		return 0
	}
	var v struct {
		Size int64 `json:"size"`
	}
	if json.Unmarshal(b, &v) != nil || v.Size < 0 {
		return 0
	}
	return v.Size
}

// Sessions lists session ids found under dir (events/ and otel/), newest first.
func Sessions(dir string) []string {
	type sid struct {
		id string
		t  int64
	}
	seen := map[string]int64{}
	for _, sub := range []string{"events", "otel"} {
		ents, _ := os.ReadDir(filepath.Join(dir, sub))
		for _, e := range ents {
			name := e.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			if t := info.ModTime().UnixNano(); t > seen[id] {
				seen[id] = t
			}
		}
	}
	var l []sid
	for id, t := range seen {
		l = append(l, sid{id, t})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].t > l[j].t })
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = s.id
	}
	return out
}

// Signature is a cheap change detector: the byte sizes of a session's files.
func Signature(dir, id string) [2]int64 {
	var sig [2]int64
	for i, sub := range []string{"events", "otel"} {
		if info, err := os.Stat(filepath.Join(dir, sub, id+".jsonl")); err == nil {
			sig[i] = info.Size()
		}
	}
	return sig
}

// ModTime is the newest modification time of a session's files (zero if none).
func ModTime(dir, id string) time.Time {
	var t time.Time
	for _, sub := range []string{"events", "otel"} {
		if info, err := os.Stat(filepath.Join(dir, sub, id+".jsonl")); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
	}
	return t
}
