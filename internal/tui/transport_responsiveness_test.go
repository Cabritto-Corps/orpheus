package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func kittyRestoreModel(t *testing.T, tab tab) model {
	t.Helper()
	t.Setenv("TMUX", "")

	m := framedTestModel()
	m.ui.width = 100
	m.ui.height = 40
	m.ui.activeTab = tab
	m.ui.imgs.protocol = imageProtocolKitty

	statusURL := ""
	switch tab {
	case tabPlaylists:
		statusURL = "playlist-cover"
		m.browse.playlistList.SetItems([]list.Item{
			playlistItem{summary: spotify.PlaylistSummary{ID: "pl-1", Name: "Playlist", URI: "spotify:playlist:pl-1", ImageURL: statusURL}},
		})
		m.browse.playlistList.Select(0)
	case tabAlbums:
		statusURL = "album-cover"
		m.browse.albumList.SetItems([]list.Item{
			playlistItem{summary: spotify.PlaylistSummary{ID: "al-1", Name: "Album", URI: "spotify:album:al-1", ImageURL: statusURL}},
		})
		m.browse.albumList.Select(0)
	default:
		statusURL = "player-cover"
		m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: statusURL, Playing: true, ProgressMS: 1000, DurationMS: 200000}
	}
	m.ui.imgs.encoded[statusURL] = "ZmFrZQ=="
	if out := m.kittyOverlay(); !strings.Contains(out, "a=T") {
		t.Fatalf("test precondition failed: expected an initial transmit, got %q", out)
	}
	return m
}

func rawOverlayPayload(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected an overlay command for an immediate restore")
	}
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		switch msg := next().(type) {
		case tea.RawMsg:
			return fmt.Sprint(msg.Msg)
		case tea.BatchMsg:
			pending = append(pending, msg...)
		}
	}
	t.Fatal("batched commands contained no overlay emission")
	return ""
}

func TestHelpCloseRestoresArtImmediately(t *testing.T) {
	m := kittyRestoreModel(t, tabPlayer)
	m.ui.helpOpen = true
	// The hide frames while open arm the close-frame restore.
	if hide := m.kittyOverlay(); !strings.Contains(hide, "a=d,d=i") {
		t.Fatalf("expected the modal hide while help is open, got %q", hide)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(model).ui.helpOpen {
		t.Fatal("expected help to close")
	}
	if got := rawOverlayPayload(t, cmd); !strings.Contains(got, "a=") {
		t.Fatalf("expected an overlay emission on help close, got %q", got)
	}
}

func TestTrackPopupCloseRestoresArtImmediately(t *testing.T) {
	m := kittyRestoreModel(t, tabPlayer)
	m.ui.trackPopupOpen = true
	m.ui.trackPopupList = newTrackPopupList(m.styles, m.ui.width, m.ui.height)
	if hide := m.kittyOverlay(); !strings.Contains(hide, "a=d,d=i") {
		t.Fatalf("expected the modal hide while the popup is open, got %q", hide)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(model).ui.trackPopupOpen {
		t.Fatal("expected the track popup to close")
	}
	if got := rawOverlayPayload(t, cmd); !strings.Contains(got, "a=") {
		t.Fatalf("expected an overlay emission on popup close, got %q", got)
	}
}

func TestSettingsCloseRestoresArtImmediately(t *testing.T) {
	m := kittyRestoreModel(t, tabPlayer)
	m.ui.settings.open = true
	m.ui.settings.mode = settingsModeRoot
	if hide := m.kittyOverlay(); !strings.Contains(hide, "a=d,d=i") {
		t.Fatalf("expected the modal hide while settings is open, got %q", hide)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(model).ui.settings.open {
		t.Fatal("expected settings to close")
	}
	if got := rawOverlayPayload(t, cmd); !strings.Contains(got, "a=") {
		t.Fatalf("expected an overlay emission on settings close, got %q", got)
	}
}

func TestTabSwitchRestoresArtImmediately(t *testing.T) {
	m := kittyRestoreModel(t, tabPlaylists)
	// The destination tab needs its own selected art for the switch path to restore.
	m.ui.activeTab = tabAlbums
	m.browse.albumList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "al-1", Name: "Album", URI: "spotify:album:al-1", ImageURL: "album-cover"}},
	})
	m.browse.albumList.Select(0)
	m.ui.imgs.encoded["album-cover"] = "ZmFrZQ=="
	m.ui.activeTab = tabPlaylists

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if next.(model).ui.activeTab != tabAlbums {
		t.Fatal("expected the tab switch to albums")
	}
	if got := rawOverlayPayload(t, cmd); !strings.Contains(got, "a=") {
		t.Fatalf("expected an overlay emission on tab switch, got %q", got)
	}
}

