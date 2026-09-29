package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func writeKeysFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadKeyOverridesValidFile(t *testing.T) {
	path := writeKeysFile(t, `{"next": "j", "play_pause": [" ", "x"]}`)
	overrides := LoadKeys(path)
	if len(overrides["next"]) != 1 || overrides["next"][0] != "j" {
		t.Fatalf("next override = %v", overrides["next"])
	}
	if len(overrides["play_pause"]) != 2 {
		t.Fatalf("play_pause override = %v", overrides["play_pause"])
	}
}

func TestLoadKeyOverridesMissingFileUsesDefaults(t *testing.T) {
	overrides := LoadKeys(filepath.Join(t.TempDir(), "nope.json"))
	if len(overrides) != 0 {
		t.Fatalf("missing file should produce no overrides, got %v", overrides)
	}
}

func TestLoadKeyOverridesMalformedFileUsesDefaults(t *testing.T) {
	overrides := LoadKeys(writeKeysFile(t, `{not json`))
	if len(overrides) != 0 {
		t.Fatalf("malformed file should produce no overrides, got %v", overrides)
	}
}

func TestLoadKeyOverridesUnknownActionIgnored(t *testing.T) {
	overrides := LoadKeys(writeKeysFile(t, `{"next": "j", "explode": "j"}`))
	if _, ok := overrides["explode"]; ok {
		t.Fatal("unknown action must be dropped")
	}
	if len(overrides["next"]) != 1 || overrides["next"][0] != "j" {
		t.Fatalf("known action must survive, got %v", overrides["next"])
	}
}

func TestLoadKeyOverridesUnknownKeyFallsBack(t *testing.T) {
	overrides := LoadKeys(writeKeysFile(t, `{"next": ["j", "mode_broken"]}`))
	for _, k := range overrides["next"] {
		if k == "mode_broken" {
			t.Fatal("unrecognized key must be dropped")
		}
	}
}

// TestIsPlausibleKeyNameV2Forms pins the loader vocabulary after the v2
// migration: canonical modifier combos and legacy aliases load, and so do
// the ultraviolet function/keypad/media names (kitty terminals report
// them; previously unloadable), while garbage still drops.
func TestIsPlausibleKeyNameV2Forms(t *testing.T) {
	for _, name := range []string{"shift+up", "alt+x", "ctrl+shift+enter", "escape", "return", " ", "space", "+", "f1", "f5", "f24", "f63", "mute", "kpenter", "begin", "ctrl+f5"} {
		if !isPlausibleKeyName(name) {
			t.Fatalf("%q must load", name)
		}
	}
	for _, name := range []string{"", "mode_broken", "ctrl+", "f0", "f64", "f99", "kpop", "ctrl", "shift"} {
		if isPlausibleKeyName(name) {
			t.Fatalf("%q must not load", name)
		}
	}
}

func TestLoadKeyOverridesEmptyListKeepsDefault(t *testing.T) {
	if overrides := LoadKeys(writeKeysFile(t, `{"next": []}`)); len(overrides) != 0 {
		t.Fatalf("empty key list must keep the default binding, got %v", overrides)
	}
}

func TestGoldenNextOverrideToJ(t *testing.T) {
	m := newKeysFromConfig(map[string][]string{"next": {"j"}})
	if !keyMatches(tea.KeyPressMsg{Code: 'j', Text: "j"}, m.Next) {
		t.Fatal("j must trigger next after override")
	}
	if keyMatches(tea.KeyPressMsg{Code: 'n', Text: "n"}, m.Next) {
		t.Fatal("n must no longer trigger next after override")
	}
}

func TestQuitAlwaysKeepsCtrlC(t *testing.T) {
	overrides := LoadKeys(writeKeysFile(t, `{"quit": "esc"}`))
	m := newKeysFromConfig(overrides)
	if !slices.Contains(m.Quit.Keys(), "ctrl+c") {
		t.Fatalf("quit must always contain ctrl+c, got %v", m.Quit.Keys())
	}
	if !slices.Contains(m.Quit.Keys(), "esc") {
		t.Fatalf("user quit key missing, got %v", m.Quit.Keys())
	}
}

func TestQuitOverrideArrayAlsoKeepsCtrlC(t *testing.T) {
	overrides := LoadKeys(writeKeysFile(t, `{"quit": ["esc", "ctrl+z"]}`))
	m := newKeysFromConfig(overrides)
	for _, want := range []string{"esc", "ctrl+z", "ctrl+c"} {
		if !slices.Contains(m.Quit.Keys(), want) {
			t.Fatalf("quit keys = %v, missing %q", m.Quit.Keys(), want)
		}
	}
}

func TestNewKeysFromConfigDefaultsWhenEmpty(t *testing.T) {
	def, cfg := newKeys(), newKeysFromConfig(nil)
	if def.Next.Keys()[0] != cfg.Next.Keys()[0] || def.PlayPause.Keys()[0] != cfg.PlayPause.Keys()[0] {
		t.Fatal("empty overrides must equal defaults")
	}
}

func TestLoadKeyOverridesWritesRoundTrip(t *testing.T) {
	in := map[string]any{"next": "j"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	overrides := LoadKeys(path)
	if len(overrides["next"]) != 1 || overrides["next"][0] != "j" {
		t.Fatalf("round trip failed: %v", overrides)
	}
}

func TestOverrideBindingHelpLabelFollowsRebind(t *testing.T) {
	b := key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next"))
	re := overrideBinding(b, []string{"j"})
	if re.Help().Key != "j" {
		t.Fatalf("help key label = %q, want %q", re.Help().Key, "j")
	}
	sp := overrideBinding(key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "play/pause")), []string{" "})
	if sp.Help().Key != "space" {
		t.Fatalf("space label should render as 'space', got %q", sp.Help().Key)
	}
}

// TestApplyKeyOverridesCoversRegistry pins the registry-driven loop: every
// registry action must round-trip through applyKeyOverrides, so a new
// action with a missing setter fails this test instead of silently
// staying unbindable.
func TestApplyKeyOverridesCoversRegistry(t *testing.T) {
	for _, meta := range actionRegistry {
		custom := []string{"f24"}
		got := applyKeyOverrides(newKeys(), map[string][]string{meta.action: custom})
		keys := meta.bind(got).Keys()
		want := custom
		if meta.action == "quit" {
			want = []string{"f24", "ctrl+c"}
		}
		if !slices.Equal(keys, want) {
			t.Fatalf("%s: got keys %v, want %v", meta.action, keys, want)
		}
	}
	// Unknown actions stay ignored.
	before := newKeys()
	after := applyKeyOverrides(before, map[string][]string{"nope": {"x"}})
	for _, meta := range actionRegistry {
		got, want := meta.bind(after).Keys(), meta.bind(before).Keys()
		if !slices.Equal(got, want) {
			t.Fatalf("unknown action changed %s: %v -> %v", meta.action, want, got)
		}
	}
}
