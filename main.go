// Command ccwatch is a read-only live view of Claude Code orchestration.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/toniim/ccwatch/internal/hook"
	"github.com/toniim/ccwatch/internal/line"
	"github.com/toniim/ccwatch/internal/otlp"
	"github.com/toniim/ccwatch/internal/state"
	"github.com/toniim/ccwatch/internal/tui"
	"github.com/toniim/ccwatch/internal/view"
	"github.com/toniim/ccwatch/internal/web"
)

const usage = `usage: ccwatch <command>

  hook                       append one hook event from stdin (always exits 0)
  serve [--addr A] [--prune-max-age D]
                             OTLP/HTTP JSON receiver + web view (default 127.0.0.1:4319);
                             prunes old state at start and every 6h (D=0 disables)
  prune [--max-age 720h] [--dry-run]   delete state of sessions idle longer than max-age
  line                       print the status line (reads statusLine JSON on stdin)
  tui [--session ID|--latest] [--once] [--width N] [--all-workers] [--table]
  demo                       print settings JSON for 'claude --settings' and OTel env lines
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "hook":
		// Never fail Claude Code: ignore every error, always exit 0.
		_ = hook.Run(os.Stdin, state.Dir(), time.Now())
	case "serve":
		os.Exit(serve(args))
	case "prune":
		os.Exit(runPrune(args))
	case "line":
		fmt.Println(line.Run(os.Stdin, state.Dir()))
	case "tui":
		os.Exit(runTUI(args))
	case "demo":
		os.Exit(demo())
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:4319", "listen address")
	maxAge := fs.Duration("prune-max-age", defaultMaxAge(), "prune sessions idle longer than this (0 disables; env CCWATCH_MAX_AGE)")
	_ = fs.Parse(args)
	dir := state.Dir()
	if *maxAge > 0 {
		go state.PruneLoop(make(chan struct{}), dir, *maxAge, pruneEvery, time.Now, func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) })
	}
	fmt.Fprintf(os.Stderr, "ccwatch serve: OTLP on http://%s/v1/logs, web view on http://%s/, state %s\n", *addr, *addr, dir)
	web := web.NewServer(view.NewSource(dir), *addr)
	srv := &http.Server{Addr: *addr, Handler: web.Wrap(otlp.Handler(dir)), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "ccwatch serve:", err)
		return 1
	}
	return 0
}

func runTUI(args []string) int {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	session := fs.String("session", "", "session id to show")
	_ = fs.Bool("latest", false, "show the most recently active session (default)")
	once := fs.Bool("once", false, "print one frame and exit")
	width := fs.Int("width", 120, "frame width for --once")
	allWorkers := fs.Bool("all-workers", false, "show every Orca dispatch, not only active ones")
	table := fs.Bool("table", false, "start in the table view (t toggles)")
	_ = fs.Parse(args)
	src := view.NewSource(state.Dir())
	if *once {
		fmt.Print(tui.Once(src, *session, *width, *table, *allWorkers))
		return 0
	}
	m := tui.New(src, *session, *allWorkers)
	if *table {
		m.ToggleTable()
	}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ccwatch tui:", err)
		return 1
	}
	return 0
}

func demo() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccwatch demo:", err)
		return 1
	}
	exe, _ = filepath.Abs(exe)
	fmt.Print(demoSettings(exe))
	fmt.Fprint(os.Stderr, demoEnv())
	return 0
}

const pruneEvery = 6 * time.Hour

// defaultMaxAge is $CCWATCH_MAX_AGE when it parses as a duration, else 720h.
func defaultMaxAge() time.Duration {
	if v := os.Getenv("CCWATCH_MAX_AGE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "ccwatch: ignoring invalid CCWATCH_MAX_AGE %q\n", v)
	}
	return state.DefaultMaxAge
}

func runPrune(args []string) int {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	maxAge := fs.Duration("max-age", defaultMaxAge(), "remove sessions idle longer than this (env CCWATCH_MAX_AGE)")
	dry := fs.Bool("dry-run", false, "print what would be removed, delete nothing")
	_ = fs.Parse(args)
	if *maxAge <= 0 {
		fmt.Fprintln(os.Stderr, "ccwatch prune: --max-age must be positive")
		return 2
	}
	res := state.Prune(state.Dir(), *maxAge, time.Now(), *dry)
	verb := "removed"
	if *dry {
		verb = "would remove"
	}
	for _, r := range res.Removed {
		fmt.Printf("%s %s (%d bytes)\n", verb, r.Path, r.Size)
	}
	for _, l := range res.Left {
		fmt.Fprintln(os.Stderr, "ccwatch prune: left", l)
	}
	fmt.Printf("%s %d files, %d bytes\n", verb, len(res.Removed), res.Bytes)
	return 0
}
