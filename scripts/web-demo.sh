#!/usr/bin/env bash
# Starts `ccwatch serve` (OTLP + web view) on a fresh state dir seeded with two
# older sessions, then runs one slow `claude -p --model haiku --settings` session
# so the page can be watched mid-run. The server stays up until $WORK/stop exists
# or DEMO_HOLD seconds pass; its PID is captured and killed on exit.
# env: CCWATCH_PORT (4319), DEMO_SLEEP (12, seconds the background agents wait), DEMO_HOLD (300)
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${CCWATCH_PORT:-4319}"
SLEEP="${DEMO_SLEEP:-12}"
HOLD="${DEMO_HOLD:-300}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/ccwatch-web.XXXXXX")"
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
go build -o "$BIN" . || exit 1

if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then
  echo "port $PORT is already in use; stop its owner or set CCWATCH_PORT" >&2
  exit 1
fi

# older sessions so the picker has something to switch to
while IFS= read -r l; do printf '%s' "$l" | "$BIN" hook; done <"$ROOT/testdata/hooks-demo-session.jsonl"
mkdir -p "$CCWATCH_STATE/events" "$CCWATCH_STATE/otel"
cp "$ROOT"/testdata/realistic/events/*.jsonl "$CCWATCH_STATE/events/"
cp "$ROOT"/testdata/realistic/otel/*.jsonl "$CCWATCH_STATE/otel/"
touch -d '2 hours ago' "$CCWATCH_STATE"/events/10000002-*.jsonl "$CCWATCH_STATE"/events/10000001-*.jsonl "$CCWATCH_STATE"/otel/10000001-*.jsonl
touch -d '1 hour ago' "$CCWATCH_STATE"/events/10000001-*.jsonl

"$BIN" serve --addr "127.0.0.1:$PORT" 2>"$WORK/serve.log" &
SERVE_PID=$!
sleep 0.5
kill -0 "$SERVE_PID" 2>/dev/null || { cat "$WORK/serve.log" >&2; exit 1; }
echo "READY http://127.0.0.1:$PORT/ serve_pid=$SERVE_PID work=$WORK"

"$BIN" demo >"$WORK/settings.json" 2>"$WORK/otel-env.sh"
sed -i "s#127.0.0.1:4319#127.0.0.1:$PORT#" "$WORK/otel-env.sh"
# shellcheck disable=SC1091
. "$WORK/otel-env.sh"

PROMPT="Do exactly these steps, using tools, and keep every reply to one short line.
1. With the Agent tool (subagent_type general-purpose) launch three subagents: one in the foreground whose task is to run the bash command \"sleep 4 && echo alpha\" and reply with its output only; and two with run_in_background true whose tasks are to run \"sleep $SLEEP && echo beta\" and \"sleep $SLEEP && echo gamma\" the same way. Wait until both background agents have finished.
2. With the Bash tool run: go test ./...
3. With the Bash tool run: false
Then reply with the single word done."

echo "CLAUDE_START $(date +%T)"
# the demo session is not an Orca worker: drop the worker terminal handle it would inherit
env -u ORCA_TERMINAL_HANDLE timeout 120 claude -p --model haiku --settings "$WORK/settings.json" \
  --permission-mode bypassPermissions "$PROMPT" </dev/null
echo "CLAUDE_DONE exit=$? $(date +%T)"

for _ in $(seq 1 "$HOLD"); do
  [ -e "$WORK/stop" ] && break
  sleep 1
done
echo "STOPPING"
