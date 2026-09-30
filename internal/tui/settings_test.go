package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
		KeysPath:          keysPath,
		Theme:             "default",
		ThemePath:         themePath,
		SettingsPath:      configPath,
		Crossfade:         false,
		CrossfadeSeconds:  3,
		AudioCacheEnabled: false,
		AudioCacheSizeMB:  1024,
	}
	return newModel(context.Background(), nil, cfg, nil, make(chan librespot.ContextTracksResult, 1), nil), keysPath, themePath, configPath
}

func send(m model, msg tea.KeyPressMsg) model {
	next, _ := m.handleSettingsKey(msg)
	return next.(model)
}

func sendKey(m model, runes string) model {
	code := '?'
	if r := []rune(runes); len(r) > 0 {
		code = r[0]
	}
	return send(m, tea.KeyPressMsg{Code: code, Text: runes})
}

func openViaKey(m model) model {
	next, _ := m.handleKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	return next.(model)
}

func sendEnter(m model) model { return send(m, tea.KeyPressMsg{Code: tea.KeyEnter}) }
func sendEsc(m model) model   { return send(m, tea.KeyPressMsg{Code: tea.KeyEscape}) }

func TestSettingsModalOpenCloseCursor(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)

	next := openViaKey(m)
	if !next.ui.settings.open || next.ui.settings.mode != settingsModeRoot {
		t.Fatal("expected settings modal open in root mode")
	}

	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	if next.ui.settings.cursor != 1 {
		t.Fatalf("down should move cursor to 1, got %d", next.ui.settings.cursor)
	}
	next = send(next, tea.KeyPressMsg{Code: tea.KeyUp})
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

	next := openViaKey(m)
	next = sendEnter(next) // theme row: open the picker
	if next.ui.settings.mode != settingsModeTheme {
		t.Fatal("enter on the theme row should open the theme picker")
	}

	// moving down previews the next theme live
	idx := next.ui.settings.themeCursor
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
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

	next := openViaKey(m)
	next = sendEnter(next) // open picker
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
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

func TestSettingsThemePickerDefersArtRegenToSave(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)
	m.refreshLikedSongsArt()
	t.Cleanup(func() {
		m.refreshLikedSongsArt()
	})

	defaultKey := likedArtPaletteKey(m.likedArtThemeColors())
	next := openViaKey(m)
	next = sendEnter(next) // open picker
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	// Live preview moves the styles but must not pay the procedural
	// cover regen per keypress: the art stays on the old palette.
	if likedArtKey != defaultKey {
		t.Fatal("preview must leave the liked-songs art unsynced until save")
	}
	next = sendEnter(next) // save: the art syncs once to the picked palette
	if next.ui.settings.mode != settingsModeRoot {
		t.Fatal("enter should return to the settings root")
	}
	if want := likedArtPaletteKey(next.likedArtThemeColors()); likedArtKey != want {
		t.Fatal("save must sync the liked-songs art to the picked palette")
	}
}

func TestSettingsKeyCaptureRoundTrip(t *testing.T) {
	m, keysPath, _, _ := newSettingsTestModel(t)

	next := openViaKey(m)
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown}) // to theme options row
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown}) // to keybinds row
	next = sendEnter(next)                                // -> keys list
	if next.ui.settings.mode != settingsModeKeys {
		t.Fatalf("enter on keybinds row should open the keys list, mode=%v", next.ui.settings.mode)
	}
	for _, entry := range settingsKeyActions {
		if entry.action == "play_pause" {
			break
		}
		next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
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

	if !keyMatches(tea.KeyPressMsg{Code: 'j', Text: "j"}, next.ui.keys.PlayPause) {
		t.Fatal("captured key should match play_pause after rebind")
	}
	if keyMatches(tea.KeyPressMsg{Code: tea.KeySpace}, next.ui.keys.PlayPause) {
		t.Fatal("space should no longer match play_pause after rebind")
	}
	if LoadKeys(keysPath)["play_pause"] == nil {
		t.Fatal("keys.json should carry the override for reload")
	}
}

func TestSettingsCrossfadePersistsToConfigFileWithRestartHint(t *testing.T) {
	m, _, _, configPath := newSettingsTestModel(t)

	next := openViaKey(m)
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
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
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
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
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown}) // theme options row
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown}) // keybinds row
	next = sendEnter(next)                                // keys list
	next = sendEnter(next)                                // capture for "tab"
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

