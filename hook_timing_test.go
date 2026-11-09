package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestHookProcessTiming builds the binary and times real `ccwatch hook` process
// invocations (exec + run + exit), the cost Claude Code actually pays per event.
func TestHookProcessTiming(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "ccwatch")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	payloadBytes, err := os.ReadFile(filepath.Join("testdata", "hooks-demo-session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	payloads := strings.Split(strings.TrimSpace(string(payloadBytes)), "\n")
	env := append(os.Environ(), "CCWATCH_STATE="+filepath.Join(tmp, "state"))

	const n = 100
	var d []time.Duration
	for i := 0; i < n; i++ {
		cmd := exec.Command(bin, "hook")
		cmd.Env = env
		cmd.Stdin = strings.NewReader(payloads[i%len(payloads)])
		start := time.Now()
		if err := cmd.Run(); err != nil {
			t.Fatalf("hook exit: %v", err)
		}
		d = append(d, time.Since(start))
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	t.Logf("ccwatch hook process time over %d runs: min %v median %v p95 %v max %v", n, d[0], d[n/2], d[n*95/100], d[n-1])
	if d[n/2] > 10*time.Millisecond {
		t.Errorf("median %v exceeds 10ms", d[n/2])
	}
	if _, err := exec.Command(bin, "hook").Output(); err != nil { // empty stdin must still exit 0
		t.Errorf("empty stdin exit: %v", err)
	}
}
