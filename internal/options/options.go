// Package options models the lock screen's F1 options menu: background
// effect and theme selection, persisted by the caller.
package options

// Options is a pure menu model. The caller owns persistence and rendering.
type Options struct {
	effects []string
	themes  []string
	eff     int
	thr     int
	open    bool
	sel     int
}

// Key is the subset of lockd key state the menu consumes.
type Key struct {
	Up, Down, Left, Right bool
	Enter, Escape, F1     bool
	Released              bool
}

// New builds a menu over the given choices, selected at effect/theme.
func New(effects, themes []string, effect, theme string) *Options {
	o := &Options{effects: append([]string{}, effects...), themes: append([]string{}, themes...)}
	o.Set(effect, theme)
	return o
}

// Set moves the selection to effect/theme; unknown values fall back to the
// first choice.
func (o *Options) Set(effect, theme string) {
	o.eff = indexOf(o.effects, effect)
	o.thr = indexOf(o.themes, theme)
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}

func (o *Options) Open() bool     { return o.open }
func (o *Options) Selected() int  { return o.sel }
func (o *Options) Effect() string { return o.effects[o.eff] }
func (o *Options) Theme() string  { return o.themes[o.thr] }

// Close hides the menu without changing the selection.
func (o *Options) Close() { o.open = false }

// Rows returns the display labels for the two menu rows.
func (o *Options) Rows() []string {
	return []string{"Background: " + o.Effect(), "Theme: " + o.Theme()}
}

// Press applies one key and reports whether the selection changed.
func (o *Options) Press(k Key) bool {
	if k.Released {
		return false
	}
	if k.F1 || k.Escape {
		if o.open {
			o.Close()
		} else if k.F1 {
			o.open = true
			o.sel = 0
		}
		return false
	}
	if !o.open {
		return false
	}
	switch {
	case k.Up:
		o.sel = (o.sel + 1) % 2
	case k.Down:
		o.sel = (o.sel + 1) % 2
	case k.Left:
		o.cycle(-1)
		return true
	case k.Right, k.Enter:
		o.cycle(1)
		return true
	}
	return false
}

func (o *Options) cycle(dir int) {
	if o.sel == 0 {
		o.eff = (o.eff + dir + len(o.effects)) % len(o.effects)
		return
	}
	o.thr = (o.thr + dir + len(o.themes)) % len(o.themes)
}
