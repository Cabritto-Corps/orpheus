package tui

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"orpheus/internal/config"
	"orpheus/internal/loader"
)

func imageStyleTestEnv(env map[string]string) func(string) string {
	return func(key string) string {
		return env[key]
	}
}

func writeImageStyleConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestStartupImageStylePrecedence(t *testing.T) {
	tests := []struct {
		name          string
		config        string
		env           map[string]string
		wantSelection string
		wantManaged   bool
		wantProtocol  imageProtocol
		wantExplicit  bool
	}{
		{
			name:          "forced pixelated beats kitty terminal",
			config:        `{"images":{"style":"pixelated"}}`,
			env:           map[string]string{"ORPHEUS_IMAGE_PROTOCOL": "kitty", "KITTY_WINDOW_ID": "1"},
			wantSelection: config.ImageStylePixelated,
			wantManaged:   true,
			wantProtocol:  imageProtocolNone,
			wantExplicit:  true,
		},
		{
			name:          "explicit rendered bypasses env none on capable terminal",
			config:        `{"images":{"style":"rendered"}}`,
			env:           map[string]string{"ORPHEUS_IMAGE_PROTOCOL": "none", "KITTY_WINDOW_ID": "1"},
			wantSelection: config.ImageStyleRendered,
			wantManaged:   true,
			wantProtocol:  imageProtocolKitty,
			wantExplicit:  false,
		},
		{
			name:          "invalid config falls back to environment",
			config:        `{"images":{"style":"oil-painting"}}`,
			env:           map[string]string{"ORPHEUS_IMAGE_PROTOCOL": "kitty"},
			wantSelection: "",
			wantManaged:   false,
			wantProtocol:  imageProtocolKitty,
			wantExplicit:  true,
		},
		{
			name:          "unset config preserves env none",
			config:        "",
			env:           map[string]string{"ORPHEUS_IMAGE_PROTOCOL": "none", "KITTY_WINDOW_ID": "1"},
			wantSelection: "",
			wantManaged:   false,
			wantProtocol:  imageProtocolNone,
			wantExplicit:  true,
		},
		{
			name:          "unset config auto-detects kitty",
			config:        "",
			env:           map[string]string{"KITTY_WINDOW_ID": "1"},
			wantSelection: "",
			wantManaged:   false,
			wantProtocol:  imageProtocolKitty,
			wantExplicit:  false,
		},
		{
			name:          "unset config falls back without terminal support",
			config:        "",
			env:           map[string]string{},
			wantSelection: "",
			wantManaged:   false,
			wantProtocol:  imageProtocolNone,
			wantExplicit:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeImageStyleConfig(t, tc.config)
			selection, managed := config.ExplicitImageStyle(path)
			if selection != tc.wantSelection || managed != tc.wantManaged {
				t.Fatalf("selection = (%q, %v), want (%q, %v)", selection, managed, tc.wantSelection, tc.wantManaged)
			}
			cache := newImgCacheWithSelection(selection, managed, imageStyleTestEnv(tc.env))
			if cache.protocolForRender() != tc.wantProtocol {
				t.Fatalf("protocol = %v, want %v", cache.protocolForRender(), tc.wantProtocol)
			}
			if cache.protocolExplicit != tc.wantExplicit {
				t.Fatalf("protocolExplicit = %v, want %v", cache.protocolExplicit, tc.wantExplicit)
			}
		})
	}
}

