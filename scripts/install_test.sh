#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf '#!/bin/sh\nexit 0\n' > "$tmp/candidate"
chmod 0755 "$tmp/candidate"
"$root/scripts/install" "$tmp/candidate" "$tmp/prefix"
test -x "$tmp/prefix/bin/sysc-lock"
grep -F "ExecStart=$tmp/prefix/bin/sysc-lock --session" "$tmp/prefix/share/systemd/user/sysc-lock-session.service"
! grep -E 'systemctl|pam.d' "$root/scripts/install"
