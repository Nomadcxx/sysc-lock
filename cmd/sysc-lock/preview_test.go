package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nomadcxx/sysc-lock/internal/art"
	"github.com/Nomadcxx/sysc-lock/internal/config"
)

func TestPreviewUsesDraftWithoutSessionOrConfigWrites(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := config.Path()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"effect":"fire","palette":"eldritch"}`)
	if err := os.WriteFile(p, original, 0600); err != nil {
		t.Fatal(err)
	}
	render := func(draft string) []byte {
		t.Helper()
		var out bytes.Buffer
		if err := runPreview(strings.NewReader(draft), &out); err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(out.Bytes()))
		if err != nil || img.Bounds().Dx() != 960 || img.Bounds().Dy() != 540 {
			t.Fatalf("preview dimensions/decode: %v, %v", img, err)
		}
		return out.Bytes()
	}
	a := render(`{"width":960,"height":540,"config":{"effect":"none","palette":"nord","clock_style":"plain","clock_24h":true}}`)
	b := render(`{"width":960,"height":540,"config":{"effect":"none","palette":"eldritch","clock_style":"kompaktblk","clock_24h":false}}`)
	if bytes.Equal(a, b) {
		t.Fatal("style, clock format and palette did not affect the preview")
	}
	got, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("preview changed config: %q, %v", got, err)
	}
}

func TestPreviewRejectsUnboundedOrInvalidInput(t *testing.T) {
	for _, body := range []string{"null", "[]", "{}{}", `{"width":0}`, `{"height":-1}`, `{"width":2147483647}`, `{"width":1920,"height":1920}`, `{"config":{"effect":"missing"}}`, `{"config":{"palette":"missing"}}`, strings.Repeat(" ", maxPreviewBytes+1), strings.Repeat(" ", maxPreviewBytes-1) + "{}"} {
		var out bytes.Buffer
		if err := runPreview(strings.NewReader(body), &out); err == nil || out.Len() != 0 {
			t.Fatalf("invalid input produced output or no error: %q (%d bytes)", body[:min(len(body), 80)], out.Len())
		}
	}
}

func TestDescriptionUsesLockerChoices(t *testing.T) {
	var out bytes.Buffer
	if err := writeDescription(&out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		ClockStyles []string      `json:"clock_styles"`
		Effects     []string      `json:"effects"`
		Defaults    config.Config `json:"defaults"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.ClockStyles, ",") != strings.Join(art.Names(), ",") || strings.Join(got.Effects, ",") != strings.Join(effectChoices(), ",") || got.Defaults.Effect != config.EffectNone {
		t.Fatalf("description does not match locker choices: %+v", got)
	}
}

func TestPreviewUsesSuppliedHeaderCatalogue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	conf := "ascii_custom=\"\"\"\nCUSTOM LOCK HEADER\n\"\"\"\n"
	var out bytes.Buffer
	body, _ := json.Marshal(map[string]any{"headers": conf, "config": map[string]any{"header": "ascii_custom", "text_effect": "none"}, "width": 1536, "height": 864})
	if err := runPreview(bytes.NewReader(body), &out); err != nil {
		t.Fatal(err)
	}
	a := append([]byte(nil), out.Bytes()...)
	out.Reset()
	if err := runPreview(strings.NewReader(`{"width":1536,"height":864}`), &out); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, out.Bytes()) {
		t.Fatal("preview ignored custom catalogue")
	}
	if _, err := os.Stat(config.HeadersPath()); !os.IsNotExist(err) {
		t.Fatal("preview wrote a header file")
	}
	out.Reset()
	if err := runPreview(strings.NewReader(`{"headers":"broken"}`), &out); err != nil {
		t.Fatal("bad decoration must fall back:", err)
	}
}

func TestDefaultPreviewShowsHeaderChoices(t *testing.T) {
	render := func(id string) []byte {
		t.Helper()
		var out bytes.Buffer
		body, _ := json.Marshal(map[string]any{"config": map[string]any{"header": id}})
		if err := runPreview(bytes.NewReader(body), &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	if bytes.Equal(render("ascii_1"), render("ascii_5")) {
		t.Fatal("ordinary shell preview dropped the selected header")
	}
}

func TestDescriptionListsCustomHeaderFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(config.HeadersPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.HeadersPath(), []byte("ascii_custom=\"\"\"\nCUSTOM\n\"\"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeDescription(&out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Headers []string `json:"headers"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Headers) != 1 || got.Headers[0] != "ascii_custom" {
		t.Fatalf("description ignored user catalogue: %v", got.Headers)
	}
}