func TestForcedPixelatedNeverUsesKitty(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	getenv := imageStyleTestEnv(map[string]string{"KITTY_WINDOW_ID": "1"})
	cache := newImgCacheWithSelection(config.ImageStylePixelated, true, getenv)
	if cache.protocolForRender() != imageProtocolNone {
		t.Fatalf("forced pixelated must use the half-block protocol, got %v", cache.protocolForRender())
	}
	if !cache.protocolExplicit {
		t.Fatal("forced pixelated must be explicit so fallback cannot restore kitty")
	}

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	img.SetRGBA(1, 1, color.RGBA{G: 255, A: 255})
	rendered := renderCover(cache.protocolForRender(), img, "", 4, 2)
	if !strings.Contains(rendered, "▀") {
		t.Fatalf("expected half-block output, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b_G") {
		t.Fatalf("forced pixelated must not emit kitty escapes, got %q", rendered)
	}
}

func TestManagedRenderedFallsBackToHalfBlockWithoutKitty(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	cache := newImgCacheWithSelection(config.ImageStyleRendered, true, imageStyleTestEnv(map[string]string{}))
	if cache.protocolForRender() != imageProtocolNone {
		t.Fatalf("rendered without kitty support must fall back, got %v", cache.protocolForRender())
	}
	if cache.protocolExplicit {
		t.Fatal("managed rendered fallback must stay eligible for kitty recovery")
	}

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	rendered := renderCover(cache.protocolForRender(), img, "", 4, 2)
	if !strings.Contains(rendered, "▀") {
		t.Fatalf("expected half-block fallback output, got %q", rendered)
	}
}

func TestSettingsImageStyleCyclePersistsAndApplies(t *testing.T) {
	t.Setenv("ORPHEUS_IMAGE_PROTOCOL", "")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	m, _, _, configPath := newSettingsTestModel(t)

	next := openViaKey(m)
	for range 5 {
		next = send(next, teaDown())
	}
	if next.ui.settings.cursor != 5 {
		t.Fatalf("cursor = %d, want images row 5", next.ui.settings.cursor)
	}
	if next.ui.settings.imageStyle != config.ImageStyleRendered || next.ui.settings.imageStyleSet {
		t.Fatalf("default images row = (%q, set=%v), want (rendered, false)", next.ui.settings.imageStyle, next.ui.settings.imageStyleSet)
	}

	next = sendEnter(next)
	if next.ui.settings.imageStyle != config.ImageStylePixelated || !next.ui.settings.imageStyleSet {
		t.Fatalf("enter must select pixelated, got (%q, set=%v)", next.ui.settings.imageStyle, next.ui.settings.imageStyleSet)
	}
	if next.ui.imgs.protocolForRender() != imageProtocolNone || !next.ui.imgs.protocolExplicit {
		t.Fatal("pixelated must apply immediately and lock out kitty")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	images, ok := saved["images"].(map[string]any)
	if !ok || images["style"] != config.ImageStylePixelated {
		t.Fatalf("images style not persisted as pixelated: %v", saved)
	}
	if out := func() string {
		view := next
		view.ui.width = 100
		view.ui.height = 40
		return view.settingsModalView()
	}(); !strings.Contains(out, "Images") || !strings.Contains(out, config.ImageStylePixelated) {
		t.Fatalf("settings row must show the pixelated choice:\n%s", out)
	}

	// The row cycles back to rendered with the same key used for the other rows.
	next = sendEnter(next)
	if next.ui.settings.imageStyle != config.ImageStyleRendered {
		t.Fatalf("enter must cycle back to rendered, got %q", next.ui.settings.imageStyle)
	}
	if next.ui.imgs.protocolForRender() != imageProtocolKitty {
		t.Fatal("rendered must apply immediately on a kitty-capable terminal")
	}
	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	saved = nil
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["images"].(map[string]any)["style"] != config.ImageStyleRendered {
		t.Fatalf("images style not persisted as rendered: %v", saved)
	}
}

func TestApplyImageStyleClearsKittyStateAndRecovery(t *testing.T) {
	getenv := imageStyleTestEnv(map[string]string{"KITTY_WINDOW_ID": "1"})
	m := model{ui: uiModel{imgs: newImgCacheWithSelection(config.ImageStyleRendered, true, getenv), cover: newCoverManager()}}
	m.ui.settings.imageStyle = config.ImageStyleRendered
	key := coverKey{url: "https://img/cover", cols: 8, rows: 8}
	m.ui.imgs.covers.Set(key, "stale kitty render")
	m.ui.imgs.coverKeysByURL[key.url] = map[coverKey]struct{}{key: {}}
	m.ui.imgs.encoded[key.url] = "stale encoding"
	m.ui.imgs.kittyChunks[key.url] = []string{"stale chunk"}
	m.ui.imgs.kittyChunkOrder = []string{key.url}
	m.ui.imgs.kittyVisible = true
	m.ui.imgs.lastKittyURL = key.url
	m.ui.cover.kittyFellBack = true
	m.ui.cover.kittyRecoveryStreak = 3
	m.ui.cover.playerCoverFailStreak = 2

	m.ui.settings.imageStyle = config.ImageStylePixelated
	nextModel, _ := m.applyImageStyleWithEnv(getenv)
	next := nextModel.(model)
	if next.ui.imgs.protocolForRender() != imageProtocolNone || !next.ui.imgs.protocolExplicit {
		t.Fatal("pixelated must force the half-block protocol and lock out kitty")
	}
	if _, ok := next.ui.imgs.covers.Get(key); ok {
		t.Fatal("stale rendered covers must be invalidated on style change")
	}
	if len(next.ui.imgs.coverKeysByURL) != 0 || len(next.ui.imgs.encoded) != 0 || len(next.ui.imgs.kittyChunks) != 0 || len(next.ui.imgs.kittyChunkOrder) != 0 {
		t.Fatal("style-specific encoded and chunk state must be invalidated on style change")
	}
	if next.ui.imgs.kittyVisible || !next.ui.imgs.kittyForceRedraw {
		t.Fatal("style change must reset the kitty overlay state")
	}
	if next.ui.cover.kittyFellBack || next.ui.cover.kittyRecoveryStreak != 0 || next.ui.cover.playerCoverFailStreak != 0 {
		t.Fatal("style change must clear stale kitty supervision state")
	}
	for range kittyProtocolRecoveryStreak * 2 {
		next.maybeRecoverKittyProtocol()
	}
	if next.ui.imgs.protocolForRender() != imageProtocolNone {
		t.Fatal("kitty recovery must not fight an explicit pixelated choice")
	}
	if next.ui.cover.kittyRecoveryStreak != 0 {
		t.Fatalf("recovery streak must not accumulate under forced pixelated, got %d", next.ui.cover.kittyRecoveryStreak)
	}
}

func TestStartupImageStyleLoadsExplicitConfig(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "config.json")
	pixelated := config.ImageStylePixelated
	if err := config.SaveAppSettings(settingsPath, config.AppSettings{Images: &config.ImageStyleSettings{Style: &pixelated}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DeviceName: "orpheus", SettingsPath: settingsPath}
	m := newModel(context.Background(), nil, cfg, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), nil)))
	if m.ui.settings.imageStyle != config.ImageStylePixelated || !m.ui.settings.imageStyleSet {
		t.Fatalf("startup must load explicit config style, got (%q, set=%v)", m.ui.settings.imageStyle, m.ui.settings.imageStyleSet)
	}
	if m.ui.imgs.protocolForRender() != imageProtocolNone || !m.ui.imgs.protocolExplicit {
		t.Fatal("startup must force half-block rendering for explicit pixelated")
	}
}
