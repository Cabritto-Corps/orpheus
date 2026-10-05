package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func TestSearchResultsIgnoreStaleRequestsAndAppendPages(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	m.browse.search.query = "radiohead"
	m.browse.search.requestID = 4
	m.browse.search.loading = true
	stale, _ := m.handleSearchResultsMsg(searchResultsMsg{
		token: 3, query: "radiohead", page: &spotify.SearchPage{Items: []spotify.SearchResultItem{{ID: "stale"}}},
	})
	if len(stale.(model).browse.search.list.Items()) != 0 {
		t.Fatal("stale search response was applied")
	}
	first, _ := m.handleSearchResultsMsg(searchResultsMsg{
		token: 4, query: "radiohead", offset: 0,
		page: &spotify.SearchPage{Offset: 0, Limit: 10, NextOffset: 10, HasMore: true, Items: []spotify.SearchResultItem{{ID: "one", Kind: "track"}}},
	})
	m = first.(model)
	if len(m.browse.search.list.Items()) != 1 || !m.browse.search.hasMore || m.browse.search.offset != 10 {
		t.Fatalf("first page was not applied: %#v", m.browse.search)
	}
	m.browse.search.list.Select(0)
	second, _ := m.handleSearchResultsMsg(searchResultsMsg{
		token: 4, query: "radiohead", offset: 10,
		page: &spotify.SearchPage{Offset: 10, Limit: 10, NextOffset: 20, Items: []spotify.SearchResultItem{{ID: "two", Kind: "album"}}},
	})
	m = second.(model)
	items := m.browse.search.list.Items()
	if len(items) != 2 || m.browse.search.hasMore || m.browse.search.offset != 20 {
		t.Fatalf("next page was not appended: %#v", m.browse.search)
	}
	if got := items[0].(searchResultItem).result.ID; got != "one" {
		t.Fatalf("previous page item was lost: %q", got)
	}
}

func TestSearchTrackSelectionSendsSingleTrackCommand(t *testing.T) {
	commands := make(chan librespot.TUICommand, 1)
	m := NewLoaderModel()
	m.tuiCmdCh = commands
	next, _ := m.selectSearchResult(spotify.SearchResultItem{
		ID: "track-id", URI: "spotify:track:track-id", Kind: "track", ImageURL: "cover",
	})
	if next.(model).ui.activeTab != tabSearch {
		t.Fatal("track selection should keep Search open for browsing")
	}
	cmd := <-commands
	if cmd.Kind != librespot.TUICommandPlayTrack || cmd.URI != "spotify:track:track-id" {
		t.Fatalf("unexpected playback command: %#v", cmd)
	}
}

func TestSearchAlbumSelectionStartsAlbumContextAndOpensPlayer(t *testing.T) {
	commands := make(chan librespot.TUICommand, 1)
	m := NewLoaderModel()
	m.tuiCmdCh = commands
	next, _ := m.selectSearchResult(spotify.SearchResultItem{
		ID: "album-id", URI: "spotify:album:album-id", Kind: "album", TrackCount: 8,
	})
	if next.(model).ui.activeTab != tabPlayer {
		t.Fatal("album selection did not switch to Player")
	}
	cmd := <-commands
	if cmd.Kind != librespot.TUICommandPlayContext || cmd.URI != "spotify:album:album-id" {
		t.Fatalf("unexpected album playback command: %#v", cmd)
	}
}

func TestSearchPaginationLoadsNearEnd(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	m.browse.search.query = "artist"
	m.browse.search.requestID = 1
	m.browse.search.offset = 10
	m.browse.search.hasMore = true
	items := make([]list.Item, 10)
	for i := range items {
		items[i] = searchResultItem{result: spotify.SearchResultItem{ID: "result"}}
	}
	m.browse.search.list.SetItems(items)
	m.browse.search.list.Select(6)
	next, cmd := m.loadMoreSearchIfNeeded()
	if cmd == nil || !next.browse.search.loading {
		t.Fatal("expected another page to load near the end of the results")
	}
}

func TestSearchPanelStatusAndSingleSpinner(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width, m.ui.height = 120, 40
	m.ui.activeTab = tabSearch

	panel := m.searchBrowserPanel(60, 20)
	if strings.Contains(panel, "Type to search") {
		t.Fatalf("empty search must not show hint text: %q", panel)
	}
	if !strings.Contains(panel, "type at least 2 characters") {
		t.Fatalf("empty search must keep the short-query indicator: %q", panel)
	}

	m.browse.search.query = "ab"
	m.browse.search.loading = true
	panel = m.searchBrowserPanel(60, 20)
	if !strings.Contains(panel, "type at least 2 characters") {
		t.Fatalf("loading must not replace the indicator with a spinner: %q", panel)
	}
	if got := strings.Count(panel, m.ui.spinner.View()); got != 1 {
		t.Fatalf("loading search must show exactly one spinner, got %d: %q", got, panel)
	}

	m.browse.search.loading = false
	m.browse.search.list.SetItems([]list.Item{
		searchResultItem{result: spotify.SearchResultItem{ID: "one", Kind: "track", Name: "One"}},
		searchResultItem{result: spotify.SearchResultItem{ID: "two", Kind: "track", Name: "Two"}},
	})
	panel = m.searchBrowserPanel(60, 20)
	if !strings.Contains(panel, "2 results") {
		t.Fatalf("loaded search must show the result count: %q", panel)
	}
}

