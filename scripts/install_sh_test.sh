#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

# The bootstrap must be valid POSIX sh.
sh -n "$root/install.sh"

# ...and executable, since the README pipes it into sh -s.
test -x "$root/install.sh"

# It builds the installer without cgo, checks for git and go, forwards every
# argument and propagates the installer's exit status. No privilege escalation.
grep -F 'CGO_ENABLED=0 go build -o "$tmp/sysc-lock-installer" ./cmd/installer' "$root/install.sh" >/dev/null
grep -F 'git clone --depth 1 https://github.com/Nomadcxx/sysc-lock.git "$tmp/sysc-lock"' "$root/install.sh" >/dev/null
grep -F '"$tmp/sysc-lock-installer" "$@"' "$root/install.sh" >/dev/null
grep -F 'exit "$status"' "$root/install.sh" >/dev/null
! grep -E 'systemctl|pam\.d|sudo |pkexec|doas' "$root/install.sh"

echo "install.sh ok"