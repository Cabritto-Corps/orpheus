package librespot

import (
	"context"
	"testing"

	"orpheus/internal/spotify"
)

func TestListUserPlaylistsPageNegativeOffset(t *testing.T) {
	c := &playlistCatalog{}
	_, err := c.ListUserPlaylistsPage(context.Background(), -1, 50)
	if err == nil {
		t.Fatal("expected error for negative offset")
	}
}

func TestListSavedAlbumsPageNegativeOffset(t *testing.T) {
	c := &playlistCatalog{}
	_, err := c.ListSavedAlbumsPage(context.Background(), -1, 50)
	if err == nil {
		t.Fatal("expected error for negative offset")
	}
}

func TestListPlaylistItemsPageEmptyID(t *testing.T) {
	c := &playlistCatalog{}
	_, err := c.ListPlaylistItemsPage(context.Background(), "", 0, 50)
	if err == nil {
		t.Fatal("expected error for empty playlist ID")
	}
}

func TestListPlaylistItemsPageNegativeOffset(t *testing.T) {
	c := &playlistCatalog{}
	_, err := c.ListPlaylistItemsPage(context.Background(), "abc", -1, 50)
	if err == nil {
		t.Fatal("expected error for negative offset")
	}
}

func TestResolveContextImageURLEmptyID(t *testing.T) {
	c := &playlistCatalog{}
	_, err := c.ResolveContextImageURL(context.Background(), spotify.ContextKindPlaylist, "")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestResolveContextImageURLUnsupportedKind(t *testing.T) {
	c := &playlistCatalog{}
	_, err := c.ResolveContextImageURL(context.Background(), "artist", "some-id")
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}
