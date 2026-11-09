// Package tui renders the live ccwatch view with bubbletea.
package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/toniim/ccwatch/internal/line"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/view"
	"github.com/toniim/ccwatch/internal/workers"
)

const refreshEvery = 500 * time.Millisecond

var (
	bold = lipgloss.NewStyle().Bold(true)
	dim  = lipgloss.NewStyle().Faint(true)
	head = lipgloss.NewStyle().Bold(true).Underline(true)
)

// Model is the bubbletea model.
type Model struct {
	src    *view.Source
	ids    []string
	cur    string
	follow bool // follow the newest session until the user presses tab
	sess   state.Session
	dash   view.Dashboard
	sig    [2]int64
	table  bool // t toggles the Phase 1 table view
	all    bool // --all-workers
	frame  int
	width  int
	height int
}

// New builds a model. session pins an id; empty means follow the latest.
func New(src *view.Source, session string, allWorkers bool) *Model {
	m := &Model{src: src, all: allWorkers, width: 120, follow: session == "", cur: session}
	m.reload(true)
	return m
}

func (m *Model) reload(force bool) {
	m.ids = state.Sessions(m.src.Dir)
	if m.follow && len(m.ids) > 0 {
		m.cur = m.ids[0]
	}
	if m.cur == "" {
		return
	}
	sig := state.Signature(m.src.Dir, m.cur)
	// Orca data arrives asynchronously, so rebuild the view model on every tick
	// but only re-read the files when their sizes changed.
	if force || sig != m.sig {
		m.sig = sig
		m.sess = state.Load(m.src.Dir, m.cur)
	}
	m.dash = m.src.Build(m.sess)
}

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return tick() }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "t":
			m.table = !m.table
		case "tab":
			if len(m.ids) > 0 {
				m.follow = false
				i := 0
				for j, id := range m.ids {
					if id == m.cur {
						i = j + 1
					}
				}
				m.cur = m.ids[i%len(m.ids)]
				m.reload(true)
			}
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		m.frame++
		m.reload(false)
		return m, tick()
	}
	return m, nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.cur == "" {
		return bold.Render("ccwatch") + " — no sessions under " + m.src.Dir + "\n" + dim.Render("q quit") + "\n"
	}
	if m.table {
		return Table(m.sess, m.src.Workers(m.sess), m.src.OrcaOn, m.all, m.width, indexOf(m.ids, m.cur)+1, len(m.ids))
	}
	d := m.dash
	return Dashboard(d, Options{Width: m.width, Height: m.height, Frame: m.frame})
}

func trunc(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func pad(s string, n int) string {
	s = trunc(s, n)
	return s + strings.Repeat(" ", n-len([]rune(s)))
}

func short(id string, n int) string {
	if len(id) > n {
		return id[:n]
	}
	return id
}

func dur(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// Table renders the Phase 1 table view.
func Table(s state.Session, ws []workers.Worker, haveOrca, allWorkers bool, width, pos, total int) string {
	w := width
	if w < 80 {
		w = 80
	}
	var b strings.Builder
	st := "active"
	if s.Ended {
		st = "ended"
	}
	fmt.Fprintf(&b, "%s  session %s (%d of %d, %s)\n", bold.Render("ccwatch"), short(s.ID, 8), pos, total, st)
	fmt.Fprintf(&b, "cwd %s\n", trunc(s.Cwd, w-4))
	model := view.ShortModel(s.Model)
	if model == "" {
		model = "?"
	}
	fmt.Fprintf(&b, "main %s | ctx %s | cost $%.4f\n\n", model, line.Tokens(s.MainCtx), s.CostUSD)

	fmt.Fprintf(&b, "%s\n", head.Render(fmt.Sprintf("Subagents (%d, %d live)", len(s.Subagents), s.Live())))
	const wID, wType, wDesc, wModel, wState, wTok, wPeak, wDur = 8, 16, 18, 16, 8, 8, 8, 7
	fixed := wID + wType + wDesc + wModel + wState + wTok + wPeak + wDur + 8
	wSum := w - fixed
	fmt.Fprintf(&b, "%s %s %s %s %s %s %s %s %s\n", pad("ID", wID), pad("TYPE", wType), pad("DESCRIPTION", wDesc),
		pad("MODEL", wModel), pad("STATE", wState), pad("TOKENS", wTok), pad("PEAK", wPeak), pad("TIME", wDur), "SUMMARY")
	if len(s.Subagents) == 0 {
		b.WriteString(dim.Render("  none") + "\n")
	}
	for _, a := range s.Subagents {
		stt := "done"
		switch {
		case a.Failed:
			stt = "failed"
		case a.Running:
			stt = "running"
		}
		desc := a.Description
		if a.Background {
			desc += " (bg)"
		}
		fmt.Fprintf(&b, "%s %s %s %s %s %s %s %s %s\n", pad(short(a.ID, wID), wID), pad(a.Type, wType), pad(desc, wDesc),
			pad(view.ShortModel(a.Model), wModel), pad(stt, wState), pad(line.Tokens(a.Tokens), wTok), pad(line.Tokens(a.Peak), wPeak), pad(dur(a.DurationMs), wDur),
			trunc(a.Summary, wSum))
	}
	b.WriteString("\n")

	if haveOrca {
		shown := ws
		if !allWorkers {
			shown = workers.Shown(ws, s.OrcaTerminal)
		}
		fmt.Fprintf(&b, "%s\n", head.Render(fmt.Sprintf("Orca workers (%d shown of %d)", len(shown), len(ws))))
		if len(shown) == 0 {
			b.WriteString(dim.Render("  none") + "\n")
		} else {
			shown = append([]workers.Worker(nil), shown...)
			sort.SliceStable(shown, func(i, j int) bool { return shown[i].Running() && !shown[j].Running() })
			if len(shown) > 8 {
				shown = shown[:8]
			}
			fmt.Fprintf(&b, "%s %s %s %s\n", pad("DISPATCH", 16), pad("STATE", 10), pad("LIVENESS", 12), "WORKSPACE")
			for _, wk := range shown {
				mark := ""
				if s.OrcaTerminal != "" && wk.Terminal == s.OrcaTerminal {
					mark = "  ◀ this session"
				}
				fmt.Fprintf(&b, "%s %s %s %s%s\n", pad(wk.DispatchID, 16), pad(wk.State, 10), pad(wk.Liveness, 12), wk.Name(), mark)
			}
		}
		b.WriteString("\n")
	}

	lev := "-"
	if s.ReturnedEst > 0 {
		lev = fmt.Sprintf("×%.0f", s.Leverage())
	}
	fmt.Fprintf(&b, "subagents processed %s | peak ctx %s | returned est. %s | leverage %s | tests %d/%d | tool failures %d\n",
		line.Tokens(s.Processed), line.Tokens(s.PeakCtx), line.Tokens(s.ReturnedEst), lev, s.TestsPass, s.TestsPass+s.TestsFail, s.ToolFailures)
	b.WriteString(dim.Render("t dashboard · tab next session · q quit") + "\n")
	return b.String()
}

func indexOf(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return 0
}

// Once returns one rendered frame (dashboard, or the table when table is set).
func Once(src *view.Source, session string, width int, table, allWorkers bool) string {
	src.Sync = true
	m := New(src, session, allWorkers)
	m.width, m.table = width, table
	return m.View()
}

// ToggleTable flips between the dashboard and the table view.
func (m *Model) ToggleTable() { m.table = !m.table }
