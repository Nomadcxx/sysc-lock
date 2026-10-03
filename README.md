# sysc-lock

A Wayland-native lock manager and screen locker with built-in idle detection.

## Features

- 🔒 **Secure PAM Authentication** - Following swaylock's proven implementation
- 🖥️ **Multi-Monitor Support** - Independent layer surfaces per monitor
- ⏱️ **Idle Detection** - Built-in idle monitoring (replaces hypridle)
- 🎨 **Theme Support** - Reuses sysc-greet themes (Dracula, Nord, Gruvbox, etc.)
- 🎬 **Screensaver Mode** - Animated screensavers (Matrix, Rain, Fire effects)
- ⚙️ **Menu System** - Settings (F1) and Power (F4) menus
- 🧪 **Test Mode** - Development mode without authentication
- 🛡️ **Input Inhibitor** - Prevents compositor hotkeys (Super+Q, etc.)
- 🔌 **Daemon Mode** - Runs as background service

## Architecture

sysc-lock is a **lock manager daemon** that spawns UI processes:

```
┌─────────────────────────────────────────────────────────────┐
│  sysc-lock (Lock Manager Daemon)                            │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ • Idle detection (monitors user activity)             │  │
│  │ • Monitor enumeration (wl_output)                     │  │
│  │ • Input inhibitor (zwlr_input_inhibit_manager_v1)     │  │
│  │ • PAM authentication                                  │  │
│  │ • IPC with UI processes (stdin/stdout)               │  │
│  └───────────────────────────────────────────────────────┘  │
│                           │                                  │
│    On Lock (idle or manual trigger):                        │
│                           ├──────────────┐                   │
│                           ▼              ▼                   │
│  ┌─────────────────────────┐   ┌─────────────────────────┐  │
│  │  sysc-lock-tui          │   │  sysc-lock-monitor      │  │
│  │  (Primary Monitor)      │   │  (Secondary Monitors)   │  │
│  │                         │   │                         │  │
│  │ • Creates layer surface │   │ • Creates layer surface │  │
│  │ • Renders TUI directly  │   │ • Renders solid color   │  │
│  │ • Password input        │   │ • Clock display         │  │
│  │ • Communicates via IPC  │   │ • Independent process   │  │
│  └─────────────────────────┘   └─────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### Three-Binary Architecture

1. **sysc-lock** - Manager daemon
   - Runs continuously in background
   - Monitors for idle timeout
   - Detects monitors and spawns UI processes
   - Handles PAM authentication
   - Manages input inhibitor
   - Coordinates unlock

2. **sysc-lock-tui** - Primary monitor UI
   - Standalone Wayland client
   - Creates its own layer surface on specified output
   - Renders password prompt, clock, menus
   - Sends auth requests to manager via IPC

3. **sysc-lock-monitor** - Secondary monitor UI
   - Standalone Wayland client
   - Creates its own layer surface on specified output
   - Renders solid color background with clock
   - Mostly passive (no user interaction)

## Current Status

### ✅ Phase 1 Complete: Wayland Protocol Bindings
- CGO-based wlr-layer-shell-unstable-v1 implementation
- Multi-monitor output enumeration working
- Input inhibitor protocol support
- Protocol C files auto-generated from XML

### ✅ Phase 2 Complete: Base UI Components
- sysc-lock-tui: Full Bubble Tea-based interface with password input
- sysc-lock-monitor: Simple secondary display
- Both binaries build successfully

### 🚧 Phase 3 In Progress: Daemon + IPC Architecture
- [ ] Refactor sysc-lock as daemon with idle detection
- [ ] Implement stdin/stdout IPC between manager and UI processes
- [ ] Each UI process creates its own layer surface
- [ ] Remove kitty dependency entirely

### 📋 Phase 4: PAM Authentication
- [ ] PAM conversation handler in daemon
- [ ] IPC protocol for auth requests/responses
- [ ] Rate limiting
- [ ] Unlock signaling

### 📋 Phase 5: Polish & Features
- [ ] Screensaver mode
- [ ] Theme system
- [ ] Configuration file
- [ ] Systemd integration
- Password input with masking and cursor animation
- Live updating clock display
- Error messages and attempt tracking
- Test mode support
- Process manager integration
- Makefile build system

### 🚧 Phase 3 In Progress: PAM Authentication
- [ ] PAM conversation handler
- [ ] Authentication flow integration
- [ ] Rate limiting
- [ ] Unlock signaling
- [ ] Error handling

### 📋 TODO

**Phase 3: PAM Authentication** (Next - 2 days)
- [ ] Implement PAM conversation handler
- [ ] Wire TUI to PAM backend
- [ ] Add rate limiting
- [ ] Unlock signaling to parent
- [ ] Error handling

**Phase 4: Screensaver Mode** (1-2 days)
- [ ] Idle timer
- [ ] Matrix rain animation
- [ ] ASCII rain animation
- [ ] Fire effect
- [ ] ASCII art cycling

**Phase 6: Polish & Testing**
- [ ] Configuration file support
- [ ] Logging
- [ ] Multi-monitor testing
- [ ] Security audit

## Protocol Files

Downloaded protocol XML files are in `internal/wayland/protocols/`:
- `wlr-layer-shell-unstable-v1.xml` - Layer shell protocol
- `wlr-input-inhibitor-unstable-v1.xml` - Input inhibitor protocol

These need to be converted to Go bindings.

## Dependencies

### System
- Wayland compositor with wlr-layer-shell support (Sway, Hyprland, Niri, River)
- kitty terminal emulator
- swww or gSlapper (optional, for secondary monitor wallpapers)
- PAM libraries
- Go 1.21+

### Go Modules
```
require (
    github.com/neurlang/wayland v0.2.1
    github.com/charmbracelet/bubbletea v1.3.10
    github.com/charmbracelet/lipgloss v1.1.0
    github.com/msteinert/pam/v2 v2.1.0
)
```

## Building

```bash
# Build everything (generates protocols + compiles)
make all

