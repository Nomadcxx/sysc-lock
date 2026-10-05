#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if "$root/scripts/qualify" sample 1 1 "$tmp/rejected" 2>/dev/null; then
 echo 'accepted unsafe PID' >&2; exit 1
fi
"$root/scripts/qualify" sample "$$" 1 "$tmp/sample"
python3 - "$tmp/sample/process-$$.csv" <<'PY'
import csv, sys
with open(sys.argv[1]) as file:
    rows = list(csv.reader(file))
assert rows[0] == ['elapsed_s', 'cpu_percent_one_core', 'rss_bytes', 'fd_count']
assert len(rows) == 2
assert float(rows[1][0]) >= 1 and int(rows[1][2]) > 0
PY
