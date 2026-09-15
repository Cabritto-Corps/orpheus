package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func newPopupTestModel(tuiCmdCh chan librespot.TUICommand, contextTracksCh chan librespot.ContextTracksResult) model {
	return newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, tuiCmdCh, contextTracksCh, nil)
}

func TestTrackPopupIgnoresStaleTokenResults(t *testing.T) {
	m := newPopupTestModel(nil, nil)
	selA := playlistItem{summary: spotify.PlaylistSummary{ID: "a", Kind: spotify.ContextKindPlaylist, URI: "spotify:playlist:a", Name: "A"}}
	selB := playlistItem{summary: spotify.PlaylistSummary{ID: "b", Kind: spotify.ContextKindPlaylist, URI: "spotify:playlist:b", Name: "B"}}

	next, _ := m.openTrackPopup(selA)
	m = next.(model)
	tokenA := m.ui.trackPopupReqToken

	next, _ = m.handleTrackPopupKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = next.(model)
	next, _ = m.openTrackPopup(selB)
	m = next.(model)
	if m.ui.trackPopupReqToken == tokenA {
		t.Fatal("expected a fresh request token per popup open")
	}

	late := trackPopupItemsMsg{token: tokenA, items: []spotify.QueueItem{{ID: "stale"}}}
	got, _ := m.handleTrackPopupItemsMsg(late)
	if g := got.(model); len(g.ui.trackPopupItems) != 0 {
		t.Fatalf("expected stale result to be dropped, got %+v", g.ui.trackPopupItems)
	}

	current := trackPopupItemsMsg{token: m.ui.trackPopupReqToken, items: []spotify.QueueItem{{ID: "fresh"}}}
	got, _ = m.handleTrackPopupItemsMsg(current)
	if g := got.(model); len(g.ui.trackPopupItems) != 1 || g.ui.trackPopupItems[0].ID != "fresh" {
		t.Fatalf("expected current result to be applied, got %+v", g.ui.trackPopupItems)
	}
}

func TestTrackPopupClosesWithErrorOnLoadTimeout(t *testing.T) {
	cmdCh := make(chan librespot.TUICommand, 1)
	resultCh := make(chan librespot.ContextTracksResult, 1)
	m := newPopupTestModel(cmdCh, resultCh)
	sel := playlistItem{summary: spotify.PlaylistSummary{ID: "a", Kind: spotify.ContextKindPlaylist, URI: "spotify:playlist:a", Name: "A"}}

	next, _ := m.openTrackPopup(sel)
	m = next.(model)
	for range trackPopupLoadTimeoutTicks + 1 {
		next, _ = m.handleTickMsg()
		m = next.(model)
	}
	if m.ui.trackPopupOpen {
		t.Fatal("expected popup to close after load timeout")
	}
	if m.transport.playbackErr == nil {
		t.Fatal("expected a playback error after popup load timeout")
	}
}
