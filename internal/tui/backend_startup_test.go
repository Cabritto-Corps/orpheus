package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

func TestPlayerBackendFailedSurfacesStartupError(t *testing.T) {
	m := NewLoaderModel()
	m.transport.playerConnecting = true
	boom := errors.New("login unavailable")

	next, cmd := m.handlePlayerBackendMsg(playerBackendMsg{err: boom})
	got := next.(model)
	if got.transport.playerConnecting {
		t.Fatal("expected backend failure to leave the connecting state")
	}
	if got.transport.playbackErr != boom {
		t.Fatalf("expected the startup failure to surface, got %v", got.transport.playbackErr)
	}
	if cmd != nil {
		t.Fatalf("expected no follow-up command for a failed backend, got %v", cmd)
	}
}

func TestPlayerBackendReadySwapsCatalogAndReloads(t *testing.T) {
	m := NewLoaderModel()
	m.transport.playerConnecting = true
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}

	next, cmd := m.handlePlayerBackendMsg(playerBackendMsg{ready: true, catalog: catalog})
	got := next.(model)
	if got.transport.playerConnecting {
		t.Fatal("expected backend success to leave the connecting state")
	}
	if !got.transport.revealArmed {
		t.Fatal("expected the ready backend to arm the startup sync bound")
	}
	if got.transport.revealGraceEnd.Before(time.Now()) {
		t.Fatal("expected a future grace deadline")
	}
	if got.resolveCatalog() == nil {
		t.Fatal("expected the upgraded catalog to be visible to queued loads")
	}
	if cmd == nil {
		t.Fatal("expected the upgraded catalog to trigger a library load")
	}
	// The load rides in a batch with the sync deadline tick; unwrap it.
	msgs := []tea.Msg{cmd()}
	for len(msgs) > 0 {
		switch got := msgs[0].(type) {
		case tea.BatchMsg:
			msgs = msgs[1:]
			for _, sub := range got {
				if sub != nil {
					msgs = append(msgs, sub())
				}
			}
		case playlistsMsg:
			msgs = nil
		default:
			msgs = msgs[1:]
		}
	}
}

func TestConnectingPlayerFrameContract(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {100, 30}} {
		variant := frameVariant{name: "connecting", width: size[0], height: size[1], tab: tabPlayer}
		m := guardModel(t, variant)
		m.transport.status = nil
		m.transport.playerConnecting = true
		content := m.View().Content
		assertFrameContract(t, "connecting", content, size[0], size[1])
		if !strings.Contains(content, "connecting to Spotify") {
			t.Fatalf("connecting state must name the pending player, got %q", content)
		}
	}
}

// While the startup gate holds, the player bar is already in place with its
// empty scaffold — it never appears late, data fills in via the normal push.
func TestPlayerBarAlwaysRenders(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 100
	m.ui.height = 40

	idle := m.playerBarView()
	if !strings.Contains(idle, "--:--") {
		t.Fatalf("nil-status bar must render the empty scaffold, got %q", idle)
	}

	m.transport.status = &spotify.PlaybackStatus{TrackName: "song", DurationMS: 120000, ProgressMS: 5000}
	if bar := m.playerBarView(); strings.Contains(bar, "--:--") {
		t.Fatalf("bar with a live track must show its duration, got %q", bar)
	}
}

// The only spinner during startup is the shared connecting one; image
// panels hold label-only, the player bar is never gated.
func TestStartupGateShowsSingleMessage(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 100
	m.ui.height = 40
	m.transport.playerConnecting = true

	if !m.startupPending() {
		t.Fatal("connecting must hold")
	}
	bar := m.playerBarView()
	if strings.Contains(bar, "connecting") || strings.Contains(bar, "loading library") {
		t.Fatalf("the player bar is never gated, got %q", bar)
	}
	panel := m.coverPreviewPanel(40, 20, 30, 15)
	if strings.Contains(panel, m.ui.spinner.View()) || strings.Contains(panel, startupText) || strings.Contains(panel, "select an item") {
		t.Fatalf("the preview panel must hold label-only, got %q", panel)
	}
}

