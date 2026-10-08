// Command sysc-lock requests the supervised Wayland session owner.
// The --session owner acquires ext-session-lock-v1 and authenticates with PAM.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Nomadcxx/sysc-Go/animations"

	"github.com/Nomadcxx/sysc-lock/internal/ambient"
	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/auth"
	"github.com/Nomadcxx/sysc-lock/internal/config"
	"github.com/Nomadcxx/sysc-lock/internal/inhibit"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/options"
	"github.com/Nomadcxx/sysc-lock/internal/power"
	"github.com/Nomadcxx/sysc-lock/internal/render"
	"github.com/Nomadcxx/sysc-lock/internal/theme"
	"github.com/Nomadcxx/sysc-terminal/renderer"
)

// The request command exits 0 after confirmed authenticated unlock; failures exit 1.
// The persistent owner reports acquisition/unlock outcomes through its snapshots.
const version = "0.1.0-dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("sysc-lock", version)
		return
	}
	var err error
	switch {
	case len(os.Args) == 1:
		err = requestLock()
	case len(os.Args) == 2 && os.Args[1] == "--session":
		err = runSession()
	case len(os.Args) == 2 && os.Args[1] == "--ambient":
		err = runAmbient()
	case len(os.Args) == 2 && os.Args[1] == "--preview":
		err = runPreview(os.Stdin, os.Stdout)
	case len(os.Args) == 2 && os.Args[1] == "--describe":
		err = writeDescription(os.Stdout)
	default:
		err = fmt.Errorf("usage: sysc-lock [--session|--version|--ambient|--preview|--describe]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		os.Exit(1)
	}
}

