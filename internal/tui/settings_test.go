package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
)

func newSettingsTestModel(t *testing.T) (model, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	keysPath := filepath.Join(dir, "keys.json")
	themePath := filepath.Join(dir, "theme.json")
	configPath := filepath.Join(dir, "config.json")
	cfg := config.Config{
		DeviceName:        "orpheus",
		PollInterval:      time.Second,
		KeysPath:          keysPath,
		Theme:             "default",
		ThemePath:         themePath,
		SettingsPath:      configPath,
		Crossfade:         false,
		CrossfadeSeconds:  3,
		AudioCacheEnabled: false,
		AudioCacheSizeMB:  1024,
	}
	return newModel(context.Background(), nil, nil, cfg, nil, make(chan librespot.ContextTracksResult, 1), nil), keysPath, themePath, configPath
}

func send(m model, msg tea.KeyMsg) model {
	next, _ := m.handleSettingsKey(msg)
	return next.(model)
}

func sendKey(m model, runes string) model {
	return send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(runes)})
}

func openViaKey(m model) model {
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	return next.(model)
}

func sendEnter(m model) model { return send(m, tea.KeyMsg{Type: tea.KeyEnter}) }
func sendEsc(m model) model   { return send(m, tea.KeyMsg{Type: tea.KeyEscape}) }

func TestSettingsModalOpenCloseCursor(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)

	next := openViaKey(m)
	if !next.ui.settings.open || next.ui.settings.mode != settingsModeRoot {
		t.Fatal("expected settings modal open in root mode")
	}

	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	if next.ui.settings.cursor != 1 {
		t.Fatalf("down should move cursor to 1, got %d", next.ui.settings.cursor)
	}
	next = send(next, tea.KeyMsg{Type: tea.KeyUp})
	if next.ui.settings.cursor != 0 {
		t.Fatalf("up should move cursor to 0, got %d", next.ui.settings.cursor)
	}

	next = sendEsc(next)
	if next.ui.settings.open {
		t.Fatal("esc should close the modal")
	}
}

func TestSettingsThemePickerLiveAppliesAndPersists(t *testing.T) {
	m, _, themePath, _ := newSettingsTestModel(t)
	applyTheme(themePresetState("default"))

	next := openViaKey(m)
	next = sendEnter(next) // theme row: open the picker
	if next.ui.settings.mode != settingsModeTheme {
		t.Fatal("enter on the theme row should open the theme picker")
	}

	// moving down previews the next theme live
	idx := next.ui.settings.themeCursor
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	if next.ui.settings.themeCursor != idx+1 {
		t.Fatalf("down should advance the theme cursor, got %d", next.ui.settings.themeCursor)
	}
	if next.ui.settings.themePreset == settingsThemeOrder[next.ui.settings.themeCursor] {
		t.Fatal("preset must not change until enter persists")
	}

	// enter persists the previewed theme
	next = sendEnter(next)
	picked := settingsThemeOrder[idx+1]
	if next.ui.settings.themePreset != themePresetName(picked) {
		t.Fatalf("persisted preset = %q, want %q", next.ui.settings.themePreset, themePresetName(picked))
	}
	if next.ui.settings.mode != settingsModeRoot {
		t.Fatal("enter should return to the settings root")
	}

	data, err := os.ReadFile(themePath)
	if err != nil {
		t.Fatalf("theme.json not written: %v", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("theme.json malformed: %v", err)
	}
	if saved["preset"] != themePresetName(picked) {
		t.Fatalf("theme.json preset = %v, want %q", saved["preset"], themePresetName(picked))
	}

	// the persisted marker survives a reload
	if got := themePresetName("minimal"); got != "minimal" {
		t.Fatalf("preset name normalization broken: %q", got)
	}
}

func TestSettingsThemePickerEscReverts(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)
	applyTheme(themePresetState("default"))

	next := openViaKey(m)
	next = sendEnter(next) // open picker
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	if next.ui.settings.themeBackup != "default" {
		t.Fatalf("backup preset = %q, want default", next.ui.settings.themeBackup)
	}
	next = sendEsc(next) // revert
	if next.ui.settings.mode != settingsModeRoot {
		t.Fatal("esc should return to the settings root")
	}
	if next.ui.settings.themePreset != "default" {
		t.Fatalf("reverted preset = %q, want default", next.ui.settings.themePreset)
	}
}

