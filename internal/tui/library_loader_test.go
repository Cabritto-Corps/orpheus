package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"orpheus/internal/config"
	"orpheus/internal/spotify"
)

type fakeCatalog struct {
	playlists func(offset, limit int) (*spotify.PlaylistPage, error)
	albums    func(offset, limit int) (*spotify.PlaylistPage, error)
}

func (f fakeCatalog) ListUserPlaylistsPage(_ context.Context, offset, limit int) (*spotify.PlaylistPage, error) {
	return f.playlists(offset, limit)
}

func (f fakeCatalog) ListSavedAlbumsPage(_ context.Context, offset, limit int) (*spotify.PlaylistPage, error) {
	return f.albums(offset, limit)
}

func (f fakeCatalog) ListPlaylistItemsPage(_ context.Context, _ string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	return &spotify.PlaylistItemsPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
}

func (f fakeCatalog) ListAlbumTracksPage(_ context.Context, _ string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	return &spotify.PlaylistItemsPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
}

func (f fakeCatalog) ResolveContextImageURL(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func (f fakeCatalog) CurrentUserID(_ context.Context) (string, error) { return "u", nil }

func TestLoadPlaylistsCmdInitialInterleavesAlbums(t *testing.T) {
	const totalPerKind = playlistAPIPageSize + 10
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			if offset >= totalPerKind {
				return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
			}
			count := min(limit, totalPerKind-offset)
			items := make([]spotify.PlaylistSummary, 0, limit)
			for i := 0; i < count; i++ {
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
			for i := 0; i < count; i++ {
				items = append(items, spotify.PlaylistSummary{
					ID:   fmt.Sprintf("a-%d", offset+i),
					Name: "Album",
					Kind: spotify.ContextKindAlbum,
				})
			}
			return &spotify.PlaylistPage{Items: items, Offset: offset, Limit: limit, NextOffset: offset + len(items), HasMore: offset+len(items) < totalPerKind}, nil
		},
	}
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	msg, ok := m.loadPlaylistsCmd(0, playlistLoadBatchSize)().(playlistsMsg)
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
			for i := 0; i < limit; i++ {
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
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	msg, ok := m.loadPlaylistsCmd(0, playlistLoadBatchSize)().(playlistsMsg)
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

func TestLoadPlaylistsCmdInitialLoadsBeyondPlaylistLoadMax(t *testing.T) {
	const totalPlaylists = playlistLoadMax + 25
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			if offset >= totalPlaylists {
				return &spotify.PlaylistPage{Items: nil, Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
			}
			count := min(limit, totalPlaylists-offset)
			items := make([]spotify.PlaylistSummary, 0, count)
			for i := 0; i < count; i++ {
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
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	msg, ok := m.loadPlaylistsCmd(0, playlistLoadBatchSize)().(playlistsMsg)
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