func TestImagesBatchLoadRestoresArtImmediately(t *testing.T) {
	m := kittyRestoreModel(t, tabPlayer)
	next, cmd := m.handleImagesBatchLoadedMsg(imagesBatchLoadedMsg{
		results: []imageLoadedMsg{{url: "player-cover"}},
	})
	_ = next
	if got := rawOverlayPayload(t, cmd); !strings.Contains(got, "a=") {
		t.Fatalf("expected an overlay emission on batch load, got %q", got)
	}
}

func TestStuckTransportTransitionClearsOnTick(t *testing.T) {
	m := NewLoaderModel()
	m.beginTransportTransition()
	fromTrack := m.transport.transition.FromTrack()
	m.transport.transition.startedAt = time.Now().Add(-(transportTransitionStuckTimeout + time.Second))
	m.transport.status = &spotify.PlaybackStatus{TrackID: fromTrack, ProgressMS: 0}

	next, _ := m.handleTickMsg()
	got := next.(model)
	if got.transport.transition.Pending() {
		t.Fatal("expected a stuck transition to clear on the tick without waiting for a state push")
	}
	if got.transport.playbackErr == nil || got.transport.playbackErr.Error() != "track didn't start — skip again" {
		t.Fatalf("expected the stuck-transition error on tick release, got %v", got.transport.playbackErr)
	}
}

func TestPlayPauseFlipsIconOnSuccessfulSend(t *testing.T) {
	ch := make(chan librespot.TUICommand, 2)
	m := volTestModel(ch, 50)
	m.transport.status.Playing = true

	if cmd := m.executePlaybackInput(playbackInputPlayPause, 0); cmd != nil {
		t.Fatalf("expected a successful play/pause send to need no retry, got %v", cmd)
	}
	if m.transport.status.Playing {
		t.Fatal("expected the pause icon to flip before the backend push")
	}
	if cmd := <-ch; cmd.Kind != librespot.TUICommandPause {
		t.Fatalf("expected a pause command, got %+v", cmd)
	}

	if cmd := m.executePlaybackInput(playbackInputPlayPause, 0); cmd != nil {
		t.Fatalf("expected a successful play/pause send to need no retry, got %v", cmd)
	}
	if !m.transport.status.Playing {
		t.Fatal("expected the play icon to flip before the backend push")
	}
	if cmd := <-ch; cmd.Kind != librespot.TUICommandResume {
		t.Fatalf("expected a resume command, got %+v", cmd)
	}
}

func TestIdleTickStretchesOnlyWhenNothingNeedsIt(t *testing.T) {
	idleModel := func() model {
		m := NewLoaderModel()
		m.browse.playlistsLoading = false
		m.ui.startupCoverBoostTicks = 0
		return m
	}

	if m := idleModel(); m.nextTickInterval() != uiIdleTickInterval {
		t.Fatalf("expected idle tick %v, got %v", uiIdleTickInterval, m.nextTickInterval())
	}

	cases := map[string]func(*model){
		"playing":        func(m *model) { m.transport.status = &spotify.PlaybackStatus{Playing: true} },
		"transition":     func(m *model) { m.beginTransportTransition() },
		"popup":          func(m *model) { m.ui.trackPopupOpen = true },
		"input":          func(m *model) { m.enqueuePlaybackInput(playbackInputVolUp) },
		"volume":         func(m *model) { m.transport.volDebouncePending = 60 },
		"cover queue":    func(m *model) { m.enqueueCoverURL("https://example.com/cover") },
		"cover inflight": func(m *model) { m.ui.imgs.inflight["https://example.com/cover"] = struct{}{} },
	}
	for name, mutate := range cases {
		m := idleModel()
		mutate(&m)
		if got := m.nextTickInterval(); got != uiTickInterval {
			t.Errorf("%s: expected active tick %v, got %v", name, uiTickInterval, got)
		}
	}
}
