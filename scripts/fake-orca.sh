#!/usr/bin/env bash
# Canned `orca orchestration ...` answers from testdata/orca, for showing the Orca
# panels on fixture state:  PATH="<dir with this script named orca>:$PATH" ccwatch serve
D="$(cd "$(dirname "$(readlink -f "$0")")/.." && pwd)/testdata/orca"
case "$2" in
  worker-list) cat "$D/worker-list.json" ;;
  worker-show) cat "$D/worker-show-ctx_aaa111.json" ;;
  task-list)   cat "$D/task-list.json" ;;
  run-list)    cat "$D/run-list.json" ;;
  *) echo '{"ok":false}' ;;
esac
