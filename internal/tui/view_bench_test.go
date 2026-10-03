package tui

import (
	"context"
	"fmt"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/spotify"
)

func benchModel(tb testing.TB, queueLen int) model {
	return benchModelTab(tb, queueLen, tabPlayer)
}

func benchModelTab(tb testing.TB, queueLen int, benchTab tab) model {
	tb.Helper()
	m := newModel(context.Background(), nil, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	m.ui.width = 120
	m.ui.height = 40
	m.ui.nerdFonts = false
	m.transport.status = &spotify.PlaybackStatus{
		DeviceName:    "orpheus",
		TrackID:       "spotify:track:7GhIk7Il098yCjg4BQjzvb",
		TrackName:     "Benchmark Track",
		ArtistName:    "Bench Artist",
		AlbumName:     "Bench Album",
		AlbumImageURL: "https://example.com/cover.jpg",
		Playing:       true,
		ProgressMS:    90000,
		DurationMS:    210000,
		Volume:        60,
	}
	m.transport.queue = make([]spotify.QueueItem, queueLen)
	for i := range m.transport.queue {
		m.transport.queue[i] = spotify.QueueItem{ID: fmt.Sprintf("spotify:track:%011d", i), Name: fmt.Sprintf("Track %d With A Fairly Long Title", i), Artist: "Some Artist Name", DurationMS: 200000 + i}
	}
	items := make([]list.Item, 120)
	for i := range items {
		items[i] = playlistItem{summary: spotify.PlaylistSummary{ID: fmt.Sprintf("pl%d", i), Name: fmt.Sprintf("Playlist number %d with long name", i), TrackCount: 42}}
	}
	m.browse.playlistList.SetItems(items)
	m.browse.playlistList.SetSize(m.ui.width, m.ui.height)
	_, _ = m.handleWindowSizeMsg(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.ui.activeTab = benchTab
	return m
}

func benchView(b *testing.B, m model) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkViewPlayerQueue500(b *testing.B) {
	benchView(b, benchModel(b, 500))
}

func BenchmarkViewPlayerQueue16(b *testing.B) {
	benchView(b, benchModel(b, 16))
}

func BenchmarkViewPlaylists(b *testing.B) {
	benchView(b, benchModelTab(b, 0, tabPlaylists))
}

func BenchmarkViewAlbums(b *testing.B) {
	benchView(b, benchModelTab(b, 0, tabAlbums))
}

// Bounds the live-preview path: the theme editor rebuilds the whole bundle
// per keypress, so this must stay far below a frame budget.
func BenchmarkBuildThemeStyles(b *testing.B) {
	state := themePresetState("default")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildThemeStyles(state)
	}
}

var _ = tea.Quit
