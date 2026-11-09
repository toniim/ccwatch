// Package otlp implements the OTLP/HTTP JSON logs receiver used by `ccwatch serve`.
package otlp

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/toniim/ccwatch/internal/state"
)

var dropKey = regexp.MustCompile(`^(user\.|organization\.|anthropic\.)`)

var keepEvents = map[string]bool{"api_request": true, "subagent_completed": true}

type anyValue struct {
	StringValue *string         `json:"stringValue"`
	IntValue    json.RawMessage `json:"intValue"`
	DoubleValue *float64        `json:"doubleValue"`
	BoolValue   *bool           `json:"boolValue"`
}

type logsReq struct {
	ResourceLogs []struct {
		ScopeLogs []struct {
			LogRecords []struct {
				Body struct {
					StringValue string `json:"stringValue"`
				} `json:"body"`
				Attributes []struct {
					Key   string   `json:"key"`
					Value anyValue `json:"value"`
				} `json:"attributes"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

func (v anyValue) value() (any, bool) {
	switch {
	case v.StringValue != nil:
		return *v.StringValue, true
	case v.DoubleValue != nil:
		return *v.DoubleValue, true
	case v.BoolValue != nil:
		return *v.BoolValue, true
	case len(v.IntValue) > 0:
		s := strings.Trim(string(v.IntValue), `"`) // OTLP JSON may send int64 as a string
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
	}
	return nil, false
}

// Flatten extracts the kept records from an OTLP logs body, grouped by session id.
func Flatten(body []byte) map[string][]state.Record {
	var req logsReq
	out := map[string][]state.Record{}
	if json.Unmarshal(body, &req) != nil {
		return out
	}
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				name := strings.TrimPrefix(lr.Body.StringValue, "claude_code.")
				if !keepEvents[name] {
					continue
				}
				attrs := map[string]any{}
				for _, a := range lr.Attributes {
					if dropKey.MatchString(a.Key) {
						continue
					}
					if v, ok := a.Value.value(); ok {
						attrs[a.Key] = v
					}
				}
				sid, _ := attrs["session.id"].(string)
				if !state.SafeID(sid) {
					continue
				}
				out[sid] = append(out[sid], state.Record{Event: name, Attrs: attrs})
			}
		}
	}
	return out
}

// Handler serves /v1/logs and /v1/metrics, always answering 200 {}.
func Handler(dir string) http.Handler {
	var mu sync.Mutex
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}
	mux.HandleFunc("/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		defer reply(w)
		body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			return
		}
		groups := Flatten(body)
		if len(groups) == 0 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_ = os.MkdirAll(filepath.Join(dir, "otel"), 0o755)
		for sid, recs := range groups {
			f, err := os.OpenFile(filepath.Join(dir, "otel", sid+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				continue
			}
			for _, rec := range recs {
				if b, err := json.Marshal(rec); err == nil {
					_, _ = f.Write(append(b, '\n'))
				}
			}
			_ = f.Close()
		}
	})
	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 8<<20))
		reply(w)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { reply(w) })
	return mux
}