# Or build manually
make protocols  # Generate Wayland protocol C files
make build      # Compile Go binaries

# Clean build artifacts
make clean
```

**Build outputs:**
- `sysc-lock` - Main lock manager (2.8MB)
- `sysc-lock-tui` - TUI component (4.3MB)

## Usage

### Daemon Mode (Recommended)

```bash
# Start daemon at login (add to compositor config)
sysc-lock --daemon

# Or with systemd
systemctl --user enable --now sysc-lock
```

The daemon will:
- Monitor for idle activity (default: 5 minutes)
- Automatically lock when idle
- Listen for manual lock signals

### Manual Lock

```bash
# Lock immediately
sysc-lock --lock

# Or send signal to running daemon
killall -USR1 sysc-lock
```

### Configuration

```bash
# Custom config file
sysc-lock --daemon --config ~/.config/sysc-lock/config.toml
```

Example `config.toml`:
```toml
[idle]
timeout = 300  # seconds (5 minutes)
enabled = true

[theme]
name = "dracula"

[auth]
max_attempts = 3
lockout_duration = 30
```

### Test Mode (Development)

```bash
# Test mode - no authentication, Escape to exit
sysc-lock --test

# Test TUI standalone (no daemon)
SYSC_LOCK_TEST_MODE=1 ./sysc-lock-tui
```

## Development Notes

### Why Not ext-session-lock-v1?

ext-session-lock-v1 breaks often in practice. We're using wlr-layer-shell-unstable-v1 + wlr-input-inhibitor-unstable-v1 instead, which is:
- More stable and battle-tested
- Broader compositor support
- Used successfully by many applications
- Easier to debug

### Multi-Monitor Strategy

Following the gSlapper pattern:
- Independent layer shell surfaces per output
- Primary output: Interactive UI (kitty + TUI)
- Secondary outputs: Wallpaper/video or solid color
- No complex IPC between surfaces needed

### PAM Implementation

Following swaylock's proven pattern:
- Conversation handler for PAM messages
- Child process architecture (optional)
- Rate limiting and lockouts
- Secure password handling

## References

- [wlr-layer-shell Protocol](https://wayland.app/protocols/wlr-layer-shell-unstable-v1)
- [wlr-input-inhibitor Protocol](https://wayland.app/protocols/wlr-input-inhibitor-unstable-v1)
- [swaylock PAM Implementation](https://github.com/swaywm/swaylock/blob/master/pam.c)
- [gSlapper](https://github.com/Nomadcxx/gSlapper)
- [sysc-greet](https://github.com/Nomadcxx/sysc-greet)

## License

MIT
