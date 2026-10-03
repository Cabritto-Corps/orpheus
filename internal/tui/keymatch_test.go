package tui

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func pressKey(code rune, text string, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text, Mod: mod}
}

func bindKeys(t *testing.T, keys ...string) key.Binding {
	t.Helper()
	return key.NewBinding(key.WithKeys(keys...))
}

// TestKeyMatchesV2Identity pins dispatch matching to v2 key identity
// (modifier flags + base code) instead of rendered-string equality: legacy
// aliases fold, modifier order is irrelevant, and shifted spellings do not
// collide with typed text.
func TestKeyMatchesV2Identity(t *testing.T) {
	cases := []struct {
		name  string
		msg   tea.KeyPressMsg
		spec  string
		match bool
	}{
		{"plain rune", pressKey('j', "j", 0), "j", true},
		{"plain rune mismatch", pressKey('j', "j", 0), "k", false},
		{"space by literal", pressKey(tea.KeySpace, " ", 0), " ", true},
		{"space by name", pressKey(tea.KeySpace, " ", 0), "space", true},
		{"space without text", pressKey(tea.KeySpace, "", 0), " ", true},
		{"enter", pressKey(tea.KeyEnter, "", 0), "enter", true},
		{"return alias folds to enter", pressKey(tea.KeyEnter, "", 0), "return", true},
		{"return code is the enter code", pressKey(tea.KeyReturn, "", 0), "enter", true},
		{"esc", pressKey(tea.KeyEscape, "", 0), "esc", true},
		{"escape alias folds to esc", pressKey(tea.KeyEscape, "", 0), "escape", true},
		{"shift tab by fields", pressKey(tea.KeyTab, "", tea.ModShift), "shift+tab", true},
		{"shift up by fields", pressKey(tea.KeyUp, "", tea.ModShift), "shift+up", true},
		{"ctrl c by fields", pressKey('c', "", tea.ModCtrl), "ctrl+c", true},
		{"alt x by fields", pressKey('x', "", tea.ModAlt), "alt+x", true},
		{"typed question mark", pressKey('?', "?", 0), "?", true},
		{"shift slash is not a typed question mark", pressKey('?', "?", 0), "shift+/", false},
		{"capital matches its shift spelling", pressKey('J', "J", 0), "shift+j", true},
		{"plus key is a base, not a separator", pressKey('+', "+", 0), "+", true},
		{"modifier order is irrelevant", pressKey('a', "", tea.ModCtrl|tea.ModShift), "shift+ctrl+a", true},
		{"modifier order canonical", pressKey('a', "", tea.ModCtrl|tea.ModShift), "ctrl+shift+a", true},
		{"missing modifier does not match", pressKey('n', "n", 0), "ctrl+n", false},
		{"extra modifier does not match", pressKey('c', "", tea.ModCtrl), "ctrl+alt+c", false},
		{"garbage never matches", pressKey('j', "j", 0), "mode_broken", false},
		{"empty never matches", pressKey('j', "j", 0), "", false},
	}
	for _, tc := range cases {
		if got := keyMatches(tc.msg, bindKeys(t, tc.spec)); got != tc.match {
			t.Errorf("%s: keyMatches(%q, %q) = %v, want %v", tc.name, tc.msg.String(), tc.spec, got, tc.match)
		}
	}
}

// TestIsQuitSignalIsOnlyCtrlC is the evidence for not routing the top-level
// guarantee through the whole quit binding: "q" (and any other rebound quit
// key) must not punch through modals or key capture.
func TestIsQuitSignalIsOnlyCtrlC(t *testing.T) {
	if !isQuitSignal(pressKey('c', "", tea.ModCtrl)) {
		t.Fatal("ctrl+c must be the quit signal")
	}
	if !isQuitSignal(pressKey('C', "", tea.ModCtrl)) {
		t.Fatal("ctrl+shift+c must be the quit signal")
	}
	for _, msg := range []tea.KeyPressMsg{
		pressKey('q', "q", 0),
		pressKey('c', "c", 0),
		pressKey(tea.KeyEscape, "", 0),
		pressKey(tea.KeyEnter, "", 0),
		pressKey('x', "", tea.ModAlt),
	} {
		if isQuitSignal(msg) {
			t.Fatalf("%q must not be the quit signal", msg.String())
		}
	}
}

func TestCancelConfirmIdentity(t *testing.T) {
	if !isCancelPress(pressKey(tea.KeyEscape, "", 0)) {
		t.Fatal("esc must cancel")
	}
	if !isCancelPress(pressKey(tea.KeyEsc, "", 0)) {
		t.Fatal("escape alias must cancel")
	}
	if !isConfirmPress(pressKey(tea.KeyEnter, "", 0)) {
		t.Fatal("enter must confirm")
	}
	if !isConfirmPress(pressKey(tea.KeyReturn, "", 0)) {
		t.Fatal("return alias must confirm")
	}
	for _, msg := range []tea.KeyPressMsg{
		pressKey('q', "q", 0),
		pressKey('c', "", tea.ModCtrl),
		pressKey(tea.KeyTab, "", 0),
	} {
		if isCancelPress(msg) || isConfirmPress(msg) {
			t.Fatalf("%q must be neither cancel nor confirm", msg.String())
		}
	}
}

