#!/usr/bin/env bash
# Exercises the interactive TUI in a detached tmux session: captures the pane,
# presses tab, appends a hook event while it runs (500 ms refresh), presses q.
# usage: scripts/tui-tmux-test.sh <state-dir-with-one-session> <capture-file>
set -u
SRC="${1:?state dir}"
OUT="${2:?capture file}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SESSION=ccwatch-tui-test
WORK="$(mktemp -d "${TMPDIR:-/tmp}/ccwatch-tui-test.XXXXXX")"
BIN="$WORK/ccwatch"
export CCWATCH_STATE="$WORK/state"

cleanup() { /usr/bin/tmux kill-session -t "$SESSION" 2>/dev/null; }
trap cleanup EXIT

cd "$ROOT" || exit 1
go build -o "$BIN" . || exit 1
cp -r "$SRC" "$CCWATCH_STATE"

# second, older session from the captured fixture, so tab has somewhere to go
while IFS= read -r l; do printf '%s' "$l" | "$BIN" hook; done <"$ROOT/testdata/hooks-demo-session.jsonl"
FIXTURE_SID=10000002-0000-4000-8000-000000000002
touch -d '1 hour ago' "$CCWATCH_STATE/events/$FIXTURE_SID.jsonl"
DEMO_SID="$(basename "$(ls -t "$CCWATCH_STATE"/events/*.jsonl | head -1)" .jsonl)"

cap() { { echo; echo "##### $1"; /usr/bin/tmux capture-pane -p -t "$SESSION"; } >>"$OUT"; }
: >"$OUT"

/usr/bin/tmux new-session -d -s "$SESSION" -x 150 -y 60 "env CCWATCH_STATE=$CCWATCH_STATE $BIN tui --latest"
sleep 1.5
cap "1. dashboard (default view, latest session $DEMO_SID)"

/usr/bin/tmux send-keys -t "$SESSION" t
sleep 1
cap "2. after t (table view)"

/usr/bin/tmux send-keys -t "$SESSION" t
sleep 1
cap "3. after t again (dashboard)"

/usr/bin/tmux send-keys -t "$SESSION" Tab
sleep 1
cap "4. after tab (should show fixture session ${FIXTURE_SID:0:8})"

/usr/bin/tmux send-keys -t "$SESSION" Tab
sleep 1

{ printf '{"session_id":"%s","hook_event_name":"SessionStart","source":"resume"}' "$DEMO_SID" | "$BIN" hook
  printf '{"session_id":"%s","hook_event_name":"UserPromptSubmit","prompt":"live append: spawn one more agent"}' "$DEMO_SID" | "$BIN" hook
  printf '{"session_id":"%s","prompt_id":"live-p","hook_event_name":"SubagentStart","agent_id":"afeed00000000live","agent_type":"general-purpose"}' "$DEMO_SID" | "$BIN" hook; }
sleep 1.2
cap "5. 1.2 s after appending SessionStart(resume) + UserPromptSubmit + SubagentStart with 'ccwatch hook' (expect a you row, a started row, one more slot, state orchestrating)"

{ echo; echo "##### 5b. ccwatch line for the demo session while the TUI runs (workers cache written by the TUI)";
  printf '{"session_id":"%s","model":{"display_name":"Haiku"}}' "$DEMO_SID" | "$BIN" line; } >>"$OUT"

/usr/bin/tmux send-keys -t "$SESSION" q
sleep 1
if /usr/bin/tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "##### 6. after q: tmux session STILL ALIVE" >>"$OUT"
  exit 1
fi
echo "##### 6. after q: program exited, tmux session closed" >>"$OUT"
