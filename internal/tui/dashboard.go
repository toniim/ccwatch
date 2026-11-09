package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/toniim/ccwatch/internal/view"
)

// Options controls one dashboard frame.
type Options struct {
	Width  int
	Height int            // 0 = unlimited; a taller frame sheds log rows, then lane borders
	Frame  int            // spinner frame
	Loc    *time.Location // log timestamps; default time.Local
}

var (
	blue   = lipgloss.NewStyle().Foreground(lipgloss.Color("69"))
	purple = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	orange = lipgloss.NewStyle().Foreground(lipgloss.Color("215"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	grey   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	strong = lipgloss.NewStyle().Bold(true)
)

const spinner = "|/-\\"

func spin(frame int) string { return string(spinner[frame%len(spinner)]) }

func cw(s string) int { return ansi.StringWidth(s) }

// fit truncates s to w cells (with an ellipsis) and pads it with spaces.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if cw(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-cw(s))
}

func center(s string, w int) string {
	if cw(s) >= w {
		return fit(s, w)
	}
	l := (w - cw(s)) / 2
	return strings.Repeat(" ", l) + s + strings.Repeat(" ", w-cw(s)-l)
}

type boxStyle struct {
	tl, tr, bl, br, h, v string
	color                lipgloss.Style
}

var (
	solid  = func(c lipgloss.Style) boxStyle { return boxStyle{"┌", "┐", "└", "┘", "─", "│", c} }
	dashed = func(c lipgloss.Style) boxStyle { return boxStyle{"┌", "┐", "└", "┘", "╌", "╎", c} }
)

// box draws a titled box exactly w cells wide around body lines.
func box(title, right string, body []string, w int, st boxStyle) []string {
	inner := w - 4
	top := st.tl + st.h
	if title != "" {
		top += " " + title + " "
	}
	tr := ""
	if right != "" {
		tr = " " + right + " "
	}
	fill := w - cw(top) - cw(tr) - 1
	if fill < 0 {
		top = ansi.Truncate(top, w-cw(tr)-1, "…")
		fill = w - cw(top) - cw(tr) - 1
		if fill < 0 {
			fill = 0
		}
	}
	out := []string{st.color.Render(top + strings.Repeat(st.h, fill) + tr + st.tr)}
	for _, l := range body {
		out = append(out, st.color.Render(st.v)+" "+fit(l, inner)+" "+st.color.Render(st.v))
	}
	out = append(out, st.color.Render(st.bl+strings.Repeat(st.h, w-2)+st.br))
	return out
}

func bar(frac float64, w int) string {
	if w < 1 {
		return ""
	}
	n := int(frac*float64(w) + 0.5)
	if n > w {
		n = w
	}
	return strings.Repeat("█", n) + strings.Repeat("░", w-n)
}

func tok(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func slotGlyph(s string, frame int) string {
	switch s {
	case "launched":
		return grey.Render("□")
	case "running":
		return orange.Render("▣")
	case "failed":
		return red.Render("✗")
	}
	return green.Render("■")
}

func markGlyph(m string, frame int) string {
	switch m {
	case "✓":
		return green.Render("✓")
	case "✗":
		return red.Render("✗")
	case "|":
		return orange.Render(spin(frame))
	}
	return " "
}

func agentsPanel(d view.Dashboard, w int) []string {
	var b []string
	if len(d.Agents) == 0 {
		b = append(b, grey.Render("no subagent types used yet"))
	}
	for _, a := range d.Agents {
		if a.Builtin {
			b = append(b, orange.Render("├ "+strong.Render(a.Type))+grey.Render("  built-in"))
			b = append(b, grey.Render("    model: ")+a.Model)
			continue
		}
		b = append(b, orange.Render("├ "+strong.Render(strings.TrimSuffix(a.File, ".md")+".md"))+grey.Render("  ("+a.Type+")"))
		b = append(b, grey.Render("    model: ")+a.Model)
		if len(a.Tools) > 0 {
			b = append(b, grey.Render("    tools: ")+strings.Join(a.Tools, ", "))
		}
	}
	b = append(b, "")
	b = append(b, green.Render("settings.json"))
	hl := wrapWords(d.HooksLine, w-len("hooks: "))
	for i, l := range hl {
		prefix := strings.Repeat(" ", len("hooks: "))
		if i == 0 {
			prefix = grey.Render("hooks: ")
		}
		b = append(b, prefix+l)
	}
	switch {
	case d.Ccwatch:
		b = append(b, grey.Render("ccwatch hooks: ")+"present")
	default:
		b = append(b, grey.Render("ccwatch hooks: not in settings.json"))
	}
	return b
}

// wrapWords breaks space-separated words into lines of at most w cells (max 3 lines).
func wrapWords(s string, w int) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case cw(cur)+1+cw(word) <= w:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) > 3 {
		lines = append(lines[:2], lines[2]+" …")
	}
	return lines
}

