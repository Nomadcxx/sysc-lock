#!/bin/sh
# sysc-lock bootstrap: curl -fsSL https://raw.githubusercontent.com/Nomadcxx/sysc-lock/master/install.sh | sh -s -- --yes
set -eu
command -v git >/dev/null 2>&1 || { echo 'git is required' >&2; exit 2; }
command -v go  >/dev/null 2>&1 || { echo 'go is required: https://go.dev/doc/install' >&2; exit 2; }
repo=https://github.com/Nomadcxx/sysc-lock.git
# Install the newest release tag; SYSC_LOCK_REF overrides it (e.g. master).
ref=${SYSC_LOCK_REF:-$(git ls-remote --tags --refs --sort=-v:refname "$repo" 'v*' | sed -n '1s|.*refs/tags/||p')}
[ -n "$ref" ] || { echo 'no release tag found; set SYSC_LOCK_REF' >&2; exit 2; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
git clone --depth 1 --branch "$ref" "$repo" "$tmp/sysc-lock"
cd "$tmp/sysc-lock"
CGO_ENABLED=0 go build -o "$tmp/sysc-lock-installer" ./cmd/installer
set +e
"$tmp/sysc-lock-installer" "$@"
status=$?
set -e
exit "$status"