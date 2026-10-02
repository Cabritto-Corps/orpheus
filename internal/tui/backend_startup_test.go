package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	if got.resolveCatalog() == nil {
		t.Fatal("expected the upgraded catalog to be visible to queued loads")
	}
	if cmd == nil {
		t.Fatal("expected the upgraded catalog to trigger a library load")
	}
	if _, ok := cmd().(playlistsMsg); !ok {
		t.Fatalf("expected a playlists message from the upgraded catalog, got %T", cmd())
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
