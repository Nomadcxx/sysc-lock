#!/bin/sh
# sysc-lock bootstrap: curl -fsSL https://raw.githubusercontent.com/Nomadcxx/sysc-lock/master/install.sh | sh -s -- --yes
set -eu
command -v git >/dev/null 2>&1 || { echo 'git is required' >&2; exit 2; }
command -v go  >/dev/null 2>&1 || { echo 'go is required: https://go.dev/doc/install' >&2; exit 2; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
git clone --depth 1 https://github.com/Nomadcxx/sysc-lock.git "$tmp/sysc-lock"
cd "$tmp/sysc-lock"
CGO_ENABLED=0 go build -o "$tmp/sysc-lock-installer" ./cmd/installer
set +e
"$tmp/sysc-lock-installer" "$@"
status=$?
set -e
exit "$status"