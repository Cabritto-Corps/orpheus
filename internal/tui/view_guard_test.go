package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"orpheus/internal/config"
	"orpheus/internal/spotify"
)

type frameVariant struct {
	name     string
	width    int
	height   int
	tab      tab
	playing  bool
	hasQueue bool
	modal    string // "", "help", "settings", "settings-theme", "settings-theme-options", "settings-keys", "settings-capture", "popup"
	erroring bool
}

func guardModel(tb testing.TB, v frameVariant) model {
	tb.Helper()
	m := newModel(tb.Context(), nil, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	m.ui.width = v.width
	m.ui.height = v.height
	m.ui.nerdFonts = false
	m.ui.activeTab = v.tab
	if v.playing {
		m.transport.status = &spotify.PlaybackStatus{
			DeviceName: "orpheus test device with a rather long name",
			TrackID:    "spotify:track:7GhIk7Il098yCjg4BQjzvb",
			TrackName:  "An Absurdly Long Track Title That Must Truncate Somewhere 🎵 Émoji Cömbo",
			ArtistName: "An Artist With A Very Long Name And A Sequel",
			AlbumName:  "Some Album Title That Is Also Fairly Long (Deluxe Edition)",
			Playing:    v.playing,
			ProgressMS: 90000,
			DurationMS: 3721000,
			Volume:     100,
		}
	}
	if v.hasQueue {
		m.transport.queue = make([]spotify.QueueItem, 30)
		for i := range m.transport.queue {
			m.transport.queue[i] = spotify.QueueItem{
				Name:       fmt.Sprintf("Queue Track %d — Extended Remix Featuring A Guest 🎧", i),
				Artist:     "Some Artist Name",
				DurationMS: 3721000,
			}
		}
		m.transport.stableQueueLen = len(m.transport.queue)
	}
	items := make([]list.Item, 40)
	for i := range items {
		items[i] = playlistItem{summary: spotify.PlaylistSummary{
			ID:         fmt.Sprintf("pl%d", i),
			URI:        fmt.Sprintf("spotify:playlist:pl%d", i),
			Name:       fmt.Sprintf("Playlist number %d with a long name that wraps", i),
			Owner:      "A Owner Name",
			TrackCount: 42,
		}}
	}
	if v.playing {
		// One browse row carries the now-playing glyph.
		nowPlayingContextURI = "spotify:playlist:pl3"
	}
	m.browse.playlistList.SetItems(items)
	m.browse.albumList.SetItems(items[:20])
	if v.erroring {
		m.browse.playlistsErr = fmt.Errorf("429 too many requests")
		m.transport.playbackErr = fmt.Errorf("playback unavailable")
	}
	m2, err := m.handleWindowSizeMsg(tea.WindowSizeMsg{Width: v.width, Height: v.height})
	if err != nil {
		tb.Fatalf("window size: %v", err)
	}
	m = m2.(model)
	switch v.modal {
	case "settings":
		m2, _ := m.openSettings()
		m = m2.(model)
	case "settings-theme":
		m2, _ := m.openSettings()
		m = m2.(model)
		m.ui.settings.mode = settingsModeTheme
	case "settings-theme-options":
		m2, _ := m.openSettings()
		m = m2.(model)
		m.openThemeOptions()
	case "settings-keys":
		m2, _ := m.openSettings()
		m = m2.(model)
		m.ui.settings.mode = settingsModeKeys
		m.ui.settings.keysTableDirty = true
	case "settings-capture":
		m2, _ := m.openSettings()
		m = m2.(model)
		m.ui.settings.mode = settingsModeCapture
		m.ui.settings.captureKey = "play_pause"
	case "help":
		m.ui.helpOpen = true
		m.ensureHelpViewport()
	case "popup":
		m.ui.trackPopupOpen = true
		m.ui.trackPopupName = "Some Playlist Name"
		items := []spotify.QueueItem{}
		for i := range 8 {
			items = append(items, spotify.QueueItem{
				ID: fmt.Sprintf("spotify:track:%011d", i), Name: fmt.Sprintf("Track %d", i), Artist: "Artist", DurationMS: 200000,
			})
		}
		m.ui.trackPopupItems = items
		_, listW, listH := popupModalSize(v.width, v.height)
		popup := list.New(nil, newTrackPopupDelegate(), listW, listH)
		popup.SetShowTitle(false)
		popup.SetShowStatusBar(true)
		popup.SetFilteringEnabled(true)
		popup.SetShowFilter(true)
		popup.SetShowHelp(false)
		m.ui.trackPopupList = popup
		m.ui.trackPopupWidth = listW - 4
		m.retruncateTrackPopupTitles()
	}
	return m
}

func assertFrameContract(t *testing.T, name string, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) > h {
		shortened := lines[min(3, len(lines)-1):]
		t.Errorf("%s: frame has %d lines, terminal %d; lines %d..: %q",
			name, len(lines), h, min(3, len(lines)-1)+1, strings.Join(shortened, " | "))
	}
	for i, line := range lines {
		if lw := lipgloss.Width(line); lw > w {
			t.Errorf("%s: line %d is %d cols (max %d): %q", name, i+1, lw, w, line)
		}
	}
}

