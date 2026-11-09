// Package agents reads Claude Code agent definitions and the hook registration
// summary from settings.json. Both are read-only.
package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Def is one agent definition file.
type Def struct {
	Name    string   `json:"name"`
	File    string   `json:"file"` // base name, e.g. explorer.md
	Model   string   `json:"model"`
	Tools   []string `json:"tools"`
	Builtin bool     `json:"builtin"`
}

// ParseFrontmatter extracts name, model and tools from a markdown agent file.
// tools may be a YAML flow list [A, B], a comma string, or a block list.
func ParseFrontmatter(src string) (Def, bool) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Def{}, false
	}
	var d Def
	inTools := false
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			return d, d.Name != ""
		}
		if inTools {
			if t := strings.TrimSpace(l); strings.HasPrefix(t, "- ") {
				d.Tools = append(d.Tools, unquote(strings.TrimPrefix(t, "- ")))
				continue
			}
			inTools = false
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok || strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "name":
			d.Name = unquote(v)
		case "model":
			d.Model = unquote(v)
		case "tools":
			if v == "" {
				inTools = true
				continue
			}
			v = strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
			for _, t := range strings.Split(v, ",") {
				if t = unquote(strings.TrimSpace(t)); t != "" {
					d.Tools = append(d.Tools, t)
				}
			}
		}
	}
	return Def{}, false // no closing ---
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// LoadDirs reads *.md agent files from each dir; later dirs do not override earlier ones.
func LoadDirs(dirs ...string) map[string]Def {
	out := map[string]Def{}
	for _, dir := range dirs {
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if d, ok := ParseFrontmatter(string(b)); ok {
				d.File = e.Name()
				if _, dup := out[d.Name]; !dup {
					out[d.Name] = d
				}
			}
		}
	}
	return out
}

// HookInfo summarises hook registrations in a settings.json.
type HookInfo struct {
	Path    string         `json:"path"`
	Counts  map[string]int `json:"counts"`
	Ccwatch bool           `json:"ccwatch"` // a registered command mentions ccwatch
	Found   bool           `json:"found"`
}

// Events returns event names sorted.
func (h HookInfo) Events() []string {
	var ev []string
	for k := range h.Counts {
		ev = append(ev, k)
	}
	sort.Strings(ev)
	return ev
}

// LoadHooks reads settings.json and counts hook handlers per event.
func LoadHooks(path string) HookInfo {
	info := HookInfo{Path: path, Counts: map[string]int{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return info
	}
	var st struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if json.Unmarshal(b, &st) != nil {
		return info
	}
	info.Found = true
	for ev, groups := range st.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				info.Counts[ev]++
				if strings.Contains(h.Command, "ccwatch") {
					info.Ccwatch = true
				}
			}
		}
	}
	return info
}
