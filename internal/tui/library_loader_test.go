package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/spotify"
)

type fakeCatalog struct {
	playlists     func(offset, limit int) (*spotify.PlaylistPage, error)
	albums        func(offset, limit int) (*spotify.PlaylistPage, error)
	playlistItems func(id string, offset, limit int) (*spotify.PlaylistItemsPage, error)
	albumItems    func(id string, offset, limit int) (*spotify.PlaylistItemsPage, error)
	recent        func(limit int) ([]spotify.QueueItem, error)
}

func (f fakeCatalog) ListUserPlaylistsPage(_ context.Context, offset, limit int) (*spotify.PlaylistPage, error) {
	return f.playlists(offset, limit)
}

func (f fakeCatalog) ListSavedAlbumsPage(_ context.Context, offset, limit int) (*spotify.PlaylistPage, error) {
	return f.albums(offset, limit)
}

func (f fakeCatalog) ListSavedTracksPage(_ context.Context, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	return &spotify.PlaylistItemsPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
}

func (f fakeCatalog) ListRecentlyPlayedTracks(_ context.Context, limit int) ([]spotify.QueueItem, error) {
	if f.recent != nil {
		return f.recent(limit)
	}
	return nil, nil
}

func TestSavedTracksExcludedFromRecents(t *testing.T) {
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset}, nil
		},
		recent: func(limit int) ([]spotify.QueueItem, error) {
			return []spotify.QueueItem{{ID: "recent-id", Name: "Recent Song", Artist: "Artist", ImageURL: "recent-art"}}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	msg := m.loadPlaylistsCmd()().(playlistsMsg)
	next, _ := m.handlePlaylistsMsg(msg)
	got := next.(model)
	rmsg := got.loadRecentsLibraryCmd()().(recentsLibraryMsg)
	next, _ = got.handleRecentsLibraryMsg(rmsg)
	got = next.(model)
	if len(got.browse.recentsList.Items()) != 1 || got.browse.recentsList.Items()[0].(trackItem).item.ID != "recent-id" {
		t.Fatalf("Recents must hold recent plays only: %#v", got.browse.recentsList.Items())
	}
	if len(got.browse.playlistList.Items()) == 0 {
		t.Fatal("Liked Songs disappeared from Playlists")
	}
	liked, ok := got.browse.playlistList.Items()[0].(playlistItem)
	if !ok || liked.summary.Kind != spotify.ContextKindLikedSongs {
		t.Fatalf("first playlist item is not Liked Songs: %#v", got.browse.playlistList.Items()[0])
	}
}

func (f fakeCatalog) ListPlaylistItemsPage(_ context.Context, id string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	if f.playlistItems != nil {
		return f.playlistItems(id, offset, limit)
	}
	return &spotify.PlaylistItemsPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
}

func (f fakeCatalog) ListAlbumTracksPage(_ context.Context, id string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	if f.albumItems != nil {
		return f.albumItems(id, offset, limit)
	}
	return &spotify.PlaylistItemsPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
}

func (f fakeCatalog) ResolveContextImageURL(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func (f fakeCatalog) SearchPage(_ context.Context, _ string, offset, limit int) (*spotify.SearchPage, error) {
	return &spotify.SearchPage{Offset: offset, Limit: limit, NextOffset: offset + limit}, nil
}

func TestInitBootstrapsLibraryLoad(t *testing.T) {
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	msg := m.Init()()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected Init to return a batch, got %T", msg)
	}
	found := false
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		if _, ok := cmd().(playlistsMsg); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("expected Init to bootstrap a library load")
	}
}

