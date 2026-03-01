package librespot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/devgianlu/go-librespot/session"

	"orpheus/internal/spotify"
)

const (
	webApi429MaxRetries   = 2
	webApi429DefaultWait  = 5 * time.Second
	webApi429MinRemaining = 2 * time.Second
)

type playlistCatalog struct {
	sess *session.Session
}

func NewPlaylistCatalog(sess *session.Session) spotify.PlaylistCatalog {
	return &playlistCatalog{sess: sess}
}

func (c *playlistCatalog) doWith429Retry(ctx context.Context, method, path string, q url.Values, body []byte) (*http.Response, error) {
	var lastResp *http.Response
	for attempt := 0; attempt <= webApi429MaxRetries; attempt++ {
		if lastResp != nil {
			lastResp.Body.Close()
		}
		resp, err := c.sess.WebApi(ctx, method, path, q, nil, body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 429 {
			return resp, nil
		}
		lastResp = resp
		wait := webApi429DefaultWait
		if s := resp.Header.Get("Retry-After"); s != "" {
			if sec, err := strconv.Atoi(s); err == nil && sec > 0 && sec <= 60 {
				wait = time.Duration(sec) * time.Second
			}
		}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining <= webApi429MinRemaining {
				if lastResp != nil {
					lastResp.Body.Close()
				}
				return nil, fmt.Errorf("rate limited (429); not enough time to wait (Retry-After %v)", wait)
			}
			if wait > remaining-webApi429MinRemaining {
				wait = remaining - webApi429MinRemaining
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	if lastResp != nil {
		lastResp.Body.Close()
	}
	return nil, fmt.Errorf("webapi rate limit (429) after %d retries", webApi429MaxRetries+1)
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
		Items []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			URI           string `json:"uri"`
			Collaborative bool   `json:"collaborative"`
			Owner         struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"owner"`
			Images []struct {
				URL string `json:"url"`
			} `json:"images"`
			Tracks struct {
				Total int `json:"total"`
			} `json:"tracks"`
		} `json:"items"`
		Next *string `json:"next"`
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
			Owner:         pl.Owner.DisplayName,
			OwnerID:       pl.Owner.ID,
			Collaborative: pl.Collaborative,
			TrackCount:    pl.Tracks.Total,
			ImageURL:      imageURL,
		})
	}
	out.NextOffset = offset + len(out.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}

func (c *playlistCatalog) ListPlaylistTrackIDsPage(ctx context.Context, playlistID string, offset, limit int) (*spotify.PlaylistTrackPage, error) {
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
	path := "v1/playlists/" + url.PathEscape(playlistID) + "/tracks"
	resp, err := c.doWith429Retry(ctx, "GET", path, q, nil)
	if err != nil {
		return nil, fmt.Errorf("webapi playlist tracks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("webapi playlist tracks: %d %s", resp.StatusCode, string(body))
	}
	var raw struct {
		Items []struct {
			Track *struct {
				ID  string `json:"id"`
				URI string `json:"uri"`
			} `json:"track"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode playlist tracks: %w", err)
	}
	out := &spotify.PlaylistTrackPage{
		Offset: offset,
		Limit:  limit,
	}
	if len(raw.Items) == 0 {
		out.NextOffset = offset
		return out, nil
	}
	out.TrackIDs = make([]string, 0, len(raw.Items))
	for _, item := range raw.Items {
		if item.Track == nil || item.Track.ID == "" {
			continue
		}
		out.TrackIDs = append(out.TrackIDs, item.Track.ID)
	}
	out.NextOffset = offset + len(raw.Items)
	out.HasMore = raw.Next != nil && *raw.Next != ""
	return out, nil
}