func TestSettingsKeyCaptureRoundTrip(t *testing.T) {
	m, keysPath, _, _ := newSettingsTestModel(t)

	next := openViaKey(m)
	next = send(next, tea.KeyMsg{Type: tea.KeyDown}) // to theme options row
	next = send(next, tea.KeyMsg{Type: tea.KeyDown}) // to keybinds row
	next = sendEnter(next)                           // -> keys list
	if next.ui.settings.mode != settingsModeKeys {
		t.Fatalf("enter on keybinds row should open the keys list, mode=%v", next.ui.settings.mode)
	}
	for _, entry := range settingsKeyActions {
		if entry.action == "play_pause" {
			break
		}
		next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	}
	next = sendEnter(next) // start capture
	if next.ui.settings.mode != settingsModeCapture || next.ui.settings.captureKey != "play_pause" {
		t.Fatalf("capture mode not entered: mode=%v capture=%q", next.ui.settings.mode, next.ui.settings.captureKey)
	}

	next = sendEsc(next)
	if next.ui.settings.mode != settingsModeKeys || next.ui.settings.captureKey != "" {
		t.Fatal("esc should cancel capture back to the keys list")
	}

	next = sendEnter(next) // capture again
	next = sendKey(next, "j")
	if next.ui.settings.pendingKey != "j" {
		t.Fatalf("capture should arm pending key after first press, got %q", next.ui.settings.pendingKey)
	}
	next = sendEnter(next) // confirm
	if next.ui.settings.captureKey != "" || next.ui.settings.pendingKey != "" {
		t.Fatal("capture should complete after enter confirm")
	}

	data, err := os.ReadFile(keysPath)
	if err != nil {
		t.Fatalf("keys.json not written: %v", err)
	}
	if string(data) != "{\n  \"play_pause\": [\n    \"j\"\n  ]\n}\n" {
		t.Fatalf("unexpected keys.json content: %q", string(data))
	}

	if !keyMatches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}, next.ui.keys.PlayPause) {
		t.Fatal("captured key should match play_pause after rebind")
	}
	if keyMatches(tea.KeyMsg{Type: tea.KeySpace}, next.ui.keys.PlayPause) {
		t.Fatal("space should no longer match play_pause after rebind")
	}
	if LoadKeys(keysPath)["play_pause"] == nil {
		t.Fatal("keys.json should carry the override for reload")
	}
}

func TestSettingsCrossfadePersistsToConfigFileWithRestartHint(t *testing.T) {
	m, _, _, configPath := newSettingsTestModel(t)

	next := openViaKey(m)
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	if next.ui.settings.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", next.ui.settings.cursor)
	}

	next = sendEnter(next) // toggle on
	if !next.ui.settings.crossfadeEnabled {
		t.Fatal("enter should toggle crossfade on")
	}
	if !next.ui.settings.restartRequiredCrossfade {
		t.Fatal("restart hint flag should be set")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"enabled": true`) || !strings.Contains(got, `"seconds": 3`) {
		t.Fatalf("crossfade not persisted:\n%s", got)
	}

	next = sendKey(next, "+") // + steps seconds
	if next.ui.settings.crossfadeSeconds != 4 {
		t.Fatalf("crossfadeSeconds = %v, want 4", next.ui.settings.crossfadeSeconds)
	}
	data, _ = os.ReadFile(configPath)
	if !strings.Contains(string(data), `"seconds": 4`) {
		t.Fatalf("seconds not persisted after step:\n%s", string(data))
	}
}

func TestSettingsCacheTogglePersistsToConfigFile(t *testing.T) {
	m, _, _, configPath := newSettingsTestModel(t)

	next := openViaKey(m)
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	next = send(next, tea.KeyMsg{Type: tea.KeyDown})
	if next.ui.settings.cursor != 4 {
		t.Fatalf("cursor = %d, want 4", next.ui.settings.cursor)
	}

	next = sendEnter(next)
	if !next.ui.settings.cacheEnabled || !next.ui.settings.restartRequiredCache {
		t.Fatal("enter should toggle cache on and flag restart")
	}

	next = sendKey(next, "-")
	if next.ui.settings.cacheSizeMB != 768 {
		t.Fatalf("cacheSizeMB = %d, want 768", next.ui.settings.cacheSizeMB)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"enabled": true`) || !strings.Contains(got, `"size_mb": 768`) {
		t.Fatalf("cache not persisted:\n%s", got)
	}
}

func TestSettingsCaptureEscAndNavigation(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)

	next := openViaKey(m)
	next = send(next, tea.KeyMsg{Type: tea.KeyDown}) // theme options row
	next = send(next, tea.KeyMsg{Type: tea.KeyDown}) // keybinds row
	next = sendEnter(next)                           // keys list
	next = sendEnter(next)                           // capture for "tab"
	if next.ui.settings.mode != settingsModeCapture {
		t.Fatal("expected capture mode")
	}
	next = sendEsc(next)
	if next.ui.settings.mode != settingsModeKeys {
		t.Fatal("esc in capture returns to the keys list")
	}
	next = sendEsc(next)
	if next.ui.settings.mode != settingsModeRoot || !next.ui.settings.open {
		t.Fatal("esc in keys list returns to root, modal still open")
	}
	next = sendEsc(next)
	if next.ui.settings.open {
		t.Fatal("esc on root should close the modal")
	}
}