// TestCaptureCanonicalizesModifiedKeys pins the alt-prefix fix: a modified
// key renders once, in canonical modifier order, and control keys stay
// uncapturable.
func TestCaptureCanonicalizesModifiedKeys(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{"plain", pressKey('j', "j", 0), "j"},
		{"space", pressKey(tea.KeySpace, " ", 0), "space"},
		{"alt x keeps one prefix", pressKey('x', "", tea.ModAlt), "alt+x"},
		{"shift tab", pressKey(tea.KeyTab, "", tea.ModShift), "shift+tab"},
		{"ctrl c", pressKey('c', "", tea.ModCtrl), "ctrl+c"},
		{"esc uncapturable", pressKey(tea.KeyEscape, "", 0), ""},
		{"enter uncapturable", pressKey(tea.KeyEnter, "", 0), ""},
		{"bare modifier uncapturable", pressKey(tea.KeyLeftCtrl, "", tea.ModCtrl), ""},
	}
	for _, tc := range cases {
		if got := captureKeyName(tc.msg); got != tc.want {
			t.Errorf("%s: captureKeyName = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestLegacyKeysJSONBinds proves the compat contract: v1-era stored spellings
// (modifier-plus-key, bare space, "return") keep binding and matching after
// the v2 migration.
func TestLegacyKeysJSONBinds(t *testing.T) {
	path := writeKeysFile(t, `{"seek_fwd": "shift+up", "play_pause": " ", "select": "return", "filter": "alt+x", "quit": "ctrl+c"}`)
	k := newKeysFromConfig(LoadKeys(path))
	if !keyMatches(pressKey(tea.KeyUp, "", tea.ModShift), k.SeekFwd) {
		t.Fatal("shift+up must still drive seek_fwd")
	}
	if !keyMatches(pressKey(tea.KeySpace, " ", 0), k.PlayPause) {
		t.Fatal("bare space must still drive play_pause")
	}
	if !keyMatches(pressKey(tea.KeyEnter, "", 0), k.Select) {
		t.Fatal("return must still drive select")
	}
	if !keyMatches(pressKey('x', "", tea.ModAlt), k.Filter) {
		t.Fatal("alt+x must still drive filter")
	}
	if !keyMatches(pressKey('c', "", tea.ModCtrl), k.Quit) {
		t.Fatal("ctrl+c must still drive quit")
	}
	if keyMatches(pressKey('n', "n", 0), k.SeekFwd) {
		t.Fatal("unrelated keys must not match the rebound action")
	}
}

// TestKeyReleasesIgnored proves release events carry no action: a release
// must never re-fire the press it follows, even inside a modal.
func TestKeyReleasesIgnored(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)
	m = openViaKey(m)
	before := m.ui.settings.cursor
	next, cmd := m.Update(tea.KeyReleaseMsg{Code: tea.KeyDown})
	if cmd != nil {
		t.Fatal("a key release must not produce a command")
	}
	if got := next.(model).ui.settings.cursor; got != before {
		t.Fatalf("a key release must not move the cursor: %d -> %d", before, got)
	}
	if _, cmd := m.Update(tea.KeyReleaseMsg{Code: 'q', Text: "q"}); cmd != nil {
		t.Fatal("releasing q must not quit")
	}
}

// TestKeyMatchesExtendedUVNames pins dispatch for the ultraviolet
// vocabulary beyond the local aliases: function, keypad and media keys
// match by code, with modifiers, through the same matcher.
func TestKeyMatchesExtendedUVNames(t *testing.T) {
	cases := []struct {
		name  string
		msg   tea.KeyPressMsg
		spec  string
		match bool
	}{
		{"f5", pressKey(tea.KeyF5, "", 0), "f5", true},
		{"f24", pressKey(tea.KeyF24, "", 0), "f24", true},
		{"ctrl f5", pressKey(tea.KeyF5, "", tea.ModCtrl), "ctrl+f5", true},
		{"mute", pressKey(tea.KeyMute, "", 0), "mute", true},
		{"kpenter", pressKey(tea.KeyKpEnter, "", 0), "kpenter", true},
		{"begin", pressKey(tea.KeyBegin, "", 0), "begin", true},
		{"f5 is not f6", pressKey(tea.KeyF6, "", 0), "f5", false},
		{"unknown f99 never matches", pressKey('j', "j", 0), "f99", false},
	}
	for _, tc := range cases {
		if got := keyMatches(tc.msg, bindKeys(t, tc.spec)); got != tc.match {
			t.Errorf("%s: keyMatches(%q, %q) = %v, want %v", tc.name, tc.msg.String(), tc.spec, got, tc.match)
		}
	}
}

// TestCanonicalKeySpecRendersExtendedNames pins the canonicalizer on the
// extended vocabulary: uv-rendered bases fold aliases exactly like the
// old table, and names outside every vocabulary pass through unchanged.
func TestCanonicalKeySpecRendersExtendedNames(t *testing.T) {
	cases := map[string]string{
		"shift+ctrl+a": "ctrl+shift+a",
		"return":       "enter",
		"escape":       "esc",
		" ":            "space",
		"ctrl++":       "ctrl++",
		"f5":           "f5",
		"shift+f5":     "shift+f5",
		"mute":         "mute",
		"mode_broken":  "mode_broken",
	}
	for spec, want := range cases {
		if got := canonicalKeySpec(spec); got != want {
			t.Errorf("canonicalKeySpec(%q) = %q, want %q", spec, got, want)
		}
	}
}
