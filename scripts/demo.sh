#!/usr/bin/env bash
# End-to-end ccwatch demo. Runs one real `claude -p --model haiku` session through
# `claude --settings <file>` only (no global config is touched), then prints the
# status line and one TUI frame for that session.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${CCWATCH_PORT:-4319}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/ccwatch-try.XXXXXX")"
BIN="$WORK/ccwatch"
export CCWATCH_STATE="$WORK/state"
SERVE_PID=""

cleanup() {
  if [ -n "$SERVE_PID" ]; then
    kill "$SERVE_PID" 2>/dev/null
    wait "$SERVE_PID" 2>/dev/null
  fi
}
trap cleanup EXIT

cd "$ROOT" || exit 1
echo "== build"
go build -o "$BIN" . || exit 1

if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
  echo "port $PORT is already in use; stop its owner or set CCWATCH_PORT" >&2
  exit 1
fi

echo "== serve on 127.0.0.1:$PORT (state $CCWATCH_STATE)"
"$BIN" serve --addr "127.0.0.1:$PORT" 2>"$WORK/serve.log" &
SERVE_PID=$!
sleep 0.5
kill -0 "$SERVE_PID" 2>/dev/null || { cat "$WORK/serve.log" >&2; exit 1; }

"$BIN" demo >"$WORK/settings.json" 2>"$WORK/otel-env.sh"
sed -i "s#127.0.0.1:4319#127.0.0.1:$PORT#" "$WORK/otel-env.sh"
# shellcheck disable=SC1091
. "$WORK/otel-env.sh"

PROMPT='Do exactly these steps, using tools, and keep every reply to one short line.
1. With the Agent tool (subagent_type general-purpose) launch three subagents: one in the foreground whose task is to run the bash command "echo alpha" and reply with its output only; and two with run_in_background true whose tasks are to run "echo beta" and "echo gamma" the same way. Wait until both background agents have finished.
2. With the Bash tool run: go test ./...
3. With the Bash tool run: false
Then reply with the single word done.'

echo "== claude -p --model haiku (cap 90 s)"
START=$(date +%s)
# the demo session is not an Orca worker: drop the worker terminal handle it would inherit
env -u ORCA_TERMINAL_HANDLE timeout 90 claude -p --model haiku --settings "$WORK/settings.json" \
  --permission-mode bypassPermissions "$PROMPT" </dev/null
echo "claude exit: $? ($(( $(date +%s) - START )) s)"
sleep 2 # let the last OTLP batch land

SID="$(basename "$(ls -t "$CCWATCH_STATE"/events/*.jsonl 2>/dev/null | head -1)" .jsonl)"
if [ -z "$SID" ] || [ "$SID" = ".jsonl" ]; then
  echo "no events captured" >&2
  exit 1
fi
echo "== session $SID"
echo "== event lines: $(cat "$CCWATCH_STATE/events/$SID.jsonl" | wc -l), otel lines: $(cat "$CCWATCH_STATE/otel/$SID.jsonl" 2>/dev/null | wc -l)"

echo "== ccwatch line"
printf '{"session_id":"%s","model":{"display_name":"Haiku"}}' "$SID" | "$BIN" line

echo "== ccwatch tui --once (dashboard)"
TZ=UTC "$BIN" tui --session "$SID" --once --width 120
echo "== ccwatch tui --once --table"
"$BIN" tui --session "$SID" --once --table

echo
echo "== replay live: CCWATCH_STATE=$CCWATCH_STATE $BIN tui --latest"
trap - EXIT
cleanup
exit 0
