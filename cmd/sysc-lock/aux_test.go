package main

import (
	"bytes"
	"encoding/json"
	"github.com/Nomadcxx/sysc-lock/internal/ambient"
	"github.com/Nomadcxx/sysc-lock/internal/input"
	"github.com/Nomadcxx/sysc-lock/internal/lockd"
	"github.com/Nomadcxx/sysc-lock/internal/power"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestEmitLockedHandshakeWritesTheShellLine(t *testing.T) {
	var buf bytes.Buffer
	emitLockedHandshake(&buf)
	if got := buf.String(); got != "sysc-lock: locked\n" {
		t.Fatalf("handshake = %q, want the stdout line the shell greps", got)
	}
}

func TestAuthIdentityIgnoresUserEnvironment(t *testing.T) {
	t.Setenv("USER", "somebody-else")
	want, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := currentUser()
	if err != nil || got != want.Username {
		t.Fatal(got, err)
	}
}

func TestBusyEntryCannotChange(t *testing.T) {
	m := &input.Model{}
	m.Append("secret")
	g := &enterGate{}
	if submit, err := g.handle(m, lockd.Key{Enter: true}); !submit || err != nil {
		t.Fatal(err)
	}
	for _, k := range []lockd.Key{{Text: "x"}, {Backspace: true}, {Escape: true}, {Enter: true}} {
		if submit, err := g.handle(m, k); submit || err != nil {
			t.Fatal("busy submitted", err)
		}
		if m.Password() != "secret" {
			t.Fatal("busy entry edited")
		}
	}
}

func TestLateAuthResultIgnored(t *testing.T) {
	g := &enterGate{}
	g.try(true)
	old := g.generation
	g.release()
	g.try(true)
	if g.accept(old, lockd.Locked) {
		t.Fatal("accepted stale generation")
	}
	if g.accept(g.generation, lockd.Terminated) {
		t.Fatal("accepted after teardown")
	}
	if !g.accept(g.generation, lockd.Locked) {
		t.Fatal("rejected current attempt")
	}
}

func TestEnterGateRejectsEmptyAndOverlap(t *testing.T) {
	var g enterGate
	if g.try(false) {
		t.Fatal("empty password must not start auth")
	}
	if !g.try(true) {
		t.Fatal("first Enter must start auth")
	}
	if g.try(true) {
		t.Fatal("overlapping Enter must be ignored")
	}
	g.release()
	if !g.try(true) {
		t.Fatal("after release, Enter must start auth again")
	}
}

func TestHiddenEntryOnlyReveals(t *testing.T) {
	m := &input.Model{}
	var r input.Reveal
	g := &enterGate{}
	now := time.Unix(100, 0)
	for _, k := range []lockd.Key{{Text: "x"}, {Enter: true}, {Backspace: true}, {}} {
		r = input.Reveal{}
		submit, err := g.press(m, &r, k, now)
		if submit || err != nil || len(m.Pass) != 0 {
			t.Fatalf("hidden entry accepted %+v", k)
		}
		if !r.Tick(now, false) {
			t.Fatalf("%+v did not reveal", k)
		}
	}
	if submit, err := g.press(m, &r, lockd.Key{Text: "p"}, now.Add(time.Second)); submit || err != nil || m.Password() != "p" {
		t.Fatal("the second key must type")
	}
}

func TestEscapeWipesAndHides(t *testing.T) {
	m := &input.Model{}
	var r input.Reveal
	g := &enterGate{}
	now := time.Unix(100, 0)
	r.Show(now)
	g.press(m, &r, lockd.Key{Text: "secret"}, now)
	if m.Password() != "secret" {
		t.Fatal("setup")
	}
	g.press(m, &r, lockd.Key{Escape: true}, now.Add(time.Second))
	if len(m.Pass) != 0 || r.Tick(now.Add(time.Second), true) {
		t.Fatal("Esc must clear the field and hide the entry")
	}
	for _, c := range m.Pass[:cap(m.Pass)] {
		if c != 0 {
			t.Fatal("Esc left password runes in the buffer")
		}
	}
}

func TestEscapeDuringVerificationKeepsEntry(t *testing.T) {
	m := &input.Model{}
	m.Append("secret")
	var r input.Reveal
	g := &enterGate{}
	now := time.Unix(100, 0)
	r.Show(now)
	if submit, _ := g.press(m, &r, lockd.Key{Enter: true}, now); !submit {
		t.Fatal("setup")
	}
	g.press(m, &r, lockd.Key{Escape: true}, now.Add(time.Second))
	if m.Password() != "secret" || !r.Tick(now.Add(time.Second), true) {
		t.Fatal("verification must not be interrupted by Esc")
	}
}

func TestPopupKeysNeverReachThePasswordBuffer(t *testing.T) {
	m := power.New(power.DefaultOrder, power.Availability{Reboot: true, Shutdown: true}, "c2")
	m.Press(power.Key{F4: true}, time.Now())
	model := &input.Model{}
	model.Append("secret")
	gate := &enterGate{}
	r := &input.Reveal{}
	r.Show(time.Now())
	for _, k := range []lockd.Key{
		{Text: "x"}, {Backspace: true}, {Up: true}, {Down: true}, {Enter: true},
		{Enter: true, Released: true}, {F4: true},
	} {
		submit, err := gate.pressMenu(model, r, powerKeys(k), m, time.Now())
		if submit || err != nil {
			t.Fatalf("a popup key submitted the entry: %v %v", submit, err)
		}
		if string(model.Pass) != "secret" {
			t.Fatalf("the buffer changed on %+v: %q", k, model.Pass)
		}
	}
	if !r.Tick(time.Now(), false) {
		t.Fatal("the entry must stay visible while the popup is open")
	}
}

func TestEscapeWithThePopupOpenKeepsTheEntry(t *testing.T) {
	m := power.New(power.DefaultOrder, power.Availability{Reboot: true, Shutdown: true}, "c2")
	m.Press(power.Key{F4: true}, time.Now())
	model := &input.Model{}
	gate := &enterGate{}
	r := &input.Reveal{}
	now := time.Now()
	r.Show(now)
	if _, err := gate.pressMenu(model, r, power.Key{Escape: true}, m, now); err != nil {
		t.Fatal(err)
	}
	if m.Open() {
		t.Fatal("escape closes the popup")
	}
	if !r.Tick(now, false) {
		t.Fatal("escape with the popup open must not hide the entry")
	}
}

func TestPopupIsInertWhileVerifying(t *testing.T) {
	m := power.New(power.DefaultOrder, power.Availability{Reboot: true, Shutdown: true}, "c2")
	gate := &enterGate{busy: true}
	r := &input.Reveal{}
	if submit, err := gate.pressMenu(&input.Model{}, r, power.Key{F4: true}, m, time.Now()); submit || err != nil {
		t.Fatal("F4 is ignored while PAM verifies", submit, err)
	}
	if m.Open() {
		t.Fatal("the popup must not open during verification")
	}
}

func TestSubmittingCancelsThePopupAndAnyHold(t *testing.T) {
	m := power.New(power.DefaultOrder, power.Availability{Reboot: true, Shutdown: true}, "c2")
	m.Press(power.Key{F4: true}, time.Now())
	model := &input.Model{}
	model.Append("secret")
	gate := &enterGate{}
	r := &input.Reveal{}
	r.Show(time.Now())
	m.Press(power.Key{Enter: true}, time.Now())
	if !m.Holding() {
		t.Fatal("the hold must be running before we submit")
	}
	m.Close() // what the submit path does
	submit, err := gate.press(model, r, lockd.Key{Enter: true}, time.Now())
	if !submit || err != nil {
		t.Fatal("enter with the popup closed submits", submit, err)
	}
	if m.Holding() {
		t.Fatal("starting a verification cancels the hold")
	}
	if a := m.Tick(time.Now().Add(time.Hour)); a != "" {
		t.Fatal("a cancelled hold can never fire", a)
	}
}

func writeAmbientSnapshot(t *testing.T, asOf time.Time) (string, time.Time) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ambient.json")
	now := time.Unix(900, 0)
	if asOf.IsZero() {
		asOf = now
	}
	s := ambient.Snapshot{AsOf: asOf, Unit: "celsius"}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path, now
}

