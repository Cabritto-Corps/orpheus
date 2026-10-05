package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/spotify"
)

func TestSearchShortcutsIgnoreTerminalLockStateModifiers(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"ctrl+f with CapsLock", tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModCapsLock}},
		{"ctrl+l with CapsLock", tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl | tea.ModCapsLock}},
		{"f3 with CapsLock", tea.KeyPressMsg{Code: tea.KeyF3, Mod: tea.ModCapsLock}},
		{"ctrl+l with all lock states", tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl | tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewLoaderModel()
			m.ui.activeTab = tabPlaylists
			next, _ := m.handleKey(tc.msg)
			got := next.(model)
			if got.ui.activeTab != tabSearch {
				t.Fatalf("shortcut was rejected: modifiers=%d", tc.msg.Mod)
			}
		})
	}
}

func TestRecentsFilterMatchesTitleAndArtist(t *testing.T) {
	for _, query := range []string{"zto", "festa", "guest"} {
		t.Run(query, func(t *testing.T) {
			m := NewLoaderModel()
			m.ui.activeTab = tabRecents
			m.browse.recentsList.SetItems([]list.Item{
				trackItem{item: spotify.QueueItem{ID: "wanted", Name: "Até A Festa Acabar", Artist: "zTokyo, Guest Singer"}},
				trackItem{item: spotify.QueueItem{ID: "other", Name: "Another Song", Artist: "Other Artist"}},
			})
			m.browse.recentsList.SetFilterText(query)
			visible := m.browse.recentsList.VisibleItems()
			if len(visible) != 1 || visible[0].(trackItem).item.ID != "wanted" {
				t.Fatalf("query %q did not match title/artist: %#v", query, visible)
			}
			// Render too: matches on artist indices must not break title highlighting.
			if view := m.browse.recentsList.View(); !strings.Contains(view, "Guest Singer") {
				t.Fatal("filtered song did not render")
			}
		})
	}
}
