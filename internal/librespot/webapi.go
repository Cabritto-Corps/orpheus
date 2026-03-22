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

func (c *playlistCatalog) CurrentUserID(ctx context.Context) (string, error) {
	resp, err := c.doWith429Retry(ctx, "GET", "v1/me", nil, nil)
	if err != nil {
		return "", fmt.Errorf("webapi me: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("webapi me: %d %s", resp.StatusCode, string(body))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode me: %w", err)
	}
	return out.ID, nil
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
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
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
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
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
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
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
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
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
	defer resp.Body.Close()
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
	for _, item := range raw.Items {
		entry := item.ResolvedItem()
		if entry == nil || entry.ID == "" {
			continue
		}
		out.ItemIDs = append(out.ItemIDs, entry.ID)
	}
	out.NextOffset = offset + len(raw.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}