// TestSettingsRootRowsDriveMenu pins the settings-root descriptor table:
// order and behavior class, plus cursor wrap that follows the table
// length instead of a hardcoded row count. Adding a row is one entry.
func TestSettingsRootRowsDriveMenu(t *testing.T) {
	wantLabels := []string{"Theme", "Theme options", "Keybinds", "Crossfade", "Audio cache", "Images"}
	if len(settingsRootRows) != len(wantLabels) {
		t.Fatalf("settingsRootRows has %d rows, want %d", len(settingsRootRows), len(wantLabels))
	}
	for i, row := range settingsRootRows {
		if row.label != wantLabels[i] {
			t.Fatalf("row %d label = %q, want %q", i, row.label, wantLabels[i])
		}
		if row.value == nil || row.activate == nil {
			t.Fatalf("row %q must have value and activate", row.label)
		}
		if row.kind == settingsRowOpen && row.adjust != nil {
			t.Fatalf("open row %q must ignore the volume keys", row.label)
		}
		if row.kind == settingsRowAdjustable && row.adjust == nil {
			t.Fatalf("adjustable row %q must handle the volume keys", row.label)
		}
	}

	m, _, _, _ := newSettingsTestModel(t)
	next := openViaKey(m)
	next.ui.settings.cursor = 0
	next = send(next, tea.KeyPressMsg{Code: tea.KeyUp})
	if next.ui.settings.cursor != len(settingsRootRows)-1 {
		t.Fatalf("up from 0 wrapped to %d, want %d", next.ui.settings.cursor, len(settingsRootRows)-1)
	}
	next = send(next, tea.KeyPressMsg{Code: tea.KeyDown})
	if next.ui.settings.cursor != 0 {
		t.Fatalf("down from last wrapped to %d, want 0", next.ui.settings.cursor)
	}
}

func TestKeysTableRendersRows(t *testing.T) {
	m := guardModel(t, frameVariant{width: 100, height: 40, tab: tabPlaylists})
	m.ui.settings.open = true
	m.ui.settings.mode = settingsModeKeys
	plain := ansi.Strip(m.View().Content)
	for _, want := range []string{"Action", "play/pause", "volume up", "next track"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("keys menu missing %q; body renders without rows", want)
		}
	}
}

// The keys table is built to the box content budget: bubbles cells carry
// their own Padding(0,1), so the columns share (w-4) and every rendered
// line lands exactly at the budget. Wider lines wrapped inside the box
// (the header divider's tail spilled into a stray fragment between the
// column titles and the rows) or were truncated at the viewport width
// (key labels lost their last characters).
func TestKeysTableLinesFitBudget(t *testing.T) {
	m := guardModel(t, frameVariant{width: 100, height: 40, modal: "settings-keys"})
	for _, w := range []int{12, 24, 40, 80, 120} {
		out := m.settingsKeysTable(w, 12).View()
		for line := range strings.SplitSeq(out, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("budget %d: table line rendered %d wide, want <= %d: %q", w, got, w, line)
			}
		}
		if !strings.Contains(ansi.Strip(out), "space") {
			t.Fatalf("budget %d: key label truncated away: %q", w, ansi.Strip(out))
		}
	}
}

// A wrapped table line leaves its overflow on the next screen row: the
// header divider's dash tail sat between the column titles and the first
// option. Assert the rendered modal keeps exactly the divider there.
func TestKeysModalDividerDoesNotWrap(t *testing.T) {
	for _, width := range []int{80, 100, 140} {
		m := guardModel(t, frameVariant{width: width, height: 40, modal: "settings-keys"})
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		idx := -1
		for i, line := range lines {
			if strings.Contains(line, "Action") && strings.Contains(line, "Key") {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("width %d: keys header row not found", width)
		}
		trim := func(s string) string { return strings.TrimSpace(strings.Trim(s, "│░ ")) }
		if div := trim(lines[idx+1]); div == "" || strings.Trim(div, "─") != "" {
			t.Fatalf("width %d: expected a divider under the keys header, got %q", width, lines[idx+1])
		}
		if next := trim(lines[idx+2]); next == "" || strings.Trim(next, "─") == "" {
			t.Fatalf("width %d: wrapped divider fragment between the column titles and the options: %q", width, lines[idx+2])
		}
	}
}

// The keys table always reflects the live keyMap: no dirty flag or cache
// stands between a rebind and the next render (the old memo could never
// engage — its writes landed on render-path copies).
func TestKeysTableReflectsRebindDirectly(t *testing.T) {
	m := testListModel()
	m.ui.settings.keysPath = filepath.Join(t.TempDir(), "keys.json")
	rowWith := func(out, label string) string {
		for line := range strings.SplitSeq(out, "\n") {
			if strings.Contains(line, label) {
				return line
			}
		}
		return ""
	}
	if row := rowWith(m.settingsKeysTable(100, 20).View(), "repeat"); !strings.Contains(row, "l") {
		t.Fatalf("expected loop row bound to l before rebind: %q", row)
	}
	if err := m.applyCapture("loop", "z"); err != nil {
		t.Fatalf("applyCapture: %v", err)
	}
	if row := rowWith(m.settingsKeysTable(100, 20).View(), "repeat"); !strings.Contains(row, "z") {
		t.Fatalf("expected loop row bound to z after rebind: %q", row)
	}
}