func TestViewFrameContract(t *testing.T) {
	sizes := [][2]int{{40, 12}, {50, 16}, {60, 20}, {80, 24}, {120, 40}}
	for _, size := range sizes {
		for _, tb := range []tab{tabPlaylists, tabAlbums, tabPlayer} {
			for _, playing := range []bool{true, false} {
				for _, variant := range []frameVariant{
					{name: "plain", hasQueue: true},
					{name: "errors", hasQueue: true, erroring: true},
				} {
					variant.width, variant.height, variant.tab, variant.playing = size[0], size[1], tb, playing
					name := fmt.Sprintf("%dx%d/%s/%s/%s", size[0], size[1], tabName(variant.tab), variant.name, map[bool]string{true: "playing", false: "idle"}[playing])
					m := guardModel(t, variant)
					assertFrameContract(t, name, m.View().Content, variant.width, variant.height)
				}
			}
		}
	}
}

func TestViewFrameContractAllThemes(t *testing.T) {
	t.Cleanup(func() { applyTheme(themePresetState("default")) })
	sizes := [][2]int{{40, 12}, {120, 40}}
	for _, themeName := range themeRegistryNames() {
		applyTheme(themePresetState(themeName))
		for _, size := range sizes {
			for _, tb := range []tab{tabPlaylists, tabAlbums, tabPlayer} {
				variant := frameVariant{name: themeName, width: size[0], height: size[1], tab: tb, hasQueue: true}
				m := guardModel(t, variant)
				name := fmt.Sprintf("%dx%d/%s/%s", size[0], size[1], themeName, tabName(variant.tab))
				assertFrameContract(t, name, m.View().Content, variant.width, variant.height)
			}
		}
	}
}

func TestViewFrameContractBackgroundModes(t *testing.T) {
	t.Cleanup(func() { applyTheme(themePresetState("default")) })
	sizes := [][2]int{{60, 20}, {120, 40}}
	for _, style := range backgroundStyleChoices {
		for _, size := range sizes {
			for _, tb := range []tab{tabPlaylists, tabAlbums, tabPlayer} {
				variant := frameVariant{name: "bg-" + style, width: size[0], height: size[1], tab: tb, hasQueue: true}
				m := guardModel(t, variant)
				// The style applies after construction: newModel re-applies
				// the stored theme, and View reads the package styles live.
				st := themePresetState("default")
				st.backgrounds.Style = style
				applyTheme(st)
				name := fmt.Sprintf("%dx%d/bg-%s/%s", size[0], size[1], style, tabName(variant.tab))
				assertFrameContract(t, name, m.View().Content, variant.width, variant.height)
			}
		}
	}
}

func TestViewFrameContractModals(t *testing.T) {
	sizes := [][2]int{{40, 12}, {50, 16}, {80, 24}, {120, 40}}
	for _, size := range sizes {
		for _, modal := range []string{"settings", "settings-theme", "settings-theme-options", "settings-keys", "settings-capture", "help", "popup"} {
			variant := frameVariant{name: modal, width: size[0], height: size[1], tab: tabPlayer, playing: true, hasQueue: true, modal: modal}
			m := guardModel(t, variant)
			assertFrameContract(t, fmt.Sprintf("modal-%s-%dx%d", modal, size[0], size[1]), m.View().Content, variant.width, variant.height)
		}
	}
}

func TestPopupListSizeInvariantUnderResize(t *testing.T) {
	for _, size := range [][2]int{{60, 20}, {80, 24}, {120, 40}} {
		v := frameVariant{name: "popup", width: size[0], height: size[1], tab: tabPlayer, playing: true, hasQueue: true, modal: "popup"}
		m := guardModel(t, v)
		_, wantW, wantH := popupModalSize(size[0], size[1])
		gotW, gotH := m.ui.trackPopupList.Width(), m.ui.trackPopupList.Height()
		if gotW != wantW || gotH != wantH {
			t.Fatalf("open at %dx%d: list %dx%d, want %dx%d", size[0], size[1], gotW, gotH, wantW, wantH)
		}
		m2, _ := m.handleWindowSizeMsg(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = m2.(model)
		gotW, gotH = m.ui.trackPopupList.Width(), m.ui.trackPopupList.Height()
		if gotW != wantW || gotH != wantH {
			t.Fatalf("resize at %dx%d: list %dx%d, want open-time %dx%d", size[0], size[1], gotW, gotH, wantW, wantH)
		}
	}
}

func TestModalHeaderNeverOverflows(t *testing.T) {
	// title+hint spanning exactly the inner width used to overflow via the
	// max(2,...) gap floor and wrap inside the box.
	cases := []struct{ title, hint string }{
		{strings.Repeat("T", 30), strings.Repeat("H", 30)},
		{strings.Repeat("T", 60), strings.Repeat("H", 60)},
		{"Settings", "enter: change   +/-: adjust"},
		{"Theme", "↑/↓: preview   enter: save   esc: revert"},
		{"", "only-hint"},
		{"only-title", ""},
	}
	for _, size := range []int{34, 40, 60, 100} {
		for _, c := range cases {
			header := modalHeader(c.title, c.hint, size)
			if w := lipgloss.Width(header); w > size {
				t.Fatalf("innerW %d: header %d wide for %q/%q", size, w, c.title, c.hint)
			}
		}
	}
}

func tabName(t tab) string {
	switch t {
	case tabPlaylists:
		return "playlists"
	case tabAlbums:
		return "albums"
	default:
		return "player"
	}
}