func TestLoadAmbientMissingAndStale(t *testing.T) {
	now := time.Unix(900, 0)
	if got := loadAmbient("/definitely/missing.json", now, 40); got != "" {
		t.Fatalf("missing file: got %q, want \"\"", got)
	}
	path, now := writeAmbientSnapshot(t, now.Add(-6*time.Second))
	if got := loadAmbient(path, now, 40); got != "" {
		t.Fatalf("stale file: got %q, want \"\"", got)
	}
}

func TestLoadAmbientGoodFile(t *testing.T) {
	path, now := writeAmbientSnapshot(t, time.Unix(0, 0))
	if got := loadAmbient(path, now, 40); got != "" {
		t.Fatalf("empty snapshot: got %q, want \"\"", got)
	}

	dir := t.TempDir()
	path = filepath.Join(dir, "ambient.json")
	pct := 82
	temp := 18.2
	body, err := json.Marshal(ambient.Snapshot{
		AsOf:       now,
		BatteryPct: &pct,
		Link:       ambient.LinkWifi,
		Media:      ambient.Playing,
		Temp:       &temp,
		Unit:       "celsius",
	})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got, want := loadAmbient(path, now, 40), "82% • Wi-Fi • playing • 18°"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestKillAmbientNilIsSafe(t *testing.T) {
	t.Cleanup(killAmbient(nil))
}

func TestKillAmbientReapsAChildThatIgnoresTerm(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", `trap "" TERM; echo x > "$1"; while true; do sleep 1; done`, "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("child never armed the TERM trap")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	killAmbient(cmd)()
	if dt := time.Since(start); dt < time.Second || dt > 3*time.Second {
		t.Fatalf("SIGKILL path took %v, want about 1.5s", dt)
	}
}

func TestAmbientRowReadsOncePerSecond(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ambient.json")
	base := time.Unix(900, 0)
	write := func(pct int) {
		t.Helper()
		body, err := json.Marshal(ambient.Snapshot{AsOf: base, BatteryPct: &pct})
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	write(82)
	row := ambientRow{path: path}
	if got, want := row.Get(base, 40), "82%"; got != want {
		t.Fatalf("first read: got %q, want %q", got, want)
	}
	write(50)
	if got, want := row.Get(base.Add(500*time.Millisecond), 40), "82%"; got != want {
		t.Fatalf("within the same second: got %q, want cached %q", got, want)
	}
	if got, want := row.Get(base.Add(1100*time.Millisecond), 40), "50%"; got != want {
		t.Fatalf("a second later: got %q, want refreshed %q", got, want)
	}
}

func TestEnterReleaseNeverSubmits(t *testing.T) {
	m := &input.Model{}
	if err := m.Append("secret"); err != nil {
		t.Fatal(err)
	}
	g := &enterGate{}
	if submit, err := g.handle(m, lockd.Key{Enter: true, Released: true}); submit || err != nil {
		t.Fatal("Enter key-up started auth (issue #10)")
	}
	if g.busy {
		t.Fatal("Enter key-up armed the gate")
	}
	if m.Password() != "secret" {
		t.Fatal("Enter key-up changed the entry")
	}
}

func TestCtrlVAppendsClipboard(t *testing.T) {
	m := &input.Model{}
	g := &enterGate{paste: func() string { return "s3cr3t" }}
	if submit, err := g.handle(m, lockd.Key{Ctrl: true, Text: "v"}); submit || err != nil {
		t.Fatal("Ctrl+V submitted auth")
	}
	if got := m.Password(); got != "s3cr3t" {
		t.Fatalf("got: %q", got)
	}
	if g.busy {
		t.Fatal("Ctrl+V armed the gate")
	}
}

func TestCtrlCombosNeverType(t *testing.T) {
	m := &input.Model{}
	g := &enterGate{paste: func() string { return "x" }}
	for _, k := range []lockd.Key{
		{Ctrl: true, Text: "x"},
		{Ctrl: true},
		{Ctrl: true, Enter: true},
	} {
		if submit, err := g.handle(m, k); submit || err != nil {
			t.Fatalf("ctrl combo %v touched auth", k)
		}
	}
	if m.Password() != "" {
		t.Fatal("ctrl combo typed into the entry")
	}
}
