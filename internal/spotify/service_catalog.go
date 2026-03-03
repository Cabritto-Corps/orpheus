package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	page, err := apiCallWithRetry(ctx, func() (*spotifyapi.SimplePlaylistPage, error) {
		return s.client.CurrentUsersPlaylists(ctx, spotifyapi.Limit(limit), spotifyapi.Offset(offset))
	})
	if err != nil {
		return nil, fmt.Errorf("fetch user playlists: %w", err)
	}

	out := &PlaylistPage{
		Offset: offset,
		Limit:  limit,
	}
	if page == nil || len(page.Playlists) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.Items = make([]PlaylistSummary, 0, len(page.Playlists))
	for _, pl := range page.Playlists {
		imageURL := ""
		if len(pl.Images) > 0 {
			imageURL = pl.Images[0].URL
		}
		out.Items = append(out.Items, PlaylistSummary{
			ID:            string(pl.ID),
			Name:          pl.Name,
			URI:           string(pl.URI),
			Kind:          ContextKindPlaylist,
			Owner:         pl.Owner.DisplayName,
			OwnerID:       pl.Owner.ID,
			Collaborative: pl.Collaborative,
			TrackCount:    int(pl.Tracks.Total),
			ImageURL:      imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = len(out.Items) >= limit
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

func (s *Service) ListPlaylistTrackIDs(ctx context.Context, playlistID string, max int) ([]string, error) {
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
		page, err := s.ListPlaylistTrackIDsPage(ctx, playlistID, offset, limit)
		if err != nil {
			return nil, err
		}
		if len(page.TrackIDs) > 0 {
			out = append(out, page.TrackIDs...)
		}
		if !page.HasMore || page.NextOffset <= offset {
			break
		}
		offset = page.NextOffset
	}
	return out, nil
}

func (s *Service) ListPlaylistTrackIDsPage(ctx context.Context, playlistID string, offset, limit int) (*PlaylistTrackPage, error) {
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

	fetch := func() (*spotifyapi.PlaylistItemPage, error) {
		if s.itemsHTTPClient != nil {
			return s.fetchPlaylistItemsViaItemsEndpoint(ctx, playlistID, offset, limit)
		}
		return s.client.GetPlaylistItems(ctx, spotifyapi.ID(playlistID), spotifyapi.Limit(limit), spotifyapi.Offset(offset))
	}
	page, err := apiCallWithRetry(ctx, fetch)
	if err != nil {
		return nil, fmt.Errorf("fetch playlist tracks: %w", err)
	}

	out := &PlaylistTrackPage{
		Offset: offset,
		Limit:  limit,
	}
	if page == nil || len(page.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}

	out.TrackIDs = make([]string, 0, len(page.Items))
	out.TrackInfos = make([]QueueItem, 0, len(page.Items))
	for _, item := range page.Items {
		if item.Track.Track == nil || item.Track.Track.ID == "" {
			continue
		}
		t := item.Track.Track
		qi := QueueItem{ID: string(t.ID), Name: t.Name, DurationMS: int(t.Duration)}
		if len(t.Artists) > 0 {
			qi.Artist = t.Artists[0].Name
		}
		out.TrackIDs = append(out.TrackIDs, string(t.ID))
		out.TrackInfos = append(out.TrackInfos, qi)
	}
	out.NextOffset = offset + len(page.Items)
	out.HasMore = len(page.Items) >= limit
	return out, nil
}

func (s *Service) fetchPlaylistItemsViaItemsEndpoint(ctx context.Context, playlistID string, offset, limit int) (*spotifyapi.PlaylistItemPage, error) {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{status: resp.StatusCode, err: fmt.Errorf("playlist items: %s", strings.TrimSpace(string(body)))}
	}
	var page spotifyapi.PlaylistItemPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decode playlist items: %w", err)
	}
	return &page, nil
}
