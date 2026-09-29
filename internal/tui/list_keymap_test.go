package tui

import (
	"context"
	"slices"
	"testing"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/loader"
)

func testListModel() model {
	return newModel(context.Background(), nil, config.Config{DeviceName: "orpheus-test"}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), nil)))
}

func pressRune(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// batchContainsQuit unwraps tea.BatchMsg layers so tests can find a
// tea.Quit hidden inside the batched commands browse handlers return.
func batchContainsQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	if msg, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range msg {
			if batchContainsQuit(c) {
				return true
			}
		}
		return false
	}
	return isQuitCmd(cmd)
}

// Bubbles v2 binds its list quit to `v` (v1 bound q/esc). The app owns
// quitting, so `v` must never escape a browse tab as tea.Quit.
func TestBrowseListVKeyDoesNotQuit(t *testing.T) {
	for _, tab := range []tab{tabPlaylists, tabAlbums} {
		m := testListModel()
		m.ui.activeTab = tab
		_, cmd := sendTop(m, pressRune('v'))
		if batchContainsQuit(cmd) {
			t.Errorf("tab %v: pressing v returned tea.Quit", tab)
		}
	}
}

// The popup forwards unmatched keys to its bubbles list, which must not
// punch tea.Quit through the modal focus trap either.
func TestTrackPopupVKeyDoesNotQuit(t *testing.T) {
	m := testListModel()
	m.ui.trackPopupOpen = true
	m.ui.trackPopupList = newTrackPopupList(m.styles, m.nowPlaying, 100, 40)
	_, cmd := sendTop(m, pressRune('v'))
	if batchContainsQuit(cmd) {
		t.Error("track popup: pressing v returned tea.Quit")
	}
}

// Guard the other direction: disabling the list's quit must not take the
// app's own quit binding with it.
func TestQuitKeyStillQuitsOnBrowse(t *testing.T) {
	m := testListModel()
	m.ui.activeTab = tabPlaylists
	_, cmd := sendTop(m, pressRune('q'))
	if !batchContainsQuit(cmd) {
		t.Error("pressing q on a browse tab no longer quits")
	}
}

// Rebinding away must restore the page key: the reconciliation rebuilds
// from the library defaults every keypress instead of stripping in place,
// so giving repeat another key hands `l` back to paging.
func TestListKeyMapsRestorePageKeysOnRebind(t *testing.T) {
	m := testListModel()
	m.ui.keys.Loop = key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "repeat"))
	next, _ := sendTop(m, teaDown())
	if got := next.browse.playlistList.KeyMap.NextPage.Keys(); !slices.Equal(got, []string{"l", "pgdown", "f"}) {
		t.Errorf("NextPage after rebind = %q, want [l pgdown f]", got)
	}
}

// queue-remove's `d` belong to the app, and the list quit binding must be
// disabled outright. Filter keeps following the live search binding.
func TestListKeyMapsYieldToRegistry(t *testing.T) {
	m := testListModel()
	next, _ := sendTop(m, teaDown()) // any key runs the reconciliation
	lists := map[string]list.Model{
		"playlists": next.browse.playlistList,
		"albums":    next.browse.albumList,
		"popup":     next.ui.trackPopupList,
	}
	for name, l := range lists {
		if l.KeyMap.Quit.Enabled() {
			t.Errorf("%s: list quit binding still enabled", name)
		}
		if got := l.KeyMap.NextPage.Keys(); !slices.Equal(got, []string{"pgdown", "f"}) {
			t.Errorf("%s: NextPage = %q, want [pgdown f] (registry l/d stripped)", name, got)
		}
		if got := l.KeyMap.PrevPage.Keys(); !slices.Equal(got, []string{"h", "pgup", "b", "u"}) {
			t.Errorf("%s: PrevPage = %q, want [h pgup b u] (registry left stripped)", name, got)
		}
		if got := l.KeyMap.Filter.Keys(); !slices.Equal(got, []string{"/"}) {
			t.Errorf("%s: Filter = %q, want [/]", name, got)
		}
	}
}
