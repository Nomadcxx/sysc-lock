package lockd

import (
	"context"
	xkb "github.com/thegrumpylion/xkb-go"
	"testing"
	"time"
)

func TestEnterDoesNotRepeat(t *testing.T) {
	now := time.Now()
	r := keyRepeat{rate: 25, delay: 300 * time.Millisecond}
	r.press(28, Key{Enter: true}, now)
	if !r.next.IsZero() {
		t.Fatal("Enter repeats")
	}
	r.press(14, Key{Backspace: true}, now)
	if r.next.IsZero() || r.due(now.Add(200*time.Millisecond)) {
		t.Fatal("bad delay")
	}
	if !r.due(now.Add(300 * time.Millisecond)) {
		t.Fatal("Backspace did not repeat")
	}
	r.release(14)
	if !r.next.IsZero() {
		t.Fatal("release retained repeat")
	}
}
func TestCapsUpdatesOnModifierEvent(t *testing.T) {
	ctx := xkb.NewContext(context.Background(), xkb.ContextNoFlags)
	km, err := ctx.NewKeymapFromNames(&xkb.RuleNames{Layout: "us"})
	if err != nil {
		t.Fatal(err)
	}
	k := &keymap{state: km.NewState(), mapData: km}
	c := &Client{keymap: k}
	var got Key
	c.onKey = func(key Key) { got = key }
	c.updateModifiers(0, 0, 2, 0)
	if !got.CapsLock || got.Layout == "" {
		t.Fatal("modifier did not update UI", got)
	}
}
func TestComposeKeepsFirstCharacter(t *testing.T) {
	ctx := xkb.NewContext(context.Background(), xkb.ContextNoFlags)
	km, err := ctx.NewKeymapFromNames(&xkb.RuleNames{Layout: "us", Variant: "intl"})
	if err != nil {
		t.Fatal(err)
	}
	k := &keymap{state: km.NewState(), mapData: km}
	table, err := ctx.NewComposeTableFromLocale("en_US.UTF-8", xkb.ComposeCompileNoFlags)
	if err != nil {
		t.Fatal(err)
	}
	k.compose = table.NewState(xkb.ComposeStateNoFlags)
	if _, text := k.resolve(30); text != "a" {
		t.Fatal("swallowed first character", text)
	}
	if _, text := k.resolve(40); text != "" {
		t.Fatal("dead key inserted", text)
	}
	if _, text := k.resolve(18); text != "é" {
		t.Fatal("compose", text)
	}
}

func TestRepeatUsesCurrentModifiers(t *testing.T) {
	ctx := xkb.NewContext(context.Background(), xkb.ContextNoFlags)
	km, err := ctx.NewKeymapFromNames(&xkb.RuleNames{Layout: "us"})
	if err != nil {
		t.Fatal(err)
	}
	k := &keymap{state: km.NewState(), mapData: km}
	c := &Client{keymap: k, repeat: keyRepeat{code: 30, key: Key{Text: "a"}}}
	c.updateModifiers(1, 0, 0, 0)
	if got := c.repeatKey(); got.Text != "A" {
		t.Fatal("repeat retained old modifier text", got)
	}
}

func TestComposedRepeatKeepsCommittedText(t *testing.T) {
	ctx := xkb.NewContext(context.Background(), xkb.ContextNoFlags)
	km, err := ctx.NewKeymapFromNames(&xkb.RuleNames{Layout: "us", Variant: "intl"})
	if err != nil {
		t.Fatal(err)
	}
	k := &keymap{state: km.NewState(), mapData: km}
	table, err := ctx.NewComposeTableFromLocale("en_US.UTF-8", xkb.ComposeCompileNoFlags)
	if err != nil {
		t.Fatal(err)
	}
	k.compose = table.NewState(xkb.ComposeStateNoFlags)
	_, _ = k.resolve(40)
	_, text := k.resolve(18)
	c := &Client{keymap: k, repeat: keyRepeat{code: 18, key: Key{Text: text, composed: k.lastComposed}}}
	if got := c.repeatKey().Text; got != "é" {
		t.Fatal("composed repeat changed credential text", got)
	}
}

func TestSpecialsMapsEveryKeyTheMenuNeeds(t *testing.T) {
	for _, tc := range []struct {
		sym                 uint32
		enter, up, down, f4 bool
	}{
		{symReturn, true, false, false, false},
		{symKP_Enter, true, false, false, false},
		{symUp, false, true, false, false},
		{symDown, false, false, true, false},
		{symF4, false, false, false, true},
		{symBackspace, false, false, false, false},
		{symEscape, false, false, false, false},
		{'a', false, false, false, false},
		{0xffcb, false, false, false, false}, // XK_F14, not F4
	} {
		enter, up, down, f4 := specials(tc.sym)
		if enter != tc.enter || up != tc.up || down != tc.down || f4 != tc.f4 {
			t.Fatalf("%#x -> %v %v %v %v", tc.sym, enter, up, down, f4)
		}
	}
}

func TestCtrlUpdatesIndicators(t *testing.T) {
	ctx := xkb.NewContext(context.Background(), xkb.ContextNoFlags)
	km, err := ctx.NewKeymapFromNames(&xkb.RuleNames{Layout: "us"})
	if err != nil {
		t.Fatal(err)
	}
	k := &keymap{state: km.NewState(), mapData: km}
	c := &Client{keymap: k}
	var got Key
	c.onKey = func(key Key) { got = key }
	c.updateModifiers(1<<modIndexCtrl, 0, 0, 0)
	if !got.Ctrl {
		t.Fatal("ctrl depressed not reported", got)
	}
	c.updateModifiers(0, 0, 0, 0)
	if got.Ctrl {
		t.Fatal("ctrl release not reported", got)
	}
}
