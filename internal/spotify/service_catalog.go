package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	spotifyapi "github.com/zmb3/spotify/v2"
)

func (s *Service) ListUserPlaylists(ctx context.Context, max int) ([]PlaylistSummary, error) {
	if max <= 0 {
		max = 100
	}

	const pageSize = 50
	offset := 0
	out := make([]PlaylistSummary, 0, min(max, pageSize))
	for len(out) < max {
		limit := min(pageSize, max-len(out))
		page, err := s.ListUserPlaylistsPage(ctx, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == 0 {
			break
		}
		out = append(out, page.Items...)
		if !page.HasMore || page.NextOffset <= offset {
			break
		}
		offset = page.NextOffset
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
		imageURL := ""
		if len(pl.Images) > 0 {
			imageURL = pl.Images[0].URL
		}
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
		imageURL := ""
		if len(album.Images) > 0 {
			imageURL = album.Images[0].URL
		}
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
	out.HasMore = len(out.Items) >= limit
	return out, nil
}

func (s *Service) ListPlaylistItemIDs(ctx context.Context, playlistID string, max int) ([]string, error) {
	playlistID = strings.TrimSpace(playlistID)
	if playlistID == "" {
		return nil, errors.New("playlist ID must not be empty")
	}
	if max <= 0 {
		max = 500
	}

	const pageSize = 100
	offset := 0
	out := make([]string, 0, min(max, pageSize))
	for len(out) < max {
		limit := min(pageSize, max-len(out))
		page, err := s.ListPlaylistItemsPage(ctx, playlistID, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(page.ItemIDs) > 0 {
			out = append(out, page.ItemIDs...)
		}
		if !page.HasMore || page.NextOffset <= offset {
			break
		}
		offset = page.NextOffset
	}
	return out, nil
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
		qi := QueueItem{ID: entry.ID, Name: entry.Name, DurationMS: entry.DurationMS}
		if len(entry.Artists) > 0 {
			qi.Artist = entry.Artists[0].Name
		}
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
	defer resp.Body.Close()
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
	defer resp.Body.Close()
	var page playlistPageResponse
	if err := DecodeWebAPIJSON(resp, http.StatusOK, &page, func(status int, body string) error {
		return &httpStatusError{status: status, err: fmt.Errorf("playlists: %s", body)}
	}); err != nil {
		return nil, err
	}
	return &page, nil
}
