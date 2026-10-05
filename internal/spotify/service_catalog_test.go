package spotify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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

func TestSavedTracksPreserveAllArtistsAndCover(t *testing.T) {
	s := &Service{itemsHTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/me/tracks" {
			t.Fatalf("unexpected saved tracks endpoint: %s", req.URL.Path)
		}
		return httpJSONResponse(http.StatusOK, `{
			"items": [{"track": {"id":"song", "name":"Saved song", "duration_ms":180000,
				"artists":[{"name":"zTokyo"},{"name":"Guest Singer"}],
				"album":{"images":[{"url":"cover-url", "width":640, "height":640}]}}}],
			"next":null
		}`), nil
	})}}
	page, err := s.ListSavedTracksPage(context.Background(), 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.ItemInfos) != 1 || page.ItemInfos[0].Artist != "zTokyo, Guest Singer" || page.ItemInfos[0].ImageURL != "cover-url" {
		t.Fatalf("saved song metadata lost: %#v", page)
	}
}

func TestRecentlyPlayedTracksParseTrackMetadata(t *testing.T) {
	s := &Service{itemsHTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/me/player/recently-played" || req.URL.Query().Get("limit") != "50" {
			t.Fatalf("unexpected recently played request: %s", req.URL.String())
		}
		return httpJSONResponse(http.StatusOK, `{
			"items": [{"track": {"id":"recent", "name":"Recent song", "duration_ms":180000,
				"artists":[{"name":"Artist One"},{"name":"Artist Two"}],
				"album":{"name":"Recent album", "images":[{"url":"recent-cover", "width":640, "height":640}]}}}]
		}`), nil
	})}}
	tracks, err := s.ListRecentlyPlayedTracks(context.Background(), 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].ID != "recent" || tracks[0].Artist != "Artist One, Artist Two" || tracks[0].Album != "Recent album" || tracks[0].ImageURL != "recent-cover" {
		t.Fatalf("recent track metadata lost: %#v", tracks)
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
						{"item":{"id":"track-2","name":"Song B","duration_ms":2000,"artists":[{"name":"Artist B"},{"name":"Guest Artist"}],"album":{"images":[{"url":"track-cover","width":640,"height":640}]}}}
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
	if len(page.ItemInfos) != 2 || page.ItemInfos[1].Artist != "Artist B, Guest Artist" || page.ItemInfos[1].ImageURL != "track-cover" {
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
				if req.Method != http.MethodGet || req.URL.Path != "/v1/albums" || req.URL.Query().Get("ids") != "alb" {
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
				}
				return httpJSONResponse(http.StatusOK, `{"albums":[{"id":"alb","images":[{"url":"https://i.scdn.co/image/a1"}]}]}`), nil
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

func TestPickDisplayImageURLPrefersMidSize(t *testing.T) {
	images := []PlaylistImage{
		{URL: "https://i.scdn.co/image/large", Width: 640, Height: 640},
		{URL: "https://i.scdn.co/image/mid", Width: 300, Height: 300},
		{URL: "https://i.scdn.co/image/small", Width: 64, Height: 64},
	}
	if got := pickDisplayImageURL(images); got != "https://i.scdn.co/image/mid" {
		t.Fatalf("expected mid-size image, got %q", got)
	}
}

func TestPickDisplayImageURLFallsThrough(t *testing.T) {
	if got := pickDisplayImageURL([]PlaylistImage{{URL: "https://i.scdn.co/image/only", Width: 640}}); got != "https://i.scdn.co/image/only" {
		t.Fatalf("expected single image fallthrough, got %q", got)
	}
	if got := pickDisplayImageURL([]PlaylistImage{{URL: "https://i.scdn.co/image/unsized"}}); got != "https://i.scdn.co/image/unsized" {
		t.Fatalf("expected unsized fallthrough, got %q", got)
	}
	if got := pickDisplayImageURL([]PlaylistImage{{URL: "https://i.scdn.co/image/tiny", Width: 64}}); got != "https://i.scdn.co/image/tiny" {
		t.Fatalf("expected tiny-only fallthrough, got %q", got)
	}
	if got := pickDisplayImageURL(nil); got != "" {
		t.Fatalf("expected empty result, got %q", got)
	}
}

func TestResolveAlbumImagesBatchesTwentyPerRequest(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	s := &Service{
		itemsHTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/albums" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.String())
					return httpJSONResponse(http.StatusBadRequest, `{}`), nil
				}
				mu.Lock()
				requests++
				mu.Unlock()
				ids := strings.Split(req.URL.Query().Get("ids"), ",")
				if len(ids) > albumBatchMaxIDs {
					t.Errorf("batch exceeds %d ids: %d", albumBatchMaxIDs, len(ids))
				}
				var sb strings.Builder
				sb.WriteString(`{"albums":[`)
				for i, id := range ids {
					if i > 0 {
						sb.WriteString(`,`)
					}
					fmt.Fprintf(&sb, `{"id":%q,"images":[{"url":%q,"width":640},{"url":%q,"width":300}]}`, id, "https://i.scdn.co/image/"+id+"/large", "https://i.scdn.co/image/"+id+"/mid")
				}
				sb.WriteString(`]}`)
				return httpJSONResponse(http.StatusOK, sb.String()), nil
			}),
		},
	}
	const total = 25
	type outcome struct {
		url string
		err error
	}
	results := make([]outcome, total)
	var resMu sync.Mutex
	var wg sync.WaitGroup
	for i := range total {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("album-%02d", i)
			url, err := s.ResolveContextImageURL(context.Background(), ContextKindAlbum, id)
			resMu.Lock()
			results[i] = outcome{url: url, err: err}
			resMu.Unlock()
		}(i)
	}
	wg.Wait()
	for i := range total {
		id := fmt.Sprintf("album-%02d", i)
		want := "https://i.scdn.co/image/" + id + "/mid"
		if results[i].err != nil {
			t.Fatalf("album %s error: %v", id, results[i].err)
		}
		if results[i].url != want {
			t.Fatalf("album %s: expected mid-size %q, got %q", id, want, results[i].url)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("expected 2 batched requests for 25 albums, got %d", requests)
	}
}
