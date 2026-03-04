package tui

import (
	"context"
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/spotify"
)

func TestHandlePlaylistKeyLoadsNewSelectedCoverImmediately(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	items := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "two", ImageURL: "u2"}},
	}
	m.playlistList.SetItems(items)
	m.playlistList.Select(0)

	nextModel, _ := m.handlePlaylistKey(tea.KeyMsg{Type: tea.KeyDown})
	got := nextModel.(model)
	if sel, ok := got.selectedPlaylist(); !ok || sel.summary.ImageURL != "u2" {
		t.Fatalf("expected selection to move to u2")
	}
	if !hasInflightURL(got.imgs, "u2") {
		t.Fatalf("expected immediate image load for new selection")
	}
}

func TestHandleAlbumKeyLoadsNewSelectedCoverImmediately(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	items := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "one", ImageURL: "u1", Kind: spotify.ContextKindAlbum}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "two", ImageURL: "u2", Kind: spotify.ContextKindAlbum}},
	}
	m.activeTab = tabAlbums
	m.albumList.SetItems(items)
	m.albumList.Select(0)

	nextModel, _ := m.handleAlbumKey(tea.KeyMsg{Type: tea.KeyDown})
	got := nextModel.(model)
	sel, ok := got.selectedAlbum()
	if !ok || sel.summary.ImageURL != "u2" {
		t.Fatalf("expected album selection to move to u2")
	}
	if !hasInflightURL(got.imgs, "u2") {
		t.Fatalf("expected immediate image load for new album selection")
	}
}

func TestImageCacheEvictsOldestImageAndItsRenderedCovers(t *testing.T) {
	cache := newImgCache()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))

	cache.setImage("u-0", img)
	cache.preRenderCovers("u-0", [][2]int{{8, 4}})

	for i := 1; i <= maxCachedImages; i++ {
		cache.setImage(fmt.Sprintf("u-%d", i), img)
	}

	if _, ok := cache.getImage("u-0"); ok {
		t.Fatalf("expected oldest image to be evicted")
	}

	cache.mu.RLock()
	_, hasCover := cache.covers.Peek(coverKey{url: "u-0", cols: 8, rows: 4})
	cache.mu.RUnlock()
	if hasCover {
		t.Fatalf("expected rendered covers for evicted image to be removed")
	}
}

func TestImageCacheEvictsOldestRenderedCover(t *testing.T) {
	cache := newImgCache()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	cache.setImage("u", img)

	sizes := make([][2]int, 0, maxCachedCoverRenders+1)
	for i := 0; i < maxCachedCoverRenders+1; i++ {
		sizes = append(sizes, [2]int{2 + i, 1})
	}
	cache.preRenderCovers("u", sizes)

	cache.mu.RLock()
	defer cache.mu.RUnlock()
	coverCount := len(cache.covers.Keys())
	if coverCount != maxCachedCoverRenders {
		t.Fatalf("expected rendered cover cache size %d, got %d", maxCachedCoverRenders, coverCount)
	}
	if _, ok := cache.covers.Peek(coverKey{url: "u", cols: 2, rows: 1}); ok {
		t.Fatalf("expected oldest rendered cover to be evicted")
	}
}

func TestHandleImageLoadedMsgSchedulesRetryOnError(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
	got := nextModel.(model)
	if cmd == nil {
		t.Fatalf("expected retry command on image load failure")
	}
	if got.imageRetryCount["u1"] != 1 {
		t.Fatalf("expected retry count 1, got %d", got.imageRetryCount["u1"])
	}
	if got.imageRetryToken["u1"] == 0 {
		t.Fatalf("expected retry token to be set")
	}
}

func TestHandleImageRetryMsgSkipsStaleOrUnneededURL(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.imageRetryToken["u-stale"] = 2

	nextModel, cmd := m.handleImageRetryMsg(imageRetryMsg{url: "u-stale", token: 1})
	if cmd != nil {
		t.Fatalf("expected stale retry token to be ignored")
	}
	got := nextModel.(model)
	if got.imageRetryToken["u-stale"] != 2 {
		t.Fatalf("expected stale token state unchanged")
	}

	got.imageRetryToken["u-drop"] = 1
	got.imageRetryCount["u-drop"] = 2
	nextModel, cmd = got.handleImageRetryMsg(imageRetryMsg{url: "u-drop", token: 1})
	if cmd != nil {
		t.Fatalf("expected no retry command when URL is no longer needed")
	}
	got = nextModel.(model)
	if _, ok := got.imageRetryToken["u-drop"]; ok {
		t.Fatalf("expected retry token cleanup for unneeded URL")
	}
	if _, ok := got.imageRetryCount["u-drop"]; ok {
		t.Fatalf("expected retry count cleanup for unneeded URL")
	}
}

func TestImageCacheBeginLoadStatsDedupesInflightAndCached(t *testing.T) {
	cache := newImgCache()
	if !cache.beginLoad("u1") {
		t.Fatal("expected first beginLoad to start")
	}
	if cache.beginLoad("u1") {
		t.Fatal("expected inflight beginLoad to be deduped")
	}
	cache.finishLoad("u1")
	cache.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if cache.beginLoad("u1") {
		t.Fatal("expected cached beginLoad to be skipped")
	}
	stats := cache.snapshotStats()
	if stats.loadStarted != 1 || stats.loadSkipInflight != 1 || stats.loadSkipCached != 1 {
		t.Fatalf("unexpected cache load stats: %+v", stats)
	}
}

func TestImageCacheShouldQueueLoad(t *testing.T) {
	cache := newImgCache()
	if !cache.shouldQueueLoad("u1") {
		t.Fatal("expected fresh URL to be queueable")
	}
	if !cache.beginLoad("u1") {
		t.Fatal("expected beginLoad to start")
	}
	if cache.shouldQueueLoad("u1") {
		t.Fatal("expected inflight URL to be skipped")
	}
	cache.finishLoad("u1")
	cache.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if cache.shouldQueueLoad("u1") {
		t.Fatal("expected cached URL to be skipped")
	}
}

func hasInflightURL(cache *imgCache, url string) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, ok := cache.inflight[url]
	return ok
}