func TestLoadPlaylistsCmdInitialInterleavesAlbums(t *testing.T) {
	const totalPerKind = playlistAPIPageSize + 10
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			if offset >= totalPerKind {
				return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
			}
			count := min(limit, totalPerKind-offset)
			items := make([]spotify.PlaylistSummary, 0, limit)
			for i := range count {
				items = append(items, spotify.PlaylistSummary{
					ID:   fmt.Sprintf("p-%d", offset+i),
					Name: "Playlist",
					Kind: spotify.ContextKindPlaylist,
				})
			}
			return &spotify.PlaylistPage{Items: items, Offset: offset, Limit: limit, NextOffset: offset + len(items), HasMore: offset+len(items) < totalPerKind}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			if offset >= totalPerKind {
				return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
			}
			count := min(limit, totalPerKind-offset)
			items := make([]spotify.PlaylistSummary, 0, limit)
			for i := range count {
				items = append(items, spotify.PlaylistSummary{
					ID:   fmt.Sprintf("a-%d", offset+i),
					Name: "Album",
					Kind: spotify.ContextKindAlbum,
				})
			}
			return &spotify.PlaylistPage{Items: items, Offset: offset, Limit: limit, NextOffset: offset + len(items), HasMore: offset+len(items) < totalPerKind}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	msg, ok := m.loadPlaylistsCmd()().(playlistsMsg)
	if !ok {
		t.Fatalf("expected playlistsMsg")
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	foundAlbum := false
	for _, it := range msg.items {
		if it.Kind == spotify.ContextKindAlbum {
			foundAlbum = true
			break
		}
	}
	if !foundAlbum {
		t.Fatalf("expected at least one album in initial library load")
	}
}

func TestLoadPlaylistsCmdInitialAlbumsForbiddenSetsHintFlag(t *testing.T) {
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			items := make([]spotify.PlaylistSummary, 0, limit)
			for i := range limit {
				items = append(items, spotify.PlaylistSummary{
					ID:   fmt.Sprintf("p-%d", offset+i),
					Name: "Playlist",
					Kind: spotify.ContextKindPlaylist,
				})
			}
			return &spotify.PlaylistPage{Items: items, Offset: offset, Limit: limit, NextOffset: offset + len(items), HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return nil, errors.New("forbidden")
		},
	}
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	msg, ok := m.loadPlaylistsCmd()().(playlistsMsg)
	if !ok {
		t.Fatalf("expected playlistsMsg")
	}
	if msg.err != nil {
		t.Fatalf("expected no fatal error, got %v", msg.err)
	}
	if !msg.albumsForbidden {
		t.Fatalf("expected albumsForbidden flag when album API is forbidden")
	}
}

func TestLoadPlaylistsCmdInitialLoadIsUncapped(t *testing.T) {
	// The library loads eagerly in one shot by design; there is no page cap.
	const totalPlaylists = 525
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			if offset >= totalPlaylists {
				return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
			}
			count := min(limit, totalPlaylists-offset)
			items := make([]spotify.PlaylistSummary, 0, count)
			for i := range count {
				items = append(items, spotify.PlaylistSummary{
					ID:   fmt.Sprintf("p-%d", offset+i),
					Name: "Playlist",
					Kind: spotify.ContextKindPlaylist,
				})
			}
			return &spotify.PlaylistPage{
				Items:      items,
				Offset:     offset,
				Limit:      limit,
				NextOffset: offset + len(items),
				HasMore:    offset+len(items) < totalPlaylists,
			}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	msg, ok := m.loadPlaylistsCmd()().(playlistsMsg)
	if !ok {
		t.Fatalf("expected playlistsMsg")
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if len(msg.items) != totalPlaylists {
		t.Fatalf("expected %d items, got %d", totalPlaylists, len(msg.items))
	}
}

func TestAlbumTabRefreshReloadsLibrary(t *testing.T) {
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, nil)
	m.ui.activeTab = tabAlbums
	m.browse.playlistsErr = errors.New("boom")

	next, cmd := m.handleAlbumKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = next.(model)
	if !m.browse.playlistsLoading {
		t.Fatal("expected refresh to restart the library load")
	}
	if m.browse.playlistsErr != nil {
		t.Fatal("expected refresh to clear the library error")
	}
	if cmd == nil {
		t.Fatal("expected refresh to issue a library load command")
	}
}