func mainPanel(d view.Dashboard, o Options, inner int) []string {
	model := strings.ToUpper(d.Model)
	if model == "" {
		model = "MAIN"
	}
	var b []string
	b = append(b, center(purple.Render(strong.Render(model+" · main session")), inner))
	st := d.State
	if st == "orchestrating" || st == "working" {
		st += " [ " + spin(o.Frame) + " ]"
	} else {
		st += " [   ]"
	}
	b = append(b, center(st, inner))
	if d.WorkerLine != "" {
		b = append(b, grey.Render(d.WorkerLine))
	}
	if d.NestedLine != "" {
		b = append(b, grey.Render(d.NestedLine))
	}
	b = append(b, "")
	if d.PlanSource == "orca run" {
		b = append(b, grey.Render("orca run: "+d.PlanTitle))
	}
	if len(d.Plan) == 0 {
		b = append(b, grey.Render("no subagent launches yet"))
	}
	for i, p := range d.Plan {
		if i == 6 {
			b = append(b, grey.Render(fmt.Sprintf("    … +%d more", len(d.Plan)-6)))
			break
		}
		line := "[" + markGlyph(p.Mark, o.Frame) + "] " + p.Text
		if p.Detail != "" {
			line += grey.Render(" -> ") + p.Detail
		}
		b = append(b, line)
	}
	b = append(b, "")
	ctxLine := grey.Render("main context")
	val := green.Render(tok(d.Ctx) + " tok")
	b = append(b, ctxLine+strings.Repeat(" ", max1(inner-cw(ctxLine)-cw(val)))+val)
	if d.CtxKnown {
		b = append(b, bar(float64(d.Ctx)/float64(d.CtxSize), inner-5)+fmt.Sprintf(" %3d%%", d.CtxPct))
		b = append(b, grey.Render(fmt.Sprintf("of %s window · cost $%.4f", tok(d.CtxSize), d.CostUSD)))
	} else {
		b = append(b, grey.Render(fmt.Sprintf("window unknown · cost $%.4f", d.CostUSD)))
	}
	return b
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func laneBody(l view.Lane, w int, frame int, narrow bool) []string {
	inner := w - 4
	nameW, barW := 18, 22
	if narrow {
		barW = 10
	}
	slotsW := inner - nameW - barW - 5 - 3
	var slots []string
	for i, s := range l.Slots {
		if (i+1)*2 > slotsW {
			slots = append(slots, "…")
			break
		}
		slots = append(slots, slotGlyph(s, frame))
	}
	l1 := fit(strong.Render(l.Name), nameW) + fit(strings.Join(slots, " "), slotsW) + "   " +
		bar(float64(l.Pct)/100, barW) + fmt.Sprintf(" %3d%%", l.Pct)
	l2 := fit(blue.Render(l.Model), nameW) + fit(grey.Render("○ ")+l.Status, slotsW)
	return []string{l1, l2}
}

func laneCompact(l view.Lane, w int, frame int) string {
	var slots []string
	for _, s := range l.Slots {
		slots = append(slots, slotGlyph(s, frame))
	}
	return fit(strong.Render(l.Name)+" "+blue.Render(l.Model)+"  "+strings.Join(slots, " ")+"  "+fmt.Sprintf("%d%% ", l.Pct)+l.Status, w)
}

// Dashboard renders the dashboard view at o.Width.
func Dashboard(d view.Dashboard, o Options) string {
	if o.Width < 60 {
		o.Width = 60
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	logRows := view.LogRows
	compact := false
	for {
		out := dashboardLines(d, o, logRows, compact)
		if o.Height <= 0 || len(out) <= o.Height {
			return strings.Join(out, "\n") + "\n"
		}
		switch {
		case logRows > 3:
			logRows--
		case !compact:
			compact = true
		default:
			return strings.Join(out[:o.Height], "\n") + "\n"
		}
	}
}

func dashboardLines(d view.Dashboard, o Options, logRows int, compact bool) []string {
	w := o.Width
	var out []string
	status := "active"
	if d.Ended {
		status = "ended"
	}
	out = append(out, strong.Render("ccwatch")+fmt.Sprintf("  session %s (%s)  cwd %s", short(d.SessionID, 8), status, d.Cwd))
	out[0] = strings.TrimRight(fit(out[0], w), " ")

	// top panels
	if w >= 110 {
		lw := w * 2 / 5
		rw := w - lw - 1
		lb, rb := agentsPanel(d, lw-4), mainPanel(d, o, rw-4)
		for len(lb) < len(rb) {
			lb = append(lb, "")
		}
		for len(rb) < len(lb) {
			rb = append(rb, "")
		}
		left := box(".claude/agents/", "[ /agents ]", lb, lw, solid(blue))
		right := box("", "", rb, rw, dashed(purple))
		out = append(out, joinCols(left, lw, right)...)
	} else {
		out = append(out, box(".claude/agents/", "[ /agents ]", agentsPanel(d, w-4), w, solid(blue))...)
		out = append(out, box("", "", mainPanel(d, o, w-4), w, dashed(purple))...)
	}

	addLane := func(l view.Lane) {
		if compact {
			out = append(out, laneCompact(l, w, o.Frame))
			return
		}
		out = append(out, box("", "", laneBody(l, w, o.Frame, w < 110), w, solid(grey))...)
	}
	if len(d.Lanes) > 0 || d.OrcaLane == nil {
		out = append(out, center(grey.Render(fmt.Sprintf("» Task tool · %s · each in its own context window «", view.Plural(d.Subagents, "subagent"))), w))
		if len(d.Lanes) == 0 {
			out = append(out, center(grey.Render("no lanes yet"), w))
		}
		for _, l := range d.Lanes {
			addLane(l)
		}
	}
	if d.OrcaLane != nil {
		out = append(out, center(grey.Render(d.OrcaHeader), w))
		addLane(*d.OrcaLane)
	}

	// back to main
	model := d.Model
	if model == "" {
		model = "main"
	}
	bw := w - 4 - 24
	back := []string{
		fit(grey.Render("subagents processed"), 22) + fit(blue.Render(tok(d.Back.Processed)+" tok"), 12) + bar(d.Back.ProcFrac, bw-34),
		fit(grey.Render("main received ≈"), 22) + fit(green.Render(tok(d.Back.Received)+" tok"), 12) + bar(d.Back.RecvFrac, bw-34),
	}
	lev := "-"
	if d.Back.Received > 0 {
		lev = fmt.Sprintf("×%.0f", d.Leverage)
	}
	back = append(back, grey.Render("bars use a log scale · leverage "+lev+" · peak subagent ctx "+tok(d.PeakCtx)))
	out = append(out, box("back to "+model+" · summaries only", "[ log scale ]", back, w, dashed(green))...)

	// session log
	live := "live"
	if d.Ended {
		live = "ended"
	}
	var logBody []string
	rows := d.Log
	if len(rows) > logRows {
		rows = rows[len(rows)-logRows:]
	}
	if len(rows) == 0 {
		logBody = append(logBody, grey.Render("no events yet"))
	}
	for _, r := range rows {
		who := r.Who
		var text string
		switch r.Kind {
		case "you":
			who = strong.Render(who)
		case "model":
			who = purple.Render(who)
		case "test":
			text = r.Tag + "  " + r.Text
			if r.Tag == "fail" {
				who = red.Render(who)
			} else {
				who = green.Render(who)
			}
		default:
			who = orange.Render(who)
		}
		if text == "" {
			text = r.Text
		}
		whoW := 22
		if w < 110 {
			whoW = 20
		}
		logBody = append(logBody, grey.Render(view.Clock(r.TS, o.Loc))+"  "+fit(who, whoW)+" "+text)
	}
	out = append(out, box("session log", "[ stream: "+live+" ]", logBody, w, solid(grey))...)
	out = append(out, grey.Render(fmt.Sprintf("tests %d/%d · tool failures %d · t table · tab next · q quit",
		d.TestsPass, d.TestsPass+d.TestsFail, d.ToolFails)))
	return out
}

func joinCols(left []string, lw int, right []string) []string {
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	var out []string
	for i := 0; i < n; i++ {
		l, r := strings.Repeat(" ", lw), ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, l+" "+r)
	}
	// pad the shorter panel with empty rows so both borders close at the same height
	return out
}
