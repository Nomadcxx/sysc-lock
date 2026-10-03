# sysc-lock

A Wayland session locker built on `ext-session-lock-v1` (compositor-enforced input
seal), in pure Go + CGO only via `msteinert/pam/v2` for in-process PAM.
Designed for Niri / sysc-shell; works with any conformant compositor.

## Security model

- Locking is the compositor's `ext-session-lock-v1` — not a layer-shell overlay.
  Input is sealed per-output; the session is never exposed by the locker dying.
- Authentication is PAM (`login` service) in-process. There is no bypass flag,
  no test-mode env var, and no IPC unlock path. Test fakes live only in `_test.go`.
- Credentials are zeroed after use and never cross any process boundary or log.
- A logind sleep inhibitor is held until the compositor confirms `locked`, then
  released — suspend before a lock is fully up is blocked, and the shell can
  order "lock before suspend" around it.

## Build

    go build ./cmd/sysc-lock

Requires libpam headers (`pam_apl.h`) because of the cgo PAM binding.

## Usage

    sysc-lock                    # locks the current Wayland session
    sysc-lock --version

Environment (display/paths only, nothing security-relevant):

- `SYSC_LOCK_PALETTE`  palette JSON (default `~/.config/sysc-shell/palette.json`)
- `SYSC_LOCK_WALLPAPER` static background image (falls back to theme color)
- `SYSC_LOCK_LAYOUT`   keyboard layout label shown on the unlock screen

Exit codes: `0` unlocked, `1` no display / connection lost, `2` lock refused
(already locked), `3` terminated before lock was established, `4` sleep
inhibitor unavailable (session stays unlocked), `5` lock ended by compositor.

On the first locked frame across all outputs it prints `sysc-lock: locked`
to stdout — sysc-shell uses that line for its spawn handshake.

## sysc-shell integration (planned, see docs/plans in sysc-shell)

Set in shell config: `{ "session": { "locker": "sysc-lock" } }`.
The shell tracks the process, re-acquires on respawn-once after crash, pauses
wallpaper animation while locked, and exposes `session.lock-state` over IPC.
