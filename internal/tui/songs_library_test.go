package tui

import (
	"context"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/spotify"
)

func TestSongsLibraryAggregatesAndDeduplicatesSources(t *testing.T) {
	catalog := fakeCatalog{
		playlistItems: func(id string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
			if id != "mix" || offset != 0 {
				t.Fatalf("unexpected playlist page request: id=%q offset=%d limit=%d", id, offset, limit)
			}
			return &spotify.PlaylistItemsPage{ItemInfos: []spotify.QueueItem{
				{ID: "liked", Name: "Liked duplicate"},
				{ID: "from-playlist", Name: "Playlist song", Artist: "Playlist artist"},
			}}, nil
		},
		albumItems: func(id string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
			if id != "album" || offset != 0 {
				t.Fatalf("unexpected album page request: id=%q offset=%d limit=%d", id, offset, limit)
			}
			return &spotify.PlaylistItemsPage{ItemInfos: []spotify.QueueItem{
				{ID: "from-album", Name: "Album song", Artist: "Album artist"},
			}}, nil
		},
		recent: func(limit int) ([]spotify.QueueItem, error) {
			if limit != 50 {
				t.Fatalf("recent track limit = %d, want 50", limit)
			}
			return []spotify.QueueItem{
				{ID: "from-playlist", Name: "Recent duplicate"},
				{ID: "recent", Name: "Recent song", Artist: "Recent artist"},
			}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{}, nil, nil, nil)
	m.browse.songsSavedTracks = []spotify.QueueItem{{ID: "liked", Name: "Liked song", Artist: "Liked artist"}}
	contexts := []spotify.PlaylistSummary{
		{ID: "mix", Kind: spotify.ContextKindPlaylist},
		{ID: "album", Kind: spotify.ContextKindAlbum, ImageURL: "album-cover"},
	}
	msg := m.loadSongsLibraryCmd(contexts)().(songsLibraryMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	next, _ := m.handleSongsLibraryMsg(msg)
	m = next.(model)
	if len(m.browse.songsTracks) != 4 {
		t.Fatalf("got %d unique songs, want 4: %#v", len(m.browse.songsTracks), m.browse.songsTracks)
	}
	if got := m.browse.songsTracks[0].ID; got != "from-playlist" {
		t.Fatalf("recent tracks must precede liked tracks: %q", got)
	}
	if got := m.browse.songsTracks[1].ID; got != "recent" {
		t.Fatalf("recent playback order changed: %q", got)
	}
	if got := m.browse.songsTracks[2].ID; got != "liked" {
		t.Fatalf("liked songs must follow recent tracks: %q", got)
	}
	if got := m.browse.songsTracks[3].ID; got != "from-album" {
		t.Fatalf("playlist/album tracks must follow liked songs: %q", got)
	}
	var albumCover, playlistArtist string
	for _, track := range m.browse.songsTracks {
		if track.ID == "from-album" {
			albumCover = track.ImageURL
		}
		if track.ID == "from-playlist" {
			playlistArtist = track.Artist
		}
	}
	if albumCover != "album-cover" || playlistArtist != "Playlist artist" {
		t.Fatalf("aggregated metadata lost: album cover=%q playlist artist=%q", albumCover, playlistArtist)
	}
}

func TestCurrentPlaybackTrackAppearsInSongs(t *testing.T) {
	m := NewLoaderModel()
	m.browse.songsSavedTracks = []spotify.QueueItem{{ID: "saved", Name: "Saved"}}
	current := &spotify.PlaybackStatus{
		TrackID: "spotify:track:now-playing", TrackName: "Now Playing", ArtistName: "Current Artist",
		DurationMS: 180000, AlbumImageURL: "current-cover",
	}
	m.transport.status = current
	m.upsertCurrentPlaybackSong(nil, current)
	if len(m.browse.songsTracks) != 2 || m.browse.songsTracks[0].ID != "spotify:track:now-playing" {
		t.Fatalf("current track was not added at the top: %#v", m.browse.songsTracks)
	}
	if len(m.browse.songsList.Items()) != 2 {
		t.Fatalf("Songs list did not receive current track: %#v", m.browse.songsList.Items())
	}
}

func TestCurrentAndSessionRecentTracksPrecedeSpotifySources(t *testing.T) {
	m := NewLoaderModel()
	m.browse.songsRecentTracks = []spotify.QueueItem{{ID: "spotify-recent", Name: "Spotify recent"}}
	m.browse.songsSavedTracks = []spotify.QueueItem{{ID: "liked", Name: "Liked"}}
	m.browse.songsCollectionTracks = []spotify.QueueItem{{ID: "playlist", Name: "Playlist"}}
	previous := &spotify.PlaybackStatus{TrackID: "just-finished", TrackName: "Just finished"}
	current := &spotify.PlaybackStatus{TrackID: "now-playing", TrackName: "Now playing"}
	m.transport.status = current
	m.upsertCurrentPlaybackSong(previous, current)
	want := []string{"now-playing", "just-finished", "spotify-recent", "liked", "playlist"}
	if len(m.browse.songsTracks) != len(want) {
		t.Fatalf("got wrong track count: %#v", m.browse.songsTracks)
	}
	for i, id := range want {
		if m.browse.songsTracks[i].ID != id {
			t.Fatalf("track %d = %q, want %q: %#v", i, m.browse.songsTracks[i].ID, id, m.browse.songsTracks)
		}
	}
}

func TestSongsDefaultsToCurrentTrackInsteadOfStaleLikedSelection(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width, m.ui.height = 120, 40
	m.ui.activeTab = tabSongs
	m.browse.librarySettled = true
	m.browse.songsSavedTracks = make([]spotify.QueueItem, 12)
	for i := range m.browse.songsSavedTracks {
		m.browse.songsSavedTracks[i] = spotify.QueueItem{ID: fmt.Sprintf("liked-%02d", i), Name: "Liked"}
	}
	m.refreshSongsList()
	// Simulate a stale list cursor surviving a partial refresh before playback
	// status arrives; this must not become the default selection.
	m.browse.songsList.Select(10)

	next, _ := m.handlePlaybackStateMsg(playbackStateMsg{status: &spotify.PlaybackStatus{
		TrackID: "current-track", TrackName: "Current", ArtistName: "Artist", DurationMS: 180000, Playing: true,
	}})
	got := next.(model)
	selected, ok := got.browse.songsList.SelectedItem().(trackItem)
	if !ok || songIdentity(selected.item.ID) != "current-track" {
		t.Fatalf("Songs default selection = %#v, want current track", got.browse.songsList.SelectedItem())
	}
	if got.browse.songsList.Paginator.Page != 0 {
		t.Fatalf("Songs opened on page %d, want first page", got.browse.songsList.Paginator.Page)
	}
}

func TestEnteringSongsResetsUntouchedSelectionToCurrentTrack(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{
		TrackID: "current-track", TrackName: "Current", ArtistName: "Artist", DurationMS: 180000, Playing: true,
	}
	m.browse.songsSavedTracks = make([]spotify.QueueItem, 40)
	for i := range m.browse.songsSavedTracks {
		m.browse.songsSavedTracks[i] = spotify.QueueItem{ID: fmt.Sprintf("liked-%02d", i), Name: "Liked"}
	}
	m.refreshSongsList()
	m.browse.songsList.Select(10)
	if m.browse.songsList.Paginator.Page == 0 {
		t.Fatal("test setup did not create a stale Songs page")
	}

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	got := next.(model)
	selected, ok := got.browse.songsList.SelectedItem().(trackItem)
	if !ok || songIdentity(selected.item.ID) != "current-track" {
		t.Fatalf("Songs entry selection = %#v, want current track", got.browse.songsList.SelectedItem())
	}
	if got.browse.songsList.Paginator.Page != 0 {
		t.Fatalf("Songs opened on page %d, want first page", got.browse.songsList.Paginator.Page)
	}
}

func TestSongsLibraryScansAtMostOneHundredPlaylistTracks(t *testing.T) {
	calls := 0
	catalog := fakeCatalog{
		playlistItems: func(id string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
			calls++
			items := make([]spotify.QueueItem, 0, limit)
			for i := 0; i < limit; i++ {
				items = append(items, spotify.QueueItem{ID: fmt.Sprintf("track-%d", offset+i), Name: "Track"})
			}
			next := offset + limit
			return &spotify.PlaylistItemsPage{ItemInfos: items, NextOffset: next, HasMore: next < 250}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{}, nil, nil, nil)
	msg := m.loadSongsLibraryCmd([]spotify.PlaylistSummary{{ID: "large", Kind: spotify.ContextKindPlaylist}})().(songsLibraryMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if len(msg.collectionTracks) != maxSongsInSongsTab {
		t.Fatalf("loaded %d playlist tracks; cap is %d", len(msg.collectionTracks), maxSongsInSongsTab)
	}
	if calls != 2 {
		t.Fatalf("made %d playlist page requests to load 100 tracks; want 2", calls)
	}
}
