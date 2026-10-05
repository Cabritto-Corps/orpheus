package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	spotifyapi "github.com/zmb3/spotify/v2"
)

const (
	albumImageBatchWindow  = 30 * time.Millisecond
	albumBatchMaxIDs       = 20
	albumBatchFetchTimeout = 15 * time.Second
)

type albumImageResult struct {
	url string
	err error
}

type albumImageBatcher struct {
	mu      sync.Mutex
	pending map[string][]chan albumImageResult
	timer   *time.Timer
	fetch   func(ctx context.Context, ids []string) (map[string]string, error)
}

func (s *Service) albumBatcher() *albumImageBatcher {
	s.albumBatchMu.Lock()
	defer s.albumBatchMu.Unlock()
	if s.albumBatch == nil {
		s.albumBatch = &albumImageBatcher{
			pending: make(map[string][]chan albumImageResult),
			fetch:   s.fetchAlbumImages,
		}
	}
	return s.albumBatch
}

func (s *Service) resolveAlbumImageBatched(ctx context.Context, id string) (string, error) {
	b := s.albumBatcher()
	ch := make(chan albumImageResult, 1)
	b.mu.Lock()
	b.pending[id] = append(b.pending[id], ch)
	if b.timer == nil {
		b.timer = time.AfterFunc(albumImageBatchWindow, b.flush)
	}
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		b.removeWaiter(id, ch)
		return "", ctx.Err()
	case r := <-ch:
		return r.url, r.err
	}
}

