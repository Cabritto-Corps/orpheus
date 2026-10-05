package tui

import (
	"context"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func TestRecentsLibraryAggregatesAndDeduplicatesSources(t *testing.T) {
	catalog := fakeCatalog{
		recent: func(limit int) ([]spotify.QueueItem, error) {
			if limit != 50 {
				t.Fatalf("recent track limit = %d, want 50", limit)
			}
			return []spotify.QueueItem{
				{ID: "session-dup", Name: "API duplicate"},
				{ID: "recent", Name: "Recent song", Artist: "Recent artist"},
			}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{}, nil, nil, nil)
	m.browse.sessionRecentTracks = []spotify.QueueItem{{ID: "session-dup", Name: "Session track", Artist: "Session artist"}}
	msg := m.loadRecentsLibraryCmd()().(recentsLibraryMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	next, _ := m.handleRecentsLibraryMsg(msg)
	m = next.(model)
	if len(m.browse.recentsTracks) != 2 {
		t.Fatalf("got %d recent tracks, want 2: %#v", len(m.browse.recentsTracks), m.browse.recentsTracks)
	}
	if got := m.browse.recentsTracks[0].ID; got != "session-dup" {
		t.Fatalf("session recents must precede API recents: %q", got)
	}
	if got := m.browse.recentsTracks[0].Artist; got != "Session artist" {
		t.Fatalf("first-seen metadata must win: %q", got)
	}
	if got := m.browse.recentsTracks[1].ID; got != "recent" {
		t.Fatalf("API recent order changed: %q", got)
	}
}

func TestPlaySingleTrackFreezesOutgoingDuration(t *testing.T) {
	m := NewLoaderModel()
	m.tuiCmdCh = make(chan librespot.TUICommand, 2)
	m.transport.status = &spotify.PlaybackStatus{
		TrackID: "spotify:track:aaa", TrackName: "A", ArtistName: "Artist",
		DurationMS: 180000, AlbumImageURL: "cover-a",
	}
	next, _ := m.playSingleTrack("spotify:track:bbb", "")
	got := next.(model)
	if len(got.browse.sessionRecentTracks) != 1 {
		t.Fatalf("outgoing track was not frozen: %#v", got.browse.sessionRecentTracks)
	}
	frozen := got.browse.sessionRecentTracks[0]
	if frozen.Name != "A" || frozen.DurationMS != 180000 {
		t.Fatalf("frozen track lost its duration: %#v", frozen)
	}
	mutilated := got.transport.status
	current := &spotify.PlaybackStatus{TrackID: "spotify:track:bbb", TrackName: "B"}
	got.upsertCurrentPlaybackRecent(mutilated, current)
	again := got
	if len(again.browse.sessionRecentTracks) != 1 {
		t.Fatalf("re-freeze duplicated the session entry: %#v", again.browse.sessionRecentTracks)
	}
	if d := again.browse.sessionRecentTracks[0].DurationMS; d != 180000 {
		t.Fatalf("re-freeze clobbered the duration: %#v", again.browse.sessionRecentTracks[0])
	}
}

func TestCurrentPlaybackTrackAppearsInRecents(t *testing.T) {
	m := NewLoaderModel()
	m.browse.apiRecentTracks = []spotify.QueueItem{{ID: "recent", Name: "Recent"}}
	current := &spotify.PlaybackStatus{
		TrackID: "spotify:track:now-playing", TrackName: "Now Playing", ArtistName: "Current Artist",
		DurationMS: 180000, AlbumImageURL: "current-cover",
	}
	m.transport.status = current
	m.upsertCurrentPlaybackRecent(nil, current)
	if len(m.browse.recentsTracks) != 2 || m.browse.recentsTracks[0].ID != "spotify:track:now-playing" {
		t.Fatalf("current track was not added at the top: %#v", m.browse.recentsTracks)
	}
	if len(m.browse.recentsList.Items()) != 2 {
		t.Fatalf("Recents list did not receive current track: %#v", m.browse.recentsList.Items())
	}
}

func TestCurrentAndSessionRecentTracksPrecedeSpotifySources(t *testing.T) {
	m := NewLoaderModel()
	m.browse.apiRecentTracks = []spotify.QueueItem{{ID: "spotify-recent", Name: "Spotify recent"}}
	previous := &spotify.PlaybackStatus{TrackID: "just-finished", TrackName: "Just finished"}
	current := &spotify.PlaybackStatus{TrackID: "now-playing", TrackName: "Now playing"}
	m.transport.status = current
	m.upsertCurrentPlaybackRecent(previous, current)
	want := []string{"now-playing", "just-finished", "spotify-recent"}
	if len(m.browse.recentsTracks) != len(want) {
		t.Fatalf("got wrong track count: %#v", m.browse.recentsTracks)
	}
	for i, id := range want {
		if m.browse.recentsTracks[i].ID != id {
			t.Fatalf("track %d = %q, want %q: %#v", i, m.browse.recentsTracks[i].ID, id, m.browse.recentsTracks)
		}
	}
}

func TestRecentsDefaultsToCurrentTrackInsteadOfStaleSelection(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width, m.ui.height = 120, 40
	m.ui.activeTab = tabRecents
	m.browse.librarySettled = true
	m.browse.apiRecentTracks = make([]spotify.QueueItem, 12)
	for i := range m.browse.apiRecentTracks {
		m.browse.apiRecentTracks[i] = spotify.QueueItem{ID: fmt.Sprintf("recent-%02d", i), Name: "Recent"}
	}
	m.refreshRecentsList()
	// Simulate a stale list cursor surviving a partial refresh before playback
	// status arrives; this must not become the default selection.
	m.browse.recentsList.Select(10)

	next, _ := m.handlePlaybackStateMsg(playbackStateMsg{status: &spotify.PlaybackStatus{
		TrackID: "current-track", TrackName: "Current", ArtistName: "Artist", DurationMS: 180000, Playing: true,
	}})
	got := next.(model)
	selected, ok := got.browse.recentsList.SelectedItem().(trackItem)
	if !ok || recentIdentity(selected.item.ID) != "current-track" {
		t.Fatalf("Recents default selection = %#v, want current track", got.browse.recentsList.SelectedItem())
	}
	if got.browse.recentsList.Paginator.Page != 0 {
		t.Fatalf("Recents opened on page %d, want first page", got.browse.recentsList.Paginator.Page)
	}
}

func TestEnteringRecentsResetsUntouchedSelectionToCurrentTrack(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabAlbums
	m.transport.status = &spotify.PlaybackStatus{
		TrackID: "current-track", TrackName: "Current", ArtistName: "Artist", DurationMS: 180000, Playing: true,
	}
	m.browse.apiRecentTracks = make([]spotify.QueueItem, 40)
	for i := range m.browse.apiRecentTracks {
		m.browse.apiRecentTracks[i] = spotify.QueueItem{ID: fmt.Sprintf("recent-%02d", i), Name: "Recent"}
	}
	m.refreshRecentsList()
	m.browse.recentsList.Select(10)
	if m.browse.recentsList.Paginator.Page == 0 {
		t.Fatal("test setup did not create a stale Recents page")
	}

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	got := next.(model)
	if got.ui.activeTab != tabRecents {
		t.Fatalf("Tab from Albums landed on %q, want Recents", got.ui.activeTab)
	}
	selected, ok := got.browse.recentsList.SelectedItem().(trackItem)
	if !ok || recentIdentity(selected.item.ID) != "current-track" {
		t.Fatalf("Recents entry selection = %#v, want current track", got.browse.recentsList.SelectedItem())
	}
	if got.browse.recentsList.Paginator.Page != 0 {
		t.Fatalf("Recents opened on page %d, want first page", got.browse.recentsList.Paginator.Page)
	}
}

func TestRecentsLibraryCapsAtOneHundredTracks(t *testing.T) {
	m := NewLoaderModel()
	recent := make([]spotify.QueueItem, 0, maxRecentsTracks+20)
	for i := range maxRecentsTracks + 20 {
		recent = append(recent, spotify.QueueItem{ID: fmt.Sprintf("track-%03d", i), Name: "Track"})
	}
	m.browse.apiRecentTracks = recent
	m.refreshRecentsList()
	if len(m.browse.recentsTracks) != maxRecentsTracks {
		t.Fatalf("recents capped at %d, got %d", maxRecentsTracks, len(m.browse.recentsTracks))
	}
}
