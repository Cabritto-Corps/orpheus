package spotify

import (
	"context"
	"testing"

	spotifyapi "github.com/zmb3/spotify/v2"
)

type searchAPIStub struct {
	API
	result *spotifyapi.SearchResult
	query  string
	types  spotifyapi.SearchType
}

func (s *searchAPIStub) Search(_ context.Context, query string, types spotifyapi.SearchType, _ ...spotifyapi.RequestOption) (*spotifyapi.SearchResult, error) {
	s.query, s.types = query, types
	return s.result, nil
}

func TestSearchPageNormalizesTracksAndAlbums(t *testing.T) {
	client := &searchAPIStub{result: &spotifyapi.SearchResult{
		Tracks: &spotifyapi.FullTrackPage{Tracks: []spotifyapi.FullTrack{{
			SimpleTrack: spotifyapi.SimpleTrack{
				ID: "track-id", URI: "spotify:track:track-id", Name: "Song", Duration: 215000,
				Artists: []spotifyapi.SimpleArtist{{Name: "Artist"}},
			},
			Album: spotifyapi.SimpleAlbum{Name: "Record", Images: []spotifyapi.Image{{URL: "track-art"}}},
		}}},
		Albums: &spotifyapi.SimpleAlbumPage{Albums: []spotifyapi.SimpleAlbum{{
			ID: "album-id", URI: "spotify:album:album-id", Name: "Record", TotalTracks: 9,
			Artists: []spotifyapi.SimpleArtist{{Name: "Artist"}}, Images: []spotifyapi.Image{{URL: "album-art"}},
		}}},
	}}
	service := NewService(client, Options{})
	page, err := service.SearchPage(context.Background(), "  song  ", 10, 10)
	if err != nil {
		t.Fatalf("SearchPage returned error: %v", err)
	}
	if client.query != "song" || client.types != spotifyapi.SearchTypeTrack|spotifyapi.SearchTypeAlbum|spotifyapi.SearchTypeArtist {
		t.Fatalf("unexpected Spotify search request: query=%q types=%v", client.query, client.types)
	}
	if page.Offset != 10 || page.Limit != 10 || page.NextOffset != 20 || len(page.Items) != 2 {
		t.Fatalf("unexpected page metadata: %#v", page)
	}
	track := page.Items[0]
	if track.Kind != "track" || track.ID != "track-id" || track.Owner != "Artist" || track.AlbumName != "Record" || track.ImageURL != "track-art" || track.DurationMS != 215000 {
		t.Fatalf("track was not normalized: %#v", track)
	}
	album := page.Items[1]
	if album.Kind != "album" || album.ID != "album-id" || album.Owner != "Artist" || album.ImageURL != "album-art" || album.TrackCount != 9 {
		t.Fatalf("album was not normalized: %#v", album)
	}
}

func TestSearchPageValidatesQueryAndOffset(t *testing.T) {
	service := NewService(&searchAPIStub{}, Options{})
	if _, err := service.SearchPage(context.Background(), "  ", 0, 10); err == nil {
		t.Fatal("expected empty query to be rejected")
	}
	if _, err := service.SearchPage(context.Background(), "song", -1, 10); err == nil {
		t.Fatal("expected negative offset to be rejected")
	}
}