func (b *albumImageBatcher) removeWaiter(id string, ch chan albumImageResult) {
	b.mu.Lock()
	defer b.mu.Unlock()
	waiters := b.pending[id]
	for i, w := range waiters {
		if w == ch {
			waiters = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(waiters) == 0 {
		delete(b.pending, id)
	} else {
		b.pending[id] = waiters
	}
}

func (b *albumImageBatcher) flush() {
	b.mu.Lock()
	pending := b.pending
	b.pending = make(map[string][]chan albumImageResult)
	b.timer = nil
	b.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	results := make(map[string]string, len(ids))
	ctx, cancel := context.WithTimeout(context.Background(), albumBatchFetchTimeout)
	defer cancel()
	var fetchErr error
	for i := 0; i < len(ids); i += albumBatchMaxIDs {
		end := min(i+albumBatchMaxIDs, len(ids))
		got, err := b.fetch(ctx, ids[i:end])
		if err != nil {
			fetchErr = err
			break
		}
		maps.Copy(results, got)
	}
	for id, chans := range pending {
		r := albumImageResult{url: results[id], err: fetchErr}
		for _, ch := range chans {
			select {
			case ch <- r:
			default:
			}
		}
	}
}

func (s *Service) fetchAlbumImages(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	if s.itemsHTTPClient == nil {
		return nil, errors.New("items http client is not configured")
	}
	params := url.Values{}
	params.Set("ids", strings.Join(ids, ","))
	u := spotifyAPIBase + "albums?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.itemsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var payload struct {
		Albums []*struct {
			ID     string          `json:"id"`
			Images []PlaylistImage `json:"images"`
		} `json:"albums"`
	}
	if err := DecodeWebAPIJSON(resp, http.StatusOK, &payload, func(status int, body string) error {
		return &httpStatusError{status: status, err: fmt.Errorf("album details: %s", body)}
	}); err != nil {
		return nil, err
	}
	for _, album := range payload.Albums {
		if album == nil || album.ID == "" {
			continue
		}
		out[album.ID] = pickDisplayImageURL(album.Images)
	}
	return out, nil
}

func (s *Service) ListUserPlaylistsPage(ctx context.Context, offset, limit int) (*PlaylistPage, error) {
	if offset < 0 {
		return nil, errors.New("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}

	out := &PlaylistPage{
		Offset: offset,
		Limit:  limit,
	}
	page, err := apiCallWithRetry(ctx, func() (*playlistPageResponse, error) {
		return s.fetchPlaylistsViaHTTP(ctx, offset, limit)
	})
	if err != nil {
		return nil, fmt.Errorf("fetch user playlists: %w", err)
	}
	if page == nil || len(page.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.Items = make([]PlaylistSummary, 0, len(page.Items))
	for _, pl := range page.Items {
		imageURL := pickDisplayImageURL(pl.Images)
		out.Items = append(out.Items, PlaylistSummary{
			ID:            pl.ID,
			Name:          pl.Name,
			URI:           pl.URI,
			Kind:          ContextKindPlaylist,
			Owner:         pl.Owner.DisplayName,
			OwnerID:       pl.Owner.ID,
			Collaborative: pl.Collaborative,
			TrackCount:    PlaylistCount(pl.Items.Total, pl.Tracks.Total),
			ImageURL:      imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = page.Next != nil && *page.Next != ""
	return out, nil
}

func (s *Service) ListSavedAlbumsPage(ctx context.Context, offset, limit int) (*PlaylistPage, error) {
	if offset < 0 {
		return nil, errors.New("album offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}

	page, err := apiCallWithRetry(ctx, func() (*spotifyapi.SavedAlbumPage, error) {
		return s.client.CurrentUsersAlbums(ctx, spotifyapi.Limit(limit), spotifyapi.Offset(offset))
	})
	if err != nil {
		return nil, fmt.Errorf("fetch saved albums: %w", err)
	}

	out := &PlaylistPage{
		Offset: offset,
		Limit:  limit,
	}
	if page == nil || len(page.Albums) == 0 {
		out.NextOffset = offset
		return out, nil
	}

	out.Items = make([]PlaylistSummary, 0, len(page.Albums))
	for _, entry := range page.Albums {
		album := entry.FullAlbum
		if album.ID == "" || album.URI == "" {
			continue
		}
		imageURL := pickDisplayImageURL(sdkImagesToPlaylistImages(album.Images))
		artists := make([]string, 0, len(album.Artists))
		for _, a := range album.Artists {
			if name := strings.TrimSpace(a.Name); name != "" {
				artists = append(artists, name)
			}
		}
		owner := strings.Join(artists, ", ")
		if owner == "" {
			owner = "Unknown artist"
		}
		out.Items = append(out.Items, PlaylistSummary{
			ID:         string(album.ID),
			Name:       album.Name,
			URI:        string(album.URI),
			Kind:       ContextKindAlbum,
			Owner:      owner,
			TrackCount: int(album.TotalTracks),
			ImageURL:   imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = page.Next != ""
	return out, nil
}

func (s *Service) ListSavedTracksPage(ctx context.Context, offset, limit int) (*PlaylistItemsPage, error) {
	if offset < 0 {
		return nil, errors.New("track offset must be >= 0")
	}
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	if s.itemsHTTPClient == nil {
		return nil, errors.New("items http client is not configured")
	}
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spotifyAPIBase+"me/tracks?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.itemsHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch saved tracks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var page struct {
		Items []struct {
			Track struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				DurationMS int    `json:"duration_ms"`
				Artists    []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Images []PlaylistImage `json:"images"`
				} `json:"album"`
			} `json:"track"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := DecodeWebAPIJSON(resp, http.StatusOK, &page, func(status int, body string) error {
		return &httpStatusError{status: status, err: fmt.Errorf("saved tracks: %s", body)}
	}); err != nil {
		return nil, err
	}
	out := &PlaylistItemsPage{Offset: offset, Limit: limit}
	if len(page.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	for _, entry := range page.Items {
		track := entry.Track
		if track.ID == "" {
			continue
		}
		artists := make([]string, 0, len(track.Artists))
		for _, artist := range track.Artists {
			if name := strings.TrimSpace(artist.Name); name != "" {
				artists = append(artists, name)
			}
		}
		out.ItemIDs = append(out.ItemIDs, track.ID)
		out.ItemInfos = append(out.ItemInfos, QueueItem{
			ID: track.ID, Name: track.Name, Artist: strings.Join(artists, ", "),
			DurationMS: track.DurationMS, ImageURL: pickDisplayImageURL(track.Album.Images),
		})
	}
	out.NextOffset = offset + len(page.Items)
	out.HasMore = page.Next != nil && *page.Next != ""
	return out, nil
}

func (s *Service) ListRecentlyPlayedTracks(ctx context.Context, limit int) ([]QueueItem, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	if s.itemsHTTPClient == nil {
		return nil, errors.New("items http client is not configured")
	}
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spotifyAPIBase+"me/player/recently-played?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.itemsHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch recently played tracks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var page struct {
		Items []struct {
			Track struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				DurationMS int    `json:"duration_ms"`
				Artists    []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name   string          `json:"name"`
					Images []PlaylistImage `json:"images"`
				} `json:"album"`
			} `json:"track"`
		} `json:"items"`
	}
	if err := DecodeWebAPIJSON(resp, http.StatusOK, &page, func(status int, body string) error {
		return &httpStatusError{status: status, err: fmt.Errorf("recently played tracks: %s", body)}
	}); err != nil {
		return nil, err
	}
	tracks := make([]QueueItem, 0, len(page.Items))
	for _, entry := range page.Items {
		track := entry.Track
		if track.ID == "" {
			continue
		}
		artists := make([]string, 0, len(track.Artists))
		for _, artist := range track.Artists {
			if name := strings.TrimSpace(artist.Name); name != "" {
				artists = append(artists, name)
			}
		}
		tracks = append(tracks, QueueItem{
			ID: track.ID, Name: track.Name, Artist: strings.Join(artists, ", "),
			Album:      track.Album.Name,
			DurationMS: track.DurationMS, ImageURL: pickDisplayImageURL(track.Album.Images),
		})
	}
	return tracks, nil
}

func (s *Service) ResolveContextImageURL(ctx context.Context, kind, id string) (string, error) {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("context ID must not be empty")
	}
	if s.itemsHTTPClient == nil {
		return "", errors.New("items http client is not configured")
	}
	switch kind {
	case ContextKindPlaylist:
		u := spotifyAPIBase + "playlists/" + url.PathEscape(id) + "/images"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := s.itemsHTTPClient.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		var images []PlaylistImage
		if err := DecodeWebAPIJSON(resp, http.StatusOK, &images, func(status int, body string) error {
			return &httpStatusError{status: status, err: fmt.Errorf("playlist images: %s", body)}
		}); err != nil {
			return "", err
		}
		return pickDisplayImageURL(images), nil
	case ContextKindAlbum:
		return s.resolveAlbumImageBatched(ctx, id)
	default:
		return "", fmt.Errorf("unsupported context kind %q", kind)
	}
}

func (s *Service) ListPlaylistItemsPage(ctx context.Context, playlistID string, offset, limit int) (*PlaylistItemsPage, error) {
	playlistID = strings.TrimSpace(playlistID)
	if playlistID == "" {
		return nil, errors.New("playlist ID must not be empty")
	}
	if offset < 0 {
		return nil, errors.New("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}

	fetch := func() (*playlistItemsResponse, error) {
		return s.fetchPlaylistItemsViaItemsEndpoint(ctx, playlistID, offset, limit)
	}
	page, err := apiCallWithRetry(ctx, fetch)
	if err != nil {
		return nil, fmt.Errorf("fetch playlist items: %w", err)
	}

	out := &PlaylistItemsPage{
		Offset: offset,
		Limit:  limit,
	}
	if page == nil || len(page.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}

	out.ItemIDs = make([]string, 0, len(page.Items))
	out.ItemInfos = make([]QueueItem, 0, len(page.Items))
	for _, item := range page.Items {
		entry := item.ResolvedItem()
		if entry == nil || entry.ID == "" {
			continue
		}
		artists := make([]string, 0, len(entry.Artists))
		for _, artist := range entry.Artists {
			if name := strings.TrimSpace(artist.Name); name != "" {
				artists = append(artists, name)
			}
		}
		qi := QueueItem{ID: entry.ID, Name: entry.Name, Artist: strings.Join(artists, ", "), DurationMS: entry.DurationMS, ImageURL: pickDisplayImageURL(entry.Album.Images)}
		out.ItemIDs = append(out.ItemIDs, entry.ID)
		out.ItemInfos = append(out.ItemInfos, qi)
	}
	out.NextOffset = offset + len(page.Items)
	out.HasMore = page.Next != nil && *page.Next != ""
	return out, nil
}

func (s *Service) fetchPlaylistItemsViaItemsEndpoint(ctx context.Context, playlistID string, offset, limit int) (*playlistItemsResponse, error) {
	if s.itemsHTTPClient == nil {
		return nil, errors.New("items http client is not configured")
	}
	u := spotifyAPIBase + "playlists/" + url.PathEscape(playlistID) + "/items?"
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	u += params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.itemsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var page playlistItemsResponse
	if err := DecodeWebAPIJSON(resp, http.StatusOK, &page, func(status int, body string) error {
		return &httpStatusError{status: status, err: fmt.Errorf("playlist items: %s", body)}
	}); err != nil {
		return nil, err
	}
	return &page, nil
}

type playlistItemsResponse struct {
	Items []PlaylistEntryWire `json:"items"`
	Next  *string             `json:"next"`
}

type playlistPageResponse struct {
	Items []PlaylistSummaryWire `json:"items"`
	Next  *string               `json:"next"`
}

func (s *Service) fetchPlaylistsViaHTTP(ctx context.Context, offset, limit int) (*playlistPageResponse, error) {
	if s.itemsHTTPClient == nil {
		return nil, errors.New("items http client is not configured")
	}
	u := spotifyAPIBase + "me/playlists?"
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	u += params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.itemsHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var page playlistPageResponse
	if err := DecodeWebAPIJSON(resp, http.StatusOK, &page, func(status int, body string) error {
		return &httpStatusError{status: status, err: fmt.Errorf("playlists: %s", body)}
	}); err != nil {
		return nil, err
	}
	return &page, nil
}

func (s *Service) ListAlbumTracksPage(ctx context.Context, albumID string, offset, limit int) (*PlaylistItemsPage, error) {
	albumID = strings.TrimSpace(albumID)
	if albumID == "" {
		return nil, errors.New("album ID must not be empty")
	}
	if offset < 0 {
		return nil, errors.New("album offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}

	u := spotifyAPIBase + "albums/" + url.PathEscape(albumID) + "/tracks?"
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	u += params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.itemsHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webapi album tracks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi album tracks: %d %s", resp.StatusCode, string(body))
	}

	var raw struct {
		Items []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			DurationMs int    `json:"duration_ms"`
			Artists    []struct {
				Name string `json:"name"`
			} `json:"artists"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode album tracks: %w", err)
	}

	out := &PlaylistItemsPage{Offset: offset, Limit: limit}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.ItemIDs = make([]string, 0, len(raw.Items))
	out.ItemInfos = make([]QueueItem, 0, len(raw.Items))
	for _, item := range raw.Items {
		if item.ID == "" {
			continue
		}
		artists := make([]string, 0, len(item.Artists))
		for _, artist := range item.Artists {
			if name := strings.TrimSpace(artist.Name); name != "" {
				artists = append(artists, name)
			}
		}
		qi := QueueItem{ID: item.ID, Name: item.Name, Artist: strings.Join(artists, ", "), DurationMS: item.DurationMs}
		out.ItemIDs = append(out.ItemIDs, item.ID)
		out.ItemInfos = append(out.ItemInfos, qi)
	}
	out.NextOffset = offset + len(raw.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func (s *Service) SearchPage(ctx context.Context, query string, offset, limit int) (*SearchPage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query must not be empty")
	}
	if offset < 0 {
		return nil, errors.New("search offset must be >= 0")
	}
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	result, err := apiCallWithRetry(ctx, func() (*spotifyapi.SearchResult, error) {
		return s.client.Search(ctx, query, spotifyapi.SearchTypeTrack|spotifyapi.SearchTypeAlbum|spotifyapi.SearchTypeArtist,
			spotifyapi.Limit(limit), spotifyapi.Offset(offset))
	})
	if err != nil {
		return nil, fmt.Errorf("search Spotify catalog: %w", err)
	}
	out := &SearchPage{Offset: offset, Limit: limit, NextOffset: offset + limit}
	if result == nil {
		return out, nil
	}
	if result.Tracks != nil {
		out.HasMore = result.Tracks.Next != ""
		for _, track := range result.Tracks.Tracks {
			if track.ID == "" || track.URI == "" {
				continue
			}
			artists := make([]string, 0, len(track.Artists))
			for _, artist := range track.Artists {
				if name := strings.TrimSpace(artist.Name); name != "" {
					artists = append(artists, name)
				}
			}
			imageURL := ""
			if len(track.Album.Images) > 0 {
				imageURL = track.Album.Images[0].URL
			}
			out.Items = append(out.Items, SearchResultItem{
				ID: string(track.ID), Name: track.Name, URI: string(track.URI),
				Kind: "track", Owner: strings.Join(artists, ", "),
				AlbumName: track.Album.Name, ImageURL: imageURL, DurationMS: int(track.Duration),
			})
		}
	}
	if result.Albums != nil {
		out.HasMore = out.HasMore || result.Albums.Next != ""
		for _, album := range result.Albums.Albums {
			if album.ID == "" || album.URI == "" {
				continue
			}
			artists := make([]string, 0, len(album.Artists))
			for _, artist := range album.Artists {
				if name := strings.TrimSpace(artist.Name); name != "" {
					artists = append(artists, name)
				}
			}
			imageURL := ""
			if len(album.Images) > 0 {
				imageURL = album.Images[0].URL
			}
			out.Items = append(out.Items, SearchResultItem{
				ID: string(album.ID), Name: album.Name, URI: string(album.URI),
				Kind: "album", Owner: strings.Join(artists, ", "),
				ImageURL: imageURL, TrackCount: int(album.TotalTracks),
			})
		}
	}
	if result.Artists != nil {
		out.HasMore = out.HasMore || result.Artists.Next != ""
		for _, artist := range result.Artists.Artists {
			if artist.ID == "" || artist.URI == "" {
				continue
			}
			imageURL := ""
			if len(artist.Images) > 0 {
				imageURL = artist.Images[0].URL
			}
			out.Items = append(out.Items, SearchResultItem{
				ID: string(artist.ID), Name: artist.Name, URI: string(artist.URI),
				Kind: "artist", ImageURL: imageURL, Genres: append([]string(nil), artist.Genres...),
			})
		}
	}
	return out, nil
}