func TestSearchInputQueryDebouncesAndClearsShortQueries(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	m.browse.search.list.SetItems([]list.Item{searchResultItem{result: spotify.SearchResultItem{ID: "old"}}})
	m, cmd := m.setSearchQuery("a")
	_ = cmd // Short-query updates may still request an image-overlay repaint.
	if m.browse.search.loading || len(m.browse.search.list.Items()) != 0 {
		t.Fatalf("short query should clear results without issuing a request (loading=%v items=%d)", m.browse.search.loading, len(m.browse.search.list.Items()))
	}
	m, cmd = m.setSearchQuery("ab")
	if cmd == nil || !m.browse.search.loading || m.browse.search.requestID != 2 {
		t.Fatal("valid query should be debounced and marked loading")
	}
}

func TestSearchInputKeyboardFlow(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	next, _ := m.handleSearchKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = next.(model)
	if !m.browse.search.input.Focused() {
		t.Fatal("slash did not focus the remote search input")
	}
	for _, r := range []rune{'a', 'b'} {
		next, _ = m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = next.(model)
	}
	if m.browse.search.query != "ab" || !m.browse.search.loading || m.browse.search.requestID != 2 {
		t.Fatalf("typed query was not scheduled: %#v", m.browse.search)
	}
}

func TestSearchArrowKeysMoveResultsWhileInputIsFocused(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	m.browse.search.list.SetItems([]list.Item{
		searchResultItem{result: spotify.SearchResultItem{ID: "one", Kind: "track"}},
		searchResultItem{result: spotify.SearchResultItem{ID: "two", Kind: "track"}},
	})
	m.browse.search.input.Focus()
	query := m.browse.search.input.Value()
	next, _ := m.handleSearchKey(tea.KeyPressMsg{Code: tea.KeyDown})
	got := next.(model)
	if got.browse.search.list.Index() != 1 {
		t.Fatalf("down arrow did not move selection: index=%d", got.browse.search.list.Index())
	}
	if got.browse.search.input.Value() != query {
		t.Fatal("navigation key changed the search query")
	}
}

func TestSearchEnterThenDownKeepsBrowsingResults(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	m.tuiCmdCh = make(chan librespot.TUICommand, 2)
	m.browse.search.input.Focus()
	m.browse.search.list.SetItems([]list.Item{
		searchResultItem{result: spotify.SearchResultItem{ID: "one", URI: "spotify:track:one", Kind: "track"}},
		searchResultItem{result: spotify.SearchResultItem{ID: "two", URI: "spotify:track:two", Kind: "track"}},
	})
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.ui.activeTab != tabSearch {
		t.Fatal("Enter left the search results")
	}
	if command := <-m.tuiCmdCh; command.URI != "spotify:track:one" {
		t.Fatalf("wrong first song: %#v", command)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	if m.browse.search.list.Index() != 1 {
		t.Fatal("Down after playback did not select the next search result")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command := <-next.(model).tuiCmdCh; command.URI != "spotify:track:two" {
		t.Fatalf("wrong second song: %#v", command)
	}
}

func TestSearchDownCrossesListPages(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	m.browse.search.input.Focus()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(model)
	items := make([]list.Item, 30)
	for i := range items {
		items[i] = searchResultItem{result: spotify.SearchResultItem{Name: "Song", Kind: "track"}}
	}
	m.browse.search.list.SetItems(items)
	perPage := m.browse.search.list.Paginator.PerPage
	for range perPage {
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = next.(model)
	}
	if m.browse.search.list.Index() != perPage || m.browse.search.list.Paginator.Page != 1 {
		t.Fatalf("Down did not scroll to the next page: index=%d page=%d", m.browse.search.list.Index(), m.browse.search.list.Paginator.Page)
	}
}

func TestSearchShortcutNavigatesToSearchTab(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{name: "ctrl+f", msg: tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}},
		{name: "ctrl+l", msg: tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}},
		{name: "f3", msg: tea.KeyPressMsg{Code: tea.KeyF3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewLoaderModel()
			m.ui.activeTab = tabPlaylists
			next, _ := m.handleKey(tc.msg)
			got := next.(model)
			if got.ui.activeTab != tabSearch || !got.browse.search.input.Focused() {
				t.Fatalf("shortcut did not open and focus Search: tab=%q focused=%v", got.ui.activeTab, got.browse.search.input.Focused())
			}
		})
	}
	t.Run("configured binding", func(t *testing.T) {
		m := NewLoaderModel()
		m.ui.activeTab = tabPlaylists
		m.ui.keys = newKeysFromConfig(map[string][]string{"search": {"alt+x"}})
		next, _ := m.handleKey(tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt})
		got := next.(model)
		if got.ui.activeTab != tabSearch || !got.browse.search.input.Focused() {
			t.Fatalf("configured Search key did not open Search: tab=%q", got.ui.activeTab)
		}
	})
	t.Run("works during local filter", func(t *testing.T) {
		m := NewLoaderModel()
		m.ui.activeTab = tabPlaylists
		m.browse.playlistList, _ = m.browse.playlistList.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
		if m.browse.playlistList.FilterState() != list.Filtering {
			t.Fatal("test setup did not enter local filter mode")
		}
		next, _ := m.handleKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
		if got := next.(model); got.ui.activeTab != tabSearch {
			t.Fatalf("Search shortcut was blocked by the local filter: tab=%q", got.ui.activeTab)
		}
	})
}
