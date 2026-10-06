package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func artistChoiceCatalog() fakeCatalog {
	return fakeCatalog{
		artistAlbums: func(id string, offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{
				Items:      []spotify.PlaylistSummary{{ID: "al1", URI: "spotify:album:al1", Kind: spotify.ContextKindAlbum}},
				Offset:     offset,
				Limit:      limit,
				NextOffset: 1,
			}, nil
		},
		albumItems: func(id string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
			return &spotify.PlaylistItemsPage{
				ItemIDs:    []string{"t1", "t2", "t3"},
				Offset:     offset,
				Limit:      limit,
				NextOffset: 3,
			}, nil
		},
	}
}

func openArtistChoiceModel() model {
	m := NewLoaderModel()
	m.ui.activeTab = tabSearch
	m.ui.width, m.ui.height = 120, 40
	m.tuiCmdCh = make(chan librespot.TUICommand, 2)
	next, _ := m.selectSearchResult(spotify.SearchResultItem{
		ID: "artist-id", URI: "spotify:artist:artist-id", Kind: "artist", Name: "Artist",
	})
	return next.(model)
}

func TestArtistChoiceStationSendsStationCommand(t *testing.T) {
	m := openArtistChoiceModel()
	if !m.ui.artistChoiceOpen {
		t.Fatal("choice modal did not open")
	}
	m.ui.artistChoiceCursor = 1
	next, _ := m.handleArtistChoiceKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.ui.artistChoiceOpen {
		t.Fatal("station choice must close the modal")
	}
	if m.ui.activeTab != tabPlayer {
		t.Fatalf("station choice did not open Player: %q", m.ui.activeTab)
	}
	select {
	case command := <-m.tuiCmdCh:
		if command.Kind != librespot.TUICommandPlayStation || command.URI != "spotify:artist:artist-id" {
			t.Fatalf("wrong station command: %#v", command)
		}
	default:
		t.Fatal("station choice sent no command")
	}
}

func TestArtistChoiceTracksPlayShuffledQueue(t *testing.T) {
	m := openArtistChoiceModel()
	m.catalogSource.set(artistChoiceCatalog())
	next, cmd := m.handleArtistChoiceKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.ui.artistChoiceLoading {
		t.Fatal("tracks choice must enter loading state")
	}
	if cmd == nil {
		t.Fatal("tracks choice must issue a fetch command")
	}
	msg, ok := cmd().(artistTracksMsg)
	if !ok {
		t.Fatalf("expected artistTracksMsg, got %T", cmd())
	}
	next, _ = m.handleArtistTracksMsg(msg)
	m = next.(model)
	if m.ui.artistChoiceOpen {
		t.Fatal("successful fetch must close the modal")
	}
	if m.ui.activeTab != tabPlayer {
		t.Fatalf("tracks choice did not open Player: %q", m.ui.activeTab)
	}
	select {
	case command := <-m.tuiCmdCh:
		if command.Kind != librespot.TUICommandPlayTracks {
			t.Fatalf("wrong command kind: %#v", command)
		}
		if command.URI != "spotify:artist:artist-id" {
			t.Fatalf("wrong context URI: %#v", command)
		}
		if len(command.URIs) != 3 {
			t.Fatalf("expected 3 queued tracks, got %#v", command)
		}
		seen := map[string]bool{}
		for _, u := range command.URIs {
			seen[u] = true
		}
		for _, want := range []string{"spotify:track:t1", "spotify:track:t2", "spotify:track:t3"} {
			if !seen[want] {
				t.Fatalf("queued set lost %s: %v", want, command.URIs)
			}
		}
	default:
		t.Fatal("tracks choice sent no command")
	}
}

func TestArtistChoiceFetchErrorStaysOpen(t *testing.T) {
	m := openArtistChoiceModel()
	catalog := artistChoiceCatalog()
	catalog.artistAlbums = func(id string, offset, limit int) (*spotify.PlaylistPage, error) {
		return nil, errors.New("boom")
	}
	m.catalogSource.set(catalog)
	next, cmd := m.handleArtistChoiceKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	msg := cmd().(artistTracksMsg)
	next, _ = m.handleArtistTracksMsg(msg)
	m = next.(model)
	if !m.ui.artistChoiceOpen {
		t.Fatal("failed fetch must keep the modal open")
	}
	if m.ui.artistChoiceErr == nil {
		t.Fatal("failed fetch must surface the error in the modal")
	}
	select {
	case command := <-m.tuiCmdCh:
		t.Fatalf("failed fetch must not play, got %#v", command)
	default:
	}
}

func TestArtistChoiceDropsStaleResults(t *testing.T) {
	m := openArtistChoiceModel()
	m.catalogSource.set(artistChoiceCatalog())
	next, cmd := m.handleArtistChoiceKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	msg := cmd().(artistTracksMsg)
	next, _ = m.handleArtistChoiceKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if m.ui.artistChoiceOpen {
		t.Fatal("esc must close the modal")
	}
	next, _ = m.handleArtistTracksMsg(msg)
	m = next.(model)
	select {
	case command := <-m.tuiCmdCh:
		t.Fatalf("stale fetch must not play, got %#v", command)
	default:
	}
	if m.ui.activeTab != tabSearch {
		t.Fatalf("stale fetch must stay on Search, got %q", m.ui.activeTab)
	}
}

func TestArtistChoiceCursorMovesAndTrapsKeys(t *testing.T) {
	m := openArtistChoiceModel()
	next, _ := m.handleArtistChoiceKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if next.(model).ui.artistChoiceCursor != 1 {
		t.Fatal("down must move to the station row")
	}
	next, _ = m.handleArtistChoiceKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if next.(model).ui.artistChoiceCursor != 0 {
		t.Fatal("up must move back to the tracks row")
	}
	after, cmd := sendTop(next.(model), tea.KeyPressMsg{Code: 'q', Text: "q"})
	if isQuitCmd(cmd) || !after.ui.artistChoiceOpen {
		t.Fatal("q must be inert while the choice modal is open")
	}
}

func TestArtistChoiceViewListsBothOptions(t *testing.T) {
	m := openArtistChoiceModel()
	view := m.artistChoiceView()
	if !strings.Contains(view, "Play artist tracks") || !strings.Contains(view, "Play artist station") {
		t.Fatalf("choice modal must list both options: %q", view)
	}
	m.ui.artistChoiceLoading = true
	if view := m.artistChoiceView(); !strings.Contains(view, "Loading artist tracks") {
		t.Fatalf("loading modal must show progress: %q", view)
	}
}