// The startup sync releases on pushed state, on a mid-load state (settling
// the reveal), or on the grace deadline with nothing pushed.
func TestStartupSyncReleases(t *testing.T) {
	// A mid-load state does not split the reveal: hold until the settle,
	// then everything lands in one frame.
	m := NewLoaderModel()
	m.transport.playerConnecting = true
	m.transport.statePushSeen = true
	if !m.startupPending() {
		t.Fatal("connecting must hold")
	}
	m.transport.playerConnecting = false
	m.browse.librarySettled = false
	if !m.startupPending() {
		t.Fatal("a mid-load state must still hold until the library settles")
	}
	m.browse.librarySettled = true
	if m.startupPending() {
		t.Fatal("settled + state seen must release the hold")
	}

	// Attached and loaded, nothing pushed yet, grace running: held.
	m2 := NewLoaderModel()
	m2.transport.revealArmed = true
	m2.transport.revealGraceEnd = time.Now().Add(time.Second)
	if !m2.startupPending() {
		t.Fatal("state-await tail must hold until the deadline passes")
	}

	// Same, deadline passed: released (idle backend).
	m2.transport.revealGraceEnd = time.Now().Add(-time.Millisecond)
	if m2.startupPending() {
		t.Fatal("expired grace must release the hold")
	}

	// Arming on attach: the reveal deadline tick fires independently of the
	// library load.
	m4 := NewLoaderModel()
	m4.transport.playerConnecting = true
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}
	next, _ := m4.handlePlayerBackendMsg(playerBackendMsg{ready: true, catalog: catalog})
	m4 = next.(model)
	if !m4.transport.revealArmed {
		t.Fatal("attach must arm the reveal bound")
	}
}

// During the hold the header plays no playback pieces: no play/pause icon,
// no title, no volume bar — all of them join the single reveal.
func TestHeaderHoldsDuringStartup(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 100
	m.ui.height = 40
	m.transport.playerConnecting = true
	m.transport.status = &spotify.PlaybackStatus{TrackName: "song", Volume: 50}

	header := m.headerView()
	if strings.Contains(header, "Playing") || strings.Contains(header, "Paused") ||
		strings.Contains(header, "song") || strings.Contains(header, "%") {
		t.Fatalf("held header must show brand+connecting only, got %q", header)
	}

	m.transport.playerConnecting = false
	m.browse.librarySettled = true
	header = m.headerView()
	if !strings.Contains(header, "song") || !strings.Contains(header, "%") {
		t.Fatalf("released header must show the full status, got %q", header)
	}
}

func TestDynamicCatalogExecutorFollowsUpgrades(t *testing.T) {
	source := newCatalogSource(nil)
	executor := NewDynamicCatalogExecutor(context.Background(), source.get)
	if got := executor(context.Background(), loader.LoadRequest{Type: loader.LoadTypeContextImageURL}); len(got) != 0 {
		t.Fatalf("expected no context resolve before the backend upgrade, got %+v", got)
	}
	source.set(stubCatalogURL("https://example.com/upgraded"))
	got := executor(context.Background(), loader.LoadRequest{
		Type:    loader.LoadTypeContextImageURL,
		Items:   []loader.LoadItem{{Kind: spotify.ContextKindAlbum, ID: "al-1"}},
		Timeout: time.Second,
	})
	if len(got) != 1 || got[0].Error != nil {
		t.Fatalf("expected the upgraded catalog to answer, got %+v", got)
	}
	if url, ok := got[0].Data.(loader.ImageURLData); !ok || url.URL != "https://example.com/upgraded" {
		t.Fatalf("expected the upgraded image URL, got %+v", got[0].Data)
	}
}

type stubCatalog struct {
	spotify.PlaylistCatalog
	url string
}

func stubCatalogURL(url string) spotify.PlaylistCatalog {
	return stubCatalog{url: url}
}

func (s stubCatalog) ResolveContextImageURL(_ context.Context, _, _ string) (string, error) {
	return s.url, nil
}