// runLocker owns a single acquisition. The persistent session owns supervision.
func runLocker(report func(lockd.Snapshot), beforeUnlock func() error) (lockd.Phase, error) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return lockd.Idle, fmt.Errorf("WAYLAND_DISPLAY not set")
	}

	// SIGTERM before the compositor confirms "locked": abandon the lock
	// request (the session was never sealed). After
	// confirmation the lock MUST stay; ignore termination signals — the only
	// way out is authentication.
	sigs := make(chan os.Signal, 2)
	defer signal.Stop(sigs)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	user, err := currentUser()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		return lockd.Idle, err
	}
	host, _ := os.Hostname()
	pal, err := theme.Load(palettePath())
	if err != nil {
		pal = theme.Default()
	}
	view := lockd.NewView(pal, user, host)
	view.TextScale = lockd.SystemTextScale()

	model := &input.Model{}
	defer model.Clear()
	view.Entry = model

	authenticator := newAuthenticator(user)
	gate := &enterGate{}

	st := lockd.New()
	sessionID := os.Getenv("XDG_SESSION_ID")
	row := &ambientRow{path: ambient.Path()}
	menu := power.New(nil, power.Availability{}, sessionID)
	opts := options.New(effectChoices(), animations.GetThemeNames(), config.Default().Effect, config.Default().Palette)
	cfg := config.Default()
	if err := art.SeedHeaders(config.HeadersPath()); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock: could not create headers.conf; shipped artwork remains available")
	}
	headers, headersErr := art.LoadHeaders(config.HeadersPath())
	if headersErr != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock: invalid headers.conf; using shipped artwork")
	}
	headerIDs := make([]string, len(headers))
	for i, h := range headers {
		headerIDs[i] = h.ID
	}
	applyArtwork := func() {
		for _, h := range headers {
			if h.ID == opts.Header() {
				view.Header = h.Text
				break
			}
		}
		view.TextEffect, view.TextPalette = opts.TextEffect(), opts.Theme()
	}
	executor := power.Executor{Session: sessionID}
	var client *lockd.Client
	client, err = lockd.Connect(st, func(k lockd.Key) {
		view.Caps = k.CapsLock
		view.Layout = k.Layout
		view.Num = k.NumLock
		if view.Terminal() || st.Phase() != lockd.Locked {
			client.Repaint()
			return
		}
		now := time.Now()
		if wakeScreensaver(view, k, now) {
			client.Repaint()
			return
		}
		if view.Powering != "" {
			client.Repaint()
			return
		}
		if gate.prompt != nil {
			if err := gate.promptKey(model, k); err != nil {
				view.SetError(err.Error(), time.Now())
			}
			client.Repaint()
			return
		}
		if gate.busy {
			client.Repaint()
			return
		}
		if !k.F1 && !k.PageUp && !k.PageDown && !gate.visible(model, &view.Reveal, now, menu.Open() || opts.Open() || view.Powering != "") {
			gate.press(model, &view.Reveal, k, now)
			client.Repaint()
			return
		}
		if opts.Open() || k.F1 || (!menu.Open() && (k.PageUp || k.PageDown)) {
			menu.Close()
			if changed := gate.pressOptions(&view.Reveal, optionsKey(k), opts, now); changed {
				backgroundChanged := cfg.Effect != opts.Effect() || cfg.Palette != opts.Theme()
				cfg.Effect, cfg.Palette = opts.Effect(), opts.Theme()
				cfg.Header, cfg.TextEffect = opts.Header(), opts.TextEffect()
				applyArtwork()
				if err := config.Save(config.Path(), cfg); err != nil {
					fmt.Fprintln(os.Stderr, "sysc-lock: options: save failed:", err)
				}
				view.Pal = view.Pal.WithScheme(cfg.Palette)
				eff := cfg.Effect
				if eff == config.EffectNone {
					eff = ""
				}
				if backgroundChanged {
					client.Post(func() {
						client.ApplyPresentation(eff, cfg.Palette, cfg.ReducedMotion, cfg.BackendChoice(), cfg.GpuPowerSave())
					})
				}
			}
			client.Repaint()
			return
		}
		if menu.Open() || k.F4 {
			opts.Close()
			gate.pressMenu(model, &view.Reveal, powerKeys(k), menu, now)
			client.Repaint()
			return
		}
		submit, editErr := gate.press(model, &view.Reveal, k, now)
		if editErr != nil {
			view.SetError(editErr.Error(), time.Now())
		}
		if submit {
			menu.Close()
			view.Busy = true
			go authenticate(authenticator, model.Password(), client, view, model, gate, gate.generation)
		}
		client.SetMotionFrozen(gate.busy, time.Now())
		client.Repaint()
	}, func(fb *render.Framebuffer, scale float64, background []byte) error {
		view.Scale = scale
		now := time.Now()
		view.Power = powerFrame(menu, now)
		view.Options = optionsFrame(opts)
		if menu.Available() {
			view.Hint = power.ScreenHelp
		} else {
			view.Hint = power.ScreenHelpPlain
		}
		if view.Prompt != "" {
			view.Hint = view.Prompt
		}
		cell := max(8, int(8*view.TextScale))
		view.Ambient = row.Get(now, max(8, lockd.Layout(fb.Width, fb.Height, scale, view.StyleName, "12:59:59 PM").Entry.Dx()/cell))
		if background == nil {
			view.Render(fb, now)
		} else {
			copy(fb.Pix, background)
			lockd.DimBackground(fb.Pix)
			view.RenderForeground(fb, now)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		return lockd.Idle, err
	}
	gate.paste = client.Paste
	stopSignals := make(chan struct{})
	defer close(stopSignals)
	go func() {
		for {
			select {
			case <-stopSignals:
				return
			case <-sigs:
				client.Post(func() { client.AbortBeforeLocked() })
			}
		}
	}()
	var amb ambientChild
	defer amb.stop()
	ambientStarted := false
	client.OnEvent = func(s lockd.Snapshot) {
		if armAmbient(s.Phase, ambientStarted) {
			ambientStarted = true
			amb = startAmbient()
		}
		if report != nil {
			report(s)
		}
	}
	client.BeforeUnlock = beforeUnlock
	defer client.Close()
	client.OnDeadline = func(now time.Time) time.Time {
		if a := menu.Tick(now); a != "" {
			view.Powering = a.Status()
			go func() {
				outcome := executor.Run(a)
				client.Post(func() {
					switch outcome {
					case power.OK:
					case power.Refused:
						menu.Recover()
						view.Powering = ""
						view.SetError("Not permitted", time.Now())
					default:
						menu.Recover()
						view.Powering = ""
						view.SetError("Failed", time.Now())
					}
					client.Repaint()
				})
			}()
		}
		return view.NextDeadline(now)
	}
	if loaded, configErr := config.Load(config.Path()); configErr != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock: invalid presentation config; using fallback")
		cfg = config.Default()
		cfg.ReducedMotion = true
	} else {
		cfg = loaded
	}
	opts.Set(cfg.Effect, cfg.Palette)
	opts.SetArtwork(headerIDs, append([]string{"none"}, renderer.TextEffects()...), cfg.Header, cfg.TextEffect)
	applyArtwork()
	view.StyleName, view.Clock24, view.Reduced = cfg.ClockStyle, cfg.Clock24h, cfg.ReducedMotion
	view.Pal = view.Pal.WithScheme(cfg.Palette)
	effect := cfg.Effect
	if effect == config.EffectNone {
		effect = ""
	}
	client.SetEffectRate(cfg.EffectFPS)
	client.EnableBackground(effect, cfg.Palette, cfg.ReducedMotion, cfg.BackendChoice(), cfg.GpuPowerSave())
	client.EnableWallpaper(os.Getenv("SYSC_LOCK_WALLPAPER"))
	client.EnableBlur(cfg.BlurBackdrop(), cfg.BlurRadiusPx())
	client.CaptureBlur()

	if err := client.Lock(); err != nil {
		fmt.Fprintln(os.Stderr, "sysc-lock:", err)
		return lockd.Idle, err
	}
	stopLogind := make(chan struct{})
	defer close(stopLogind)
	go func() {
		lg, err := inhibit.NewLogind()
		if err != nil {
			return
		}
		defer lg.Release()
		caller := power.NewCaller(lg)
		avail := power.Check(caller)
		client.Post(func() {
			executor.Caller = caller
			menu = power.New(cfg.PowerActions, avail, sessionID)
			client.Repaint()
		})
		<-stopLogind
	}()
	if err := client.Run(); err != nil {

		fmt.Fprintln(os.Stderr, "sysc-lock: connection lost:", err)
		return client.State().Phase(), err
	}
	return client.State().Phase(), client.WaitBackground()
}

