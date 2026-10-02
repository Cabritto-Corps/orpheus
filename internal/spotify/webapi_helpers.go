package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	spotifyapi "github.com/zmb3/spotify/v2"
)

type PlaylistTotalWire struct {
	Total int `json:"total"`
}

type PlaylistSummaryWire struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	URI           string            `json:"uri"`
	Collaborative bool              `json:"collaborative"`
	Owner         PlaylistOwnerWire `json:"owner"`
	Images        []PlaylistImage   `json:"images"`
	Tracks        PlaylistTotalWire `json:"tracks"`
	Items         PlaylistTotalWire `json:"items"`
}

type PlaylistOwnerWire struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type PlaylistImage struct {
	URL    string `json:"url"`
	Height int    `json:"height"`
	Width  int    `json:"width"`
}

const displayImageMinPixels = 250

func imagePixelSize(width, height int) int {
	if width > 0 {
		return width
	}
	return height
}

func sdkImagesToPlaylistImages(images []spotifyapi.Image) []PlaylistImage {
	converted := make([]PlaylistImage, 0, len(images))
	for _, img := range images {
		converted = append(converted, PlaylistImage{URL: img.URL, Height: int(img.Height), Width: int(img.Width)})
	}
	return converted
}

func pickDisplayImageURL(images []PlaylistImage) string {
	best := ""
	bestSize := 0
	fallback := ""
	fallbackSize := 0
	for _, img := range images {
		url := strings.TrimSpace(img.URL)
		if url == "" {
			continue
		}
		size := imagePixelSize(img.Width, img.Height)
		if size <= 0 {
			if fallback == "" {
				fallback = url
			}
			continue
		}
		if size >= displayImageMinPixels && (best == "" || size < bestSize) {
			best = url
			bestSize = size
		}
		if size > fallbackSize {
			fallback = url
			fallbackSize = size
		}
	}
	if best != "" {
		return best
	}
	return fallback
}

type PlaylistItemWire struct {
	ID         string `json:"id"`
	URI        string `json:"uri"`
	Name       string `json:"name"`
	DurationMS int    `json:"duration_ms"`
	Artists    []struct {
		Name string `json:"name"`
	} `json:"artists"`
}

type PlaylistEntryWire struct {
	Item  *PlaylistItemWire `json:"item"`
	Track *PlaylistItemWire `json:"track"`
}

func (e PlaylistEntryWire) ResolvedItem() *PlaylistItemWire {
	if e.Item != nil {
		return e.Item
	}
	return e.Track
}

func PlaylistCount(itemsTotal, tracksTotal int) int {
	if itemsTotal > tracksTotal {
		return itemsTotal
	}
	return tracksTotal
}

func DecodeWebAPIJSON(resp *http.Response, successStatus int, out any, statusErr func(int, string) error) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != successStatus {
		return statusErr(resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}
