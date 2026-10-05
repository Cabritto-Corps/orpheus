package librespot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/elxgy/go-librespot/session"

	"orpheus/internal/spotify"
)

type playlistCatalog struct {
	sess *session.Session
}

func NewPlaylistCatalog(sess *session.Session) spotify.PlaylistCatalog {
	return &playlistCatalog{sess: sess}
}

func (c *playlistCatalog) doWith429Retry(ctx context.Context, method, path string, q url.Values, body []byte) (*http.Response, error) {
	return c.sess.WebApiWith429Retry(ctx, method, path, q, nil, body)
}

func (c *playlistCatalog) ListUserPlaylistsPage(ctx context.Context, offset, limit int) (*spotify.PlaylistPage, error) {
	if offset < 0 {
		return nil, fmt.Errorf("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	resp, err := c.doWith429Retry(ctx, "GET", "v1/me/playlists", q, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi playlists: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi playlists: %d %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Items []spotify.PlaylistSummaryWire `json:"items"`
		Next  *string                       `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode playlists: %w", err)
	}
	out := &spotify.PlaylistPage{
		Offset: offset,
		Limit:  limit,
	}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.Items = make([]spotify.PlaylistSummary, 0, len(raw.Items))
	for _, pl := range raw.Items {
		imageURL := ""
		if len(pl.Images) > 0 {
			imageURL = pl.Images[0].URL
		}
		out.Items = append(out.Items, spotify.PlaylistSummary{
			ID:            pl.ID,
			Name:          pl.Name,
			URI:           pl.URI,
			Kind:          spotify.ContextKindPlaylist,
			Owner:         pl.Owner.DisplayName,
			OwnerID:       pl.Owner.ID,
			Collaborative: pl.Collaborative,
			TrackCount:    spotify.PlaylistCount(pl.Items.Total, pl.Tracks.Total),
			ImageURL:      imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func (c *playlistCatalog) ListSavedAlbumsPage(ctx context.Context, offset, limit int) (*spotify.PlaylistPage, error) {
	if offset < 0 {
		return nil, fmt.Errorf("album offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}

	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	resp, err := c.doWith429Retry(ctx, "GET", "v1/me/albums", q, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi albums: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi albums: %d %s", resp.StatusCode, string(body))
	}

	var raw struct {
		Items []struct {
			Album struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				URI         string `json:"uri"`
				TotalTracks int    `json:"total_tracks"`
				Artists     []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Images []struct {
					URL string `json:"url"`
				} `json:"images"`
			} `json:"album"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode albums: %w", err)
	}

	out := &spotify.PlaylistPage{
		Offset: offset,
		Limit:  limit,
	}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.Items = make([]spotify.PlaylistSummary, 0, len(raw.Items))
	for _, item := range raw.Items {
		album := item.Album
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
		out.Items = append(out.Items, spotify.PlaylistSummary{
			ID:         album.ID,
			Name:       album.Name,
			URI:        album.URI,
			Kind:       spotify.ContextKindAlbum,
			Owner:      owner,
			TrackCount: album.TotalTracks,
			ImageURL:   imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func (c *playlistCatalog) ListSavedTracksPage(ctx context.Context, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	if offset < 0 {
		return nil, fmt.Errorf("track offset must be >= 0")
	}
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	resp, err := c.doWith429Retry(ctx, "GET", "v1/me/tracks", q, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi saved tracks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi saved tracks: %d %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Items []struct {
			Track struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				DurationMS int    `json:"duration_ms"`
				Artists    []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Images []struct {
						URL string `json:"url"`
					} `json:"images"`
				} `json:"album"`
			} `json:"track"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode saved tracks: %w", err)
	}
	out := &spotify.PlaylistItemsPage{Offset: offset, Limit: limit}
	for _, entry := range raw.Items {
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
		imageURL := ""
		if len(track.Album.Images) > 0 {
			imageURL = track.Album.Images[0].URL
		}
		out.ItemInfos = append(out.ItemInfos, spotify.QueueItem{
			ID: track.ID, Name: track.Name, Artist: strings.Join(artists, ", "),
			DurationMS: track.DurationMS, ImageURL: imageURL,
		})
	}
	out.NextOffset = offset + len(raw.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func (c *playlistCatalog) ListRecentlyPlayedTracks(ctx context.Context, limit int) ([]spotify.QueueItem, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	resp, err := c.doWith429Retry(ctx, "GET", "v1/me/player/recently-played", q, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi recently played tracks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw struct {
		Items []struct {
			Track struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				DurationMS int    `json:"duration_ms"`
				Artists    []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Images []struct {
						URL string `json:"url"`
					} `json:"images"`
				} `json:"album"`
			} `json:"track"`
		} `json:"items"`
	}
	if err := spotify.DecodeWebAPIJSON(resp, http.StatusOK, &raw, func(status int, body string) error {
		return fmt.Errorf("webapi recently played tracks: %d %s", status, body)
	}); err != nil {
		return nil, err
	}
	tracks := make([]spotify.QueueItem, 0, len(raw.Items))
	for _, entry := range raw.Items {
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
		imageURL := ""
		if len(track.Album.Images) > 0 {
			imageURL = track.Album.Images[0].URL
		}
		tracks = append(tracks, spotify.QueueItem{
			ID: track.ID, Name: track.Name, Artist: strings.Join(artists, ", "),
			DurationMS: track.DurationMS, ImageURL: imageURL,
		})
	}
	return tracks, nil
}

func (c *playlistCatalog) ResolveContextImageURL(ctx context.Context, kind, id string) (string, error) {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("context ID must not be empty")
	}
	switch kind {
	case spotify.ContextKindPlaylist:
		resp, err := c.doWith429Retry(ctx, "GET", "v1/playlists/"+url.PathEscape(id)+"/images", nil, nil)
		if err != nil {
			return "", fmt.Errorf("webapi playlist images: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return "", fmt.Errorf("webapi playlist images: %d %s", resp.StatusCode, string(body))
		}
		var images []spotify.PlaylistImage
		if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
			return "", fmt.Errorf("decode playlist images: %w", err)
		}
		if len(images) == 0 {
			return "", nil
		}
		return strings.TrimSpace(images[0].URL), nil
	case spotify.ContextKindAlbum:
		resp, err := c.doWith429Retry(ctx, "GET", "v1/albums/"+url.PathEscape(id), nil, nil)
		if err != nil {
			return "", fmt.Errorf("webapi album details: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return "", fmt.Errorf("webapi album details: %d %s", resp.StatusCode, string(body))
		}
		var album struct {
			Images []spotify.PlaylistImage `json:"images"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&album); err != nil {
			return "", fmt.Errorf("decode album details: %w", err)
		}
		if len(album.Images) == 0 {
			return "", nil
		}
		return strings.TrimSpace(album.Images[0].URL), nil
	default:
		return "", fmt.Errorf("unsupported context kind %q", kind)
	}
}

func (c *playlistCatalog) ListPlaylistItemsPage(ctx context.Context, playlistID string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	if playlistID == "" {
		return nil, fmt.Errorf("playlist ID must not be empty")
	}
	if offset < 0 {
		return nil, fmt.Errorf("playlist offset must be >= 0")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 100 {
		limit = 100
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	path := "v1/playlists/" + url.PathEscape(playlistID) + "/items"
	resp, err := c.doWith429Retry(ctx, "GET", path, q, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi playlist items: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw struct {
		Items []spotify.PlaylistEntryWire `json:"items"`
		Next  *string                     `json:"next"`
	}
	if err := spotify.DecodeWebAPIJSON(resp, http.StatusOK, &raw, func(status int, body string) error {
		return fmt.Errorf("webapi playlist items: %d %s", status, body)
	}); err != nil {
		return nil, err
	}
	out := &spotify.PlaylistItemsPage{
		Offset: offset,
		Limit:  limit,
	}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.ItemIDs = make([]string, 0, len(raw.Items))
	out.ItemInfos = make([]spotify.QueueItem, 0, len(raw.Items))
	for _, item := range raw.Items {
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
		imageURL := ""
		if len(entry.Album.Images) > 0 {
			imageURL = entry.Album.Images[0].URL
		}
		qi := spotify.QueueItem{ID: entry.ID, Name: entry.Name, Artist: strings.Join(artists, ", "), DurationMS: entry.DurationMS, ImageURL: imageURL}
		out.ItemIDs = append(out.ItemIDs, entry.ID)
		out.ItemInfos = append(out.ItemInfos, qi)
	}
	out.NextOffset = offset + len(raw.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func (c *playlistCatalog) ListAlbumTracksPage(ctx context.Context, albumID string, offset, limit int) (*spotify.PlaylistItemsPage, error) {
	albumID = strings.TrimSpace(albumID)
	if albumID == "" {
		return nil, fmt.Errorf("album ID must not be empty")
	}
	if offset < 0 {
		return nil, fmt.Errorf("album offset must be >= 0")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 50 {
		limit = 50
	}

	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	resp, err := c.doWith429Retry(ctx, "GET", "v1/albums/"+url.PathEscape(albumID)+"/tracks", q, nil)
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

	out := &spotify.PlaylistItemsPage{Offset: offset, Limit: limit}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.ItemIDs = make([]string, 0, len(raw.Items))
	out.ItemInfos = make([]spotify.QueueItem, 0, len(raw.Items))
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
		qi := spotify.QueueItem{ID: item.ID, Name: item.Name, Artist: strings.Join(artists, ", "), DurationMS: item.DurationMs}
		out.ItemIDs = append(out.ItemIDs, item.ID)
		out.ItemInfos = append(out.ItemInfos, qi)
	}
	out.NextOffset = offset + len(raw.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func (c *playlistCatalog) SearchPage(ctx context.Context, query string, offset, limit int) (*spotify.SearchPage, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search query must not be empty")
	}
	if offset < 0 {
		return nil, fmt.Errorf("search offset must be >= 0")
	}
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("type", "track,album,artist")
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	resp, err := c.doWith429Retry(ctx, "GET", "v1/search", q, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw struct {
		Tracks struct {
			Items []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				URI        string `json:"uri"`
				DurationMS int    `json:"duration_ms"`
				Artists    []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name   string `json:"name"`
					Images []struct {
						URL string `json:"url"`
					} `json:"images"`
				} `json:"album"`
			} `json:"items"`
			Next *string `json:"next"`
		} `json:"tracks"`
		Albums struct {
			Items []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				URI         string `json:"uri"`
				TotalTracks int    `json:"total_tracks"`
				Artists     []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Images []struct {
					URL string `json:"url"`
				} `json:"images"`
			} `json:"items"`
			Next *string `json:"next"`
		} `json:"albums"`
		Artists struct {
			Items []struct {
				ID     string   `json:"id"`
				Name   string   `json:"name"`
				URI    string   `json:"uri"`
				Genres []string `json:"genres"`
				Images []struct {
					URL string `json:"url"`
				} `json:"images"`
			} `json:"items"`
			Next *string `json:"next"`
		} `json:"artists"`
	}
	if err := spotify.DecodeWebAPIJSON(resp, http.StatusOK, &raw, func(status int, body string) error {
		return fmt.Errorf("webapi search: %d %s", status, body)
	}); err != nil {
		return nil, err
	}
	out := &spotify.SearchPage{Offset: offset, Limit: limit, NextOffset: offset + limit}
	for _, track := range raw.Tracks.Items {
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
		out.Items = append(out.Items, spotify.SearchResultItem{
			ID: track.ID, Name: track.Name, URI: track.URI,
			Kind: "track", Owner: strings.Join(artists, ", "),
			AlbumName: track.Album.Name, ImageURL: imageURL, DurationMS: track.DurationMS,
		})
	}
	for _, album := range raw.Albums.Items {
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
		out.Items = append(out.Items, spotify.SearchResultItem{
			ID: album.ID, Name: album.Name, URI: album.URI,
			Kind: "album", Owner: strings.Join(artists, ", "),
			ImageURL: imageURL, TrackCount: album.TotalTracks,
		})
	}
	for _, artist := range raw.Artists.Items {
		if artist.ID == "" || artist.URI == "" {
			continue
		}
		imageURL := ""
		if len(artist.Images) > 0 {
			imageURL = artist.Images[0].URL
		}
		out.Items = append(out.Items, spotify.SearchResultItem{
			ID: artist.ID, Name: artist.Name, URI: artist.URI,
			Kind: "artist", ImageURL: imageURL, Genres: artist.Genres,
		})
	}
	out.HasMore = (raw.Tracks.Next != nil && *raw.Tracks.Next != "") ||
		(raw.Albums.Next != nil && *raw.Albums.Next != "") ||
		(raw.Artists.Next != nil && *raw.Artists.Next != "")
	return out, nil
}