func authenticate(a authenticator, pass string, client *lockd.Client, view *lockd.View, model *input.Model, gate *enterGate, generation uint64) {
	var abortErr error
	ask := func(p auth.Prompt) (string, error) {
		w := &promptWait{reply: make(chan promptAnswer, 1)}
		timer := time.AfterFunc(promptDeadline, func() {
			w.send(promptAnswer{err: errPromptTimeout})
		})
		client.Post(func() {
			if !gate.accept(generation, client.State().Phase()) {
				w.send(promptAnswer{err: errPromptCancelled})
				return
			}
			gate.prompt = w
			view.Prompt = displayPrompt(p.Message, "Enter code")
			view.PromptEcho = p.Kind == auth.Visible
			client.Repaint()
		})
		ans := <-w.reply
		timer.Stop()
		if ans.err != nil {
			return "", ans.err
		}
		return ans.text, nil
	}
	first := pass
	pass = "" // immutable runtime/PAM copies cannot be reliably erased
	response := func(p auth.Prompt) (string, error) {
		switch p.Kind {
		case auth.Info, auth.Problem:
			client.Post(func() {
				if p.Kind == auth.Info {
					view.Prompt = displayPrompt(p.Message, "Waiting for device")
				} else {
					view.SetError(displayPrompt(p.Message, "Authentication error"), time.Now())
				}
				client.Repaint()
			})
			return "", nil
		case auth.Visible:
			text, err := ask(p)
			if err != nil {
				abortErr = err
				return "", err
			}
			return text, nil
		default:
			if first != "" {
				text := first
				first = ""
				return text, nil
			}
			text, err := ask(p)
			if err != nil {
				abortErr = err
				return "", err
			}
			return text, nil
		}
	}
	res, err := a.Verify(a.User(), response)
	client.Post(func() {
		if !gate.accept(generation, client.State().Phase()) {
			return
		}
		gate.release()
		gate.prompt = nil
		view.Busy = false
		view.Prompt = ""
		view.PromptEcho = false
		model.Clear()
		if !showAuthFailure(view, res, err, abortErr, time.Now()) {
			view.SetError("", time.Now())
			if err := client.UnlockAndQuit(); err != nil {
				if errors.Is(err, lockd.ErrUnlockDeferred) {
					view.SetError("Resume before unlocking", time.Now())
				} else {
					view.SetErrorTerminal("Unlock confirmation failed", time.Now())
				}
			}
		}
		client.SetMotionFrozen(false, time.Now())
		client.Repaint()
	})
}

func powerKeys(k lockd.Key) power.Key {
	return power.Key{Up: k.Up, Down: k.Down, Enter: k.Enter, Escape: k.Escape, F4: k.F4, Released: k.Released}
}

func powerFrame(m *power.Menu, now time.Time) *lockd.PowerView {
	if !m.Available() {
		return nil
	}
	rows := m.Items()
	p := &lockd.PowerView{Open: m.Open(), Title: power.Title, Help: power.Help, Progress: m.Progress(now)}
	for i, a := range rows {
		p.Rows = append(p.Rows, lockd.PowerRow{Title: a.Label(), Selected: i == m.Selected()})
	}
	return p
}

func optionsKey(k lockd.Key) options.Key {
	return options.Key{Up: k.Up, Down: k.Down, Left: k.Left, Right: k.Right, Enter: k.Enter, Escape: k.Escape, F1: k.F1, PageUp: k.PageUp, PageDown: k.PageDown, Released: k.Released}
}

func optionsFrame(o *options.Options) *lockd.MenuView {
	if !o.Open() {
		return nil
	}
	p := &lockd.MenuView{Open: true, Title: "────///////OPTIONS///////────", Help: "↑↓ Row • ←→ Change • PgUp/PgDn Header", Progress: -1}
	for i, row := range []lockd.PowerRow{{Title: "Background", Value: o.Effect()}, {Title: "Theme", Value: o.Theme()}, {Title: "Header", Value: o.Header()}, {Title: "Text effect", Value: o.TextEffect()}} {
		row.Selected = i == o.Selected()
		p.Rows = append(p.Rows, row)
	}
	return p
}

// effectChoices is the menu's background list: none plus every scene effect;
// text effects use the separately selected header.
func effectChoices() []string {
	text := map[string]bool{}
	for _, n := range animations.GetTextBasedEffects() {
		text[n] = true
	}
	out := []string{config.EffectNone}
	for _, n := range animations.GetEffectNames() {
		if !text[n] {
			out = append(out, n)
		}
	}
	return out
}
