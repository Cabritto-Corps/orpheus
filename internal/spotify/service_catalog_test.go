package spotify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func httpJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestListPlaylistItemsPageParsesItemAndTrack(t *testing.T) {
	s := &Service{
		itemsHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/playlists/pl/items" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
				}
				return httpJSONResponse(http.StatusOK, `{
					"items":[
						{"item":{"id":"item-1","name":"Song A","duration_ms":1000,"artists":[{"name":"Artist A"}]}},
						{"track":{"id":"track-2","name":"Song B","duration_ms":2000,"artists":[{"name":"Artist B"}]}}
					],
					"next":"https://api.spotify.com/v1/playlists/pl/items?offset=2&limit=2"
				}`), nil
			}),
		},
	}

	page, err := s.ListPlaylistItemsPage(context.Background(), "pl", 0, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.ItemIDs) != 2 || page.ItemIDs[0] != "item-1" || page.ItemIDs[1] != "track-2" {
		t.Fatalf("unexpected ids: %#v", page.ItemIDs)
	}
	if len(page.ItemInfos) != 2 || page.ItemInfos[1].Artist != "Artist B" {
		t.Fatalf("unexpected infos: %#v", page.ItemInfos)
	}
	if !page.HasMore {
		t.Fatal("expected hasMore true")
	}
}

func TestListUserPlaylistsPageUsesItemsAndTracksTotals(t *testing.T) {
	s := &Service{
		itemsHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/me/playlists" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
				}
				return httpJSONResponse(http.StatusOK, `{
					"items":[
						{"id":"p1","name":"P1","uri":"spotify:playlist:p1","owner":{"id":"u1","display_name":"U1"},"images":[],"collaborative":false,"items":{"total":5},"tracks":{"total":3}},
						{"id":"p2","name":"P2","uri":"spotify:playlist:p2","owner":{"id":"u2","display_name":"U2"},"images":[],"collaborative":true,"tracks":{"total":7}}
					],
					"next":null
				}`), nil
			}),
		},
	}

	page, err := s.ListUserPlaylistsPage(context.Background(), 0, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 playlists, got %d", len(page.Items))
	}
	if page.Items[0].TrackCount != 5 || page.Items[1].TrackCount != 7 {
		t.Fatalf("unexpected track counts: %d, %d", page.Items[0].TrackCount, page.Items[1].TrackCount)
	}
}

func TestListUserPlaylistsPageRequiresItemsHTTPClient(t *testing.T) {
	s := &Service{}
	_, err := s.ListUserPlaylistsPage(context.Background(), 0, 5)
	if err == nil || !strings.Contains(err.Error(), "items http client is not configured") {
		t.Fatalf("expected items http client error, got %v", err)
	}
}

func TestResolveContextImageURLPlaylist(t *testing.T) {
	s := &Service{
		itemsHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/playlists/pl/images" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
				}
				return httpJSONResponse(http.StatusOK, `[{"url":"https://i.scdn.co/image/p1"}]`), nil
			}),
		},
	}
	url, err := s.ResolveContextImageURL(context.Background(), ContextKindPlaylist, "pl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://i.scdn.co/image/p1" {
		t.Fatalf("unexpected URL: %q", url)
	}
}

func TestResolveContextImageURLAlbum(t *testing.T) {
	s := &Service{
		itemsHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/albums/alb" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
				}
				return httpJSONResponse(http.StatusOK, `{"images":[{"url":"https://i.scdn.co/image/a1"}]}`), nil
			}),
		},
	}
	url, err := s.ResolveContextImageURL(context.Background(), ContextKindAlbum, "alb")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://i.scdn.co/image/a1" {
		t.Fatalf("unexpected URL: %q", url)
	}
}
