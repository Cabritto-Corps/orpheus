package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
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

func TestLoadKeyOverridesEmptyListKeepsDefault(t *testing.T) {
	if overrides := LoadKeys(writeKeysFile(t, `{"next": []}`)); len(overrides) != 0 {
		t.Fatalf("empty key list must keep the default binding, got %v", overrides)
	}
}

func TestGoldenNextOverrideToJ(t *testing.T) {
	m := newKeysFromConfig(map[string][]string{"next": {"j"}})
	if !keyMatches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}, m.Next) {
		t.Fatal("j must trigger next after override")
	}
	if keyMatches(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, m.Next) {
		t.Fatal("n must no longer trigger next after override")
	}
}

func TestKeysContainList(t *testing.T) {
	if !keysContainList([]string{"a", "b"}, "b") || keysContainList([]string{"a"}, "b") {
		t.Fatal("keysContainList broken")
	}
}

func TestQuitAlwaysKeepsCtrlC(t *testing.T) {
	overrides := LoadKeys(writeKeysFile(t, `{"quit": "esc"}`))
	m := newKeysFromConfig(overrides)
	if !keysContainList(m.Quit.Keys(), "ctrl+c") {
		t.Fatalf("quit must always contain ctrl+c, got %v", m.Quit.Keys())
	}
	if !keysContainList(m.Quit.Keys(), "esc") {
		t.Fatalf("user quit key missing, got %v", m.Quit.Keys())
	}
}

func TestQuitOverrideArrayAlsoKeepsCtrlC(t *testing.T) {
	overrides := LoadKeys(writeKeysFile(t, `{"quit": ["esc", "ctrl+z"]}`))
	m := newKeysFromConfig(overrides)
	for _, want := range []string{"esc", "ctrl+z", "ctrl+c"} {
		if !keysContainList(m.Quit.Keys(), want) {
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
