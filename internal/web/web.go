// Package web serves the dashboard page and its JSON/SSE APIs from `ccwatch serve`.
package web

import (
	"crypto/sha1"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/view"
)

//go:embed static
var staticFS embed.FS

const pollEvery = 500 * time.Millisecond

// SessionInfo is one row of /api/sessions.
type SessionInfo struct {
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
	Ended     bool   `json:"ended"`
	Subagents int    `json:"subagents"`
	Live      int    `json:"live"`
	State     string `json:"state"` // ended | running (live agents) | active
	Label     string `json:"label"` // picker text: id · state · agents · model · dir
	Updated   string `json:"updated"`
}

// Server holds what the handlers need.
type Server struct {
	Src        *view.Source
	AllowHosts map[string]bool // lower-case host names allowed in the Host header; nil = any
}

// NewServer builds a Server. addr is the listen address; when it is a loopback
// address only loopback host names are accepted (DNS-rebinding guard).
func NewServer(src *view.Source, addr string) *Server {
	s := &Server{Src: src}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
		s.AllowHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, strings.ToLower(host): true}
	}
	return s
}

func (s *Server) hostOK(r *http.Request) bool {
	if s.AllowHosts == nil {
		return true
	}
	h := r.Host
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return s.AllowHosts[strings.ToLower(strings.Trim(h, "[]"))]
}

// Wrap routes the UI and API paths to the web handlers and everything else
// (OTLP, whatever it was) to next unchanged.
func (s *Server) Wrap(next http.Handler) http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sessions", s.sessions)
	mux.HandleFunc("/api/state", s.state)
	mux.HandleFunc("/api/stream", s.stream)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isUI := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			(r.URL.Path == "/" || r.URL.Path == "/app.js" || r.URL.Path == "/style.css" || strings.HasPrefix(r.URL.Path, "/api/"))
		if !isUI {
			next.ServeHTTP(w, r)
			return
		}
		if !s.hostOK(r) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) listSessions() []SessionInfo {
	ids := state.Sessions(s.Src.Dir)
	out := make([]SessionInfo, 0, len(ids))
	for _, id := range ids {
		ss := state.Load(s.Src.Dir, id)
		info := SessionInfo{ID: id, Cwd: ss.Cwd, Model: ss.Model, Ended: ss.Ended, Subagents: len(ss.Subagents), Live: ss.Live()}
		info.State, info.Label = sessionLabel(id, ss)
		if t := state.ModTime(s.Src.Dir, id); !t.IsZero() {
			info.Updated = t.UTC().Format(time.RFC3339)
		}
		out = append(out, info)
	}
	return out
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.listSessions())
}

// resolve maps ?session= to an id: empty or "latest" is the newest session.
func (s *Server) resolve(q url.Values) (string, int, string) {
	id := q.Get("session")
	if id == "" || id == "latest" {
		ids := state.Sessions(s.Src.Dir)
		if len(ids) == 0 {
			return "", http.StatusNotFound, "no sessions yet"
		}
		return ids[0], 0, ""
	}
	if !state.SafeID(id) {
		return "", http.StatusBadRequest, "bad session id"
	}
	if state.Signature(s.Src.Dir, id) == [2]int64{} {
		return "", http.StatusNotFound, "unknown session"
	}
	return id, 0, ""
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	id, code, msg := s.resolve(r.URL.Query())
	if code != 0 {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, s.Src.Dashboard(id))
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	fl.Flush()

	var lastState, lastList [20]byte
	var lastSig [2]int64
	lastID := ""
	send := func(event string, v any, last *[20]byte) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return true
		}
		sum := sha1.Sum(b)
		if sum == *last {
			return true
		}
		*last = sum
		if _, err := w.Write([]byte("event: " + event + "\ndata: " + string(b) + "\n\n")); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	tk := time.NewTicker(pollEvery)
	defer tk.Stop()
	for n := 0; ; n++ {
		id, code, _ := s.resolve(q)
		if code == 0 {
			sig := state.Signature(s.Src.Dir, id)
			// files change on every event; Orca / agent files are re-read at 1 s and then every 5 s
			if id != lastID || sig != lastSig || n == 2 || n%10 == 0 { // n == 2: pick up the first Orca answer quickly
				lastID, lastSig = id, sig
				if !send("state", s.Src.Dashboard(id), &lastState) {
					return
				}
			}
		}
		if n%2 == 0 {
			l := s.listSessions()
			if !send("sessions", l, &lastList) {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-tk.C:
		}
	}
}

// sessionLabel builds the picker text, state first so a narrow select never cuts it:
// "<id8> · <ended|active|running> · <agents> · <model> · <dir>".
func sessionLabel(id string, ss state.Session) (string, string) {
	total, live := len(ss.Subagents), ss.Live()
	st := "active"
	switch {
	case ss.Ended:
		st = "ended"
	case live > 0:
		st = "running"
	}
	agents := "no agents"
	switch {
	case live > 0:
		agents = fmt.Sprintf("%d of %s running", live, view.Plural(total, "agent"))
	case total > 0:
		agents = view.Plural(total, "agent") + " done"
	}
	model := ss.Model
	if model == "" {
		model = "?"
	}
	if len(id) > 8 {
		id = id[:8]
	}
	dir := strings.TrimRight(ss.Cwd, "/")
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		dir = dir[i+1:]
	}
	parts := []string{id, st, agents, model}
	if dir != "" {
		parts = append(parts, dir)
	}
	return st, strings.Join(parts, " · ")
}
