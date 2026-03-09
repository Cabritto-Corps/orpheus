package tui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/infra/ports"
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

func TestTabSwitchClampsTargetPaginationAndQueuesCoverLoad(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	playlists := make([]list.Item, 0, 24)
	for i := 0; i < 24; i++ {
		playlists = append(playlists, playlistItem{
			summary: spotify.PlaylistSummary{ID: fmt.Sprintf("p-%d", i), Name: fmt.Sprintf("playlist-%d", i), ImageURL: fmt.Sprintf("purl-%d", i)},
		})
	}
	albums := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "a-1", Kind: spotify.ContextKindAlbum, Name: "album-1", ImageURL: "aurl-1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "a-2", Kind: spotify.ContextKindAlbum, Name: "album-2", ImageURL: "aurl-2"}},
	}
	m.playlistList.SetItems(playlists)
	m.albumList.SetItems(albums)
	m.playlistList.Paginator.PerPage = 1
	m.playlistList.Paginator.Page = 20
	m.playlistList.Select(20)
	m.albumList.Paginator.PerPage = 1
	m.albumList.Paginator.Page = 20
	m.activeTab = tabPlaylists

	nextModel, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := nextModel.(model)
	if got.activeTab != tabAlbums {
		t.Fatal("expected tab switch to albums")
	}
	if got.albumList.Paginator.Page > 1 {
		t.Fatalf("expected album page to clamp within range, got %d", got.albumList.Paginator.Page)
	}
	sel, ok := got.selectedAlbum()
	if !ok {
		t.Fatal("expected album selection to remain valid after tab switch")
	}
	if !hasInflightURL(got.imgs, sel.summary.ImageURL) {
		t.Fatal("expected selected album cover to queue on tab switch even after page clamp")
	}
}

func TestImageCacheEvictsOldestImageAndItsRenderedCovers(t *testing.T) {
	cache := newImgCache()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))

	cache.setImage("u-0", img, 0, 0)
	cache.preRenderCovers("u-0", [][2]int{{8, 4}})

	for i := 1; i <= maxCachedImages; i++ {
		cache.setImage(fmt.Sprintf("u-%d", i), img, 0, 0)
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
	cache.protocol = imageProtocolNone
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	cache.setImage("u", img, 0, 0)

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
	if got.cover.imageRetryCount["u1"] != 1 {
		t.Fatalf("expected retry count 1, got %d", got.cover.imageRetryCount["u1"])
	}
	if got.cover.imageRetryToken["u1"] == 0 {
		t.Fatalf("expected retry token to be set")
	}
}

func TestHandleImageLoadedMsgForCurrentPlayerCoverForcesKittyRedraw(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.activeTab = tabPlayer
	m.imgs.protocol = imageProtocolKitty
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1"})
	got := nextModel.(model)
	if cmd != nil {
		t.Fatal("expected no follow-up command on successful image load")
	}
	if !got.imgs.kittyForceRedraw {
		t.Fatal("expected successful current player cover load to force kitty redraw")
	}
}

func TestHandleImageLoadedMsgForOtherURLDoesNotForceKittyRedraw(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.activeTab = tabPlayer
	m.imgs.protocol = imageProtocolKitty
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}

	nextModel, _ := m.handleImageLoadedMsg(imageLoadedMsg{url: "u2"})
	got := nextModel.(model)
	if got.imgs.kittyForceRedraw {
		t.Fatal("expected unrelated image load success not to force kitty redraw")
	}
}

func TestHandleImageLoadedMsgExhaustedRetriesQueuesMetadataResolveWhenURLStillReferenced(t *testing.T) {
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistsLoading = false
	m.cover.imageRetryCount["u1"] = imageLoadRetryMax
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
	})

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
	got := nextModel.(model)
	if cmd == nil {
		t.Fatal("expected metadata resolve command after retries exhausted for referenced URL")
	}
	if _, ok := got.cover.resolveInFlight[coverResolveKey(spotify.ContextKindPlaylist, "p1")]; !ok {
		t.Fatal("expected cover resolve to be queued for failed playlist image URL")
	}
}

func TestHandleImageLoadedMsgExhaustedRetriesSkipsMetadataRefreshWhenURLNotReferenced(t *testing.T) {
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistsLoading = false
	m.cover.imageRetryCount["u1"] = imageLoadRetryMax

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
	_ = nextModel.(model)
	if cmd != nil {
		t.Fatal("expected no metadata refresh command for unreferenced failed URL")
	}
}

func TestHandleImageRetryMsgSkipsStaleOrUnneededURL(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.cover.imageRetryToken["u-stale"] = 2

	nextModel, cmd := m.handleImageRetryMsg(imageRetryMsg{url: "u-stale", token: 1})
	if cmd != nil {
		t.Fatalf("expected stale retry token to be ignored")
	}
	got := nextModel.(model)
	if got.cover.imageRetryToken["u-stale"] != 2 {
		t.Fatalf("expected stale token state unchanged")
	}

	got.cover.imageRetryToken["u-drop"] = 1
	got.cover.imageRetryCount["u-drop"] = 2
	nextModel, cmd = got.handleImageRetryMsg(imageRetryMsg{url: "u-drop", token: 1})
	if cmd != nil {
		t.Fatalf("expected no retry command when URL is no longer needed")
	}
	got = nextModel.(model)
	if _, ok := got.cover.imageRetryToken["u-drop"]; ok {
		t.Fatalf("expected retry token cleanup for unneeded URL")
	}
	if _, ok := got.cover.imageRetryCount["u-drop"]; ok {
		t.Fatalf("expected retry count cleanup for unneeded URL")
	}
}

func TestNeedsImageURLIncludesWholeLibrary(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u-library"}},
	})
	if !m.needsImageURL("u-library") {
		t.Fatal("expected library image URL to be considered needed even when not selected")
	}
}

func TestQueueMissingLibraryImageResolvesCmdQueuesEmptyImageEntries(t *testing.T) {
	catalog := fakeCatalog{
		playlists: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
		albums: func(offset, limit int) (*spotify.PlaylistPage, error) {
			return &spotify.PlaylistPage{Offset: offset, Limit: limit, NextOffset: offset, HasMore: false}, nil
		},
	}
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", Kind: spotify.ContextKindPlaylist, ImageURL: ""}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", Kind: spotify.ContextKindPlaylist, ImageURL: "u2"}},
	})
	cmd := m.queueMissingLibraryImageResolvesCmd(4)
	if cmd == nil {
		t.Fatal("expected cover resolve command batch")
	}
	if _, ok := m.cover.resolveInFlight[coverResolveKey(spotify.ContextKindPlaylist, "p1")]; !ok {
		t.Fatal("expected missing-image playlist to be queued for resolve")
	}
}

func TestLoadLibraryCoversCmdQueuesAllUniqueLibraryImages(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", ImageURL: "u2"}},
	})
	m.albumList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "a1", Name: "album", Kind: spotify.ContextKindAlbum, ImageURL: "u2"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "a2", Name: "album2", Kind: spotify.ContextKindAlbum, ImageURL: "u3"}},
	})

	cmd := m.loadLibraryCoversCmd(0)
	if cmd == nil {
		t.Fatal("expected library cover preload command")
	}
	for _, url := range []string{"u1", "u2", "u3"} {
		if !hasInflightURL(m.imgs, url) {
			t.Fatalf("expected %s to be queued for preload", url)
		}
	}
}

func TestLoadLibraryCoversCmdRespectsLimit(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", ImageURL: "u2"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p3", Name: "three", ImageURL: "u3"}},
	})

	cmd := m.loadLibraryCoversCmd(2)
	if cmd == nil {
		t.Fatal("expected limited library cover preload command")
	}
	for _, url := range []string{"u1", "u2"} {
		if !hasInflightURL(m.imgs, url) {
			t.Fatalf("expected %s to be queued within limit", url)
		}
	}
	if hasInflightURL(m.imgs, "u3") {
		t.Fatal("expected third URL to remain unqueued when limit is reached")
	}
}

func TestHandleCoverImageResolvedMsgUpdatesItemAndQueuesImageLoad(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", Kind: spotify.ContextKindPlaylist, ImageURL: ""}},
	})
	key := coverResolveKey(spotify.ContextKindPlaylist, "p1")
	m.cover.resolveInFlight[key] = struct{}{}

	nextModel, cmd := m.handleCoverImageResolvedMsg(coverImageResolvedMsg{
		kind: spotify.ContextKindPlaylist,
		id:   "p1",
		url:  "u1",
	})
	got := nextModel.(model)
	if cmd == nil {
		t.Fatal("expected image load command after resolving image URL")
	}
	if _, ok := got.cover.resolveInFlight[key]; ok {
		t.Fatal("expected resolve inflight marker to be cleared")
	}
	if !hasInflightURL(got.imgs, "u1") {
		t.Fatal("expected resolved URL to be queued for image load")
	}
}

func TestApplyResolvedContextImageURLKeepsSelectionIndex(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", Kind: spotify.ContextKindPlaylist, ImageURL: ""}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", Kind: spotify.ContextKindPlaylist, ImageURL: "u2"}},
	})
	m.playlistList.Select(1)

	if !m.applyResolvedContextImageURL(spotify.ContextKindPlaylist, "p1", "u1") {
		t.Fatal("expected playlist image URL update")
	}
	sel, ok := m.selectedPlaylist()
	if !ok || sel.summary.ID != "p2" {
		t.Fatal("expected playlist selection to remain on previously selected item")
	}
}

func TestHandlePlaylistsMsgQueuesInitialPlaylistAndAlbumPreviewCover(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.playlistsLoading = true

	nextModel, _ := m.handlePlaylistsMsg(playlistsMsg{
		offset: 0,
		limit:  2,
		items: []spotify.PlaylistSummary{
			{ID: "p1", Name: "playlist", Kind: spotify.ContextKindPlaylist, ImageURL: "u-playlist"},
			{ID: "a1", Name: "album", Kind: spotify.ContextKindAlbum, ImageURL: "u-album"},
		},
		hasMore: false,
	})
	got := nextModel.(model)
	if !hasInflightURL(got.imgs, "u-playlist") {
		t.Fatal("expected startup selected playlist cover to queue image load")
	}
	if !hasInflightURL(got.imgs, "u-album") {
		t.Fatal("expected startup selected album cover to queue image load")
	}
}

func TestHandleTickMsgPlayerTabQueuesCurrentAlbumImageRefresh(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "player-u1"}
	m.playerCoverRefreshTick = playerCoverRefreshEvery - 1

	nextModel, _ := m.handleTickMsg()
	got := nextModel.(model)
	if !hasInflightURL(got.imgs, "player-u1") {
		t.Fatal("expected periodic player cover refresh to queue current album image")
	}
}

func TestCoverQueueDedupesAndDrains(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.enqueueCoverURL("u1")
	m.enqueueCoverURL("u1")
	if len(m.cover.queue) != 1 {
		t.Fatalf("expected deduped cover queue size 1, got %d", len(m.cover.queue))
	}
	cmd := m.drainCoverQueueCmd(4)
	if cmd == nil {
		t.Fatal("expected drain command")
	}
	if !hasInflightURL(m.imgs, "u1") {
		t.Fatal("expected queued URL to launch image load")
	}
}

func TestPlayerCoverFailuresFallbackFromKitty(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.imgs.protocol = imageProtocolKitty
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	for i := 0; i < kittyProtocolFallbackFailures; i++ {
		nextModel, _ := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
		m = nextModel.(model)
	}
	if m.imgs.protocol != imageProtocolNone {
		t.Fatal("expected kitty protocol to fallback to ansi after repeated player cover failures")
	}
}

func TestImageCacheBeginLoadDedupesInflightAndCached(t *testing.T) {
	cache := newImgCache()
	if !cache.beginLoad("u1") {
		t.Fatal("expected first beginLoad to start")
	}
	if cache.beginLoad("u1") {
		t.Fatal("expected inflight beginLoad to be deduped")
	}
	cache.finishLoad("u1")
	cache.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)), 0, 0)
	if cache.beginLoad("u1") {
		t.Fatal("expected cached beginLoad to be skipped")
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
	cache.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)), 0, 0)
	if cache.shouldQueueLoad("u1") {
		t.Fatal("expected cached URL to be skipped")
	}
}

func TestImageCacheShouldQueueLoadWhenKittyEncodedMissing(t *testing.T) {
	cache := newImgCache()
	cache.protocol = imageProtocolKitty
	cache.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)), 0, 0)

	cache.mu.Lock()
	delete(cache.encoded, "u1")
	cache.mu.Unlock()

	if !cache.shouldQueueLoad("u1") {
		t.Fatal("expected kitty URL to be queueable when encoded payload is missing")
	}
	if !cache.shouldQueuePriorityLoad("u1") {
		t.Fatal("expected kitty priority queue to include URLs missing encoded payload")
	}
}

func TestLoadImageCmdRepairsMissingKittyEncodingFromCachedImage(t *testing.T) {
	oldProvider := imageProvider
	defer func() { imageProvider = oldProvider }()
	imageProvider = imageProviderFunc(func(ctx context.Context, url string) ([]byte, error) {
		t.Fatal("expected cached image path to avoid provider fetch")
		return nil, nil
	})

	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.imgs.protocol = imageProtocolKitty
	m.imgs.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)), 0, 0)
	m.imgs.mu.Lock()
	delete(m.imgs.encoded, "u1")
	m.imgs.mu.Unlock()

	cmd := m.loadImageCmd("u1", false)
	if cmd == nil {
		t.Fatal("expected image load command for cached kitty image missing encoded payload")
	}
	msg := cmd()
	loaded, ok := msg.(imageLoadedMsg)
	if !ok || loaded.err != nil {
		t.Fatalf("expected successful imageLoadedMsg, got %#v", msg)
	}
	if strings.TrimSpace(m.imgs.encodedFor("u1")) == "" {
		t.Fatal("expected kitty encoded payload to be repaired from cached image")
	}
}

func TestLoadImageCmdWaitsForSemaphoreBeforeFetchTimeoutStarts(t *testing.T) {
	oldProvider := imageProvider
	oldSemaphore := imgSemaphore
	defer func() {
		imageProvider = oldProvider
		imgSemaphore = oldSemaphore
	}()

	imgSemaphore = make(chan struct{}, 1)
	imgSemaphore <- struct{}{}

	providerCalled := make(chan struct{}, 1)
	imageProvider = imageProviderFunc(func(ctx context.Context, url string) ([]byte, error) {
		providerCalled <- struct{}{}
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	})

	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	cmd := m.loadImageCmd("u1", false)
	if cmd == nil {
		t.Fatal("expected image load command")
	}

	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()

	select {
	case <-providerCalled:
		t.Fatal("expected fetch to wait for semaphore slot")
	case <-time.After(100 * time.Millisecond):
	}

	<-imgSemaphore

	select {
	case <-providerCalled:
	case <-time.After(time.Second):
		t.Fatal("expected fetch to start after semaphore release")
	}

	select {
	case msg := <-done:
		loaded, ok := msg.(imageLoadedMsg)
		if !ok || loaded.err != nil {
			t.Fatalf("expected successful imageLoadedMsg, got %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("expected command to finish")
	}
}

func TestKittyOverlayStateAvoidsRedrawWhenUnchanged(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first overlay render")
	}
	second := m.kittyOverlay()
	if second != "" {
		t.Fatalf("expected unchanged overlay to skip redraw, got %q", second)
	}
	m.imgs.forceKittyRedraw()
	forced := m.kittyOverlay()
	if forced == "" {
		t.Fatal("expected force redraw to emit overlay even when key is unchanged")
	}
	if forced == first {
		t.Fatal("expected force redraw payload to differ so renderer dedup cannot skip write")
	}
	m.imgs.resetKittyOverlayState()
	third := m.kittyOverlay()
	if third == "" {
		t.Fatal("expected overlay redraw after kitty state reset")
	}
}

func TestKittyOverlayDeletesOnceWhenImageDisappears(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	if m.kittyOverlay() == "" {
		t.Fatal("expected initial overlay render")
	}
	m.status.AlbumImageURL = ""

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, kittyDeleteAll) {
		t.Fatal("expected delete-all when image disappears")
	}
	if again := m.kittyOverlay(); again != "" {
		t.Fatalf("expected repeated empty state to avoid repeated delete, got %q", again)
	}
}

func TestKittyOverlayDeletesWhenHelpOpens(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	if m.kittyOverlay() == "" {
		t.Fatal("expected initial overlay render")
	}
	m.helpOpen = true

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, kittyDeleteAll) {
		t.Fatal("expected help modal to clear Kitty image")
	}
	if again := m.kittyOverlay(); !strings.Contains(again, kittyDeleteAll) {
		t.Fatalf("expected help-open state to keep emitting delete-all, got %q", again)
	}
}

func TestKittyOverlayPlayerClearsPreviousImageWhileNextLoads(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.status.AlbumImageURL = "u2"

	loading := m.kittyOverlay()
	if !strings.Contains(loading, kittyDeleteAll) {
		t.Fatal("expected stale player image to be cleared while next cover is loading")
	}
}

func TestKittyOverlayPlayerClearsWhileTransportTransitionPending(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.transportTransitionPending = true
	m.transportTransitionFromTrack = "track-1"
	m.status.AlbumImageURL = "u2"
	loading := m.kittyOverlay()
	if !strings.Contains(loading, kittyDeleteAll) {
		t.Fatal("expected kitty overlay to clear while transport transition is pending")
	}
}

func TestKittyOverlayPlayerDoesNotForceClearForSameCoverDuringTransition(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{TrackID: "track-2", AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="
	m.transportTransitionPending = true
	m.transportTransitionFromTrack = "track-1"

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, "ZmFrZQ==") {
		t.Fatal("expected kitty overlay to keep rendering for same cover during transition")
	}
}

func TestKittyOverlayResetStateNextLoadReturnsEmptyWhenNothingDisplayed(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.imgs.resetKittyOverlayState()
	m.status.AlbumImageURL = "u2"

	loading := m.kittyOverlay()
	if loading != "" {
		t.Fatalf("expected no overlay when reset and next url has no encoding and nothing was displayed, got %q", loading)
	}
}

func TestAlbumCoverPanelKittyShowsPlaceholderWhileLoading(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.imgs.protocol = imageProtocolKitty
	m.status = &spotify.PlaybackStatus{
		AlbumImageURL: "u-missing",
		TrackName:     "track",
		ArtistName:    "artist",
	}

	panel := m.albumCoverPanel(40, 20)
	if !strings.Contains(panel, "╭") {
		t.Fatal("expected kitty loading state to show placeholder like ansi")
	}
}

func TestAlbumCoverPanelAnsiShowsPlaceholderWhileLoading(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.imgs.protocol = imageProtocolNone
	m.status = &spotify.PlaybackStatus{
		AlbumImageURL: "u-missing",
		TrackName:     "track",
		ArtistName:    "artist",
	}

	panel := m.albumCoverPanel(40, 20)
	if !strings.Contains(panel, "╭") {
		t.Fatal("expected ansi loading state to keep placeholder box")
	}
}

func TestKittyOverlayPlayerSameCoverDifferentTrackStillRedraws(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{
		TrackID:       "track-1",
		TrackName:     "one",
		ArtistName:    "artist",
		AlbumImageURL: "u1",
	}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first kitty render")
	}
	m.status.TrackID = "track-2"
	m.status.TrackName = "two"

	second := m.kittyOverlay()
	if second == "" {
		t.Fatal("expected redraw for same image URL on track change")
	}
	if second == first {
		t.Fatal("expected redraw payload to differ for same-cover track transition")
	}
	if strings.Contains(second, kittyDeleteAll) {
		t.Fatal("expected same-cover redraw not to clear globally before drawing")
	}
}

func TestKittyOverlayPlayerEpochForcesRedrawWithSameKeyInputs(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{
		TrackID:       "track-1",
		TrackName:     "one",
		ArtistName:    "artist",
		AlbumImageURL: "u1",
	}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first kitty render")
	}
	if second := m.kittyOverlay(); second != "" {
		t.Fatalf("expected unchanged state to skip redraw, got %q", second)
	}
	m.playerCoverEpoch++
	third := m.kittyOverlay()
	if third == "" {
		t.Fatal("expected epoch increment to force kitty redraw even with same cover/subject")
	}
}

func TestKittyOverlaySameURLWithoutEncodingKeepsCurrentImage(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlayer
	m.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first kitty render")
	}
	delete(m.imgs.encoded, "u1")

	loading := m.kittyOverlay()
	if strings.Contains(loading, kittyDeleteAll) {
		t.Fatal("expected same-url missing encoding to keep current kitty image visible")
	}
}

func TestKittyOverlayClearsStaleImageOnTabSwitchWithoutEncodedCover(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.width = 120
	m.height = 40
	m.activeTab = tabPlaylists
	m.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
	})
	m.playlistList.Select(0)
	m.imgs.protocol = imageProtocolKitty
	m.imgs.encoded["u1"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial playlist kitty render")
	}

	m.activeTab = tabAlbums
	m.albumList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "a1", Name: "album", Kind: spotify.ContextKindAlbum, ImageURL: "u2"}},
	})
	m.albumList.Select(0)

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, kittyDeleteAll) {
		t.Fatal("expected stale kitty image to clear when switched tab cover is not yet encoded")
	}
}

func hasInflightURL(cache *imgCache, url string) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, ok := cache.inflight[url]
	return ok
}

type imageProviderFunc func(ctx context.Context, url string) ([]byte, error)

func (f imageProviderFunc) Fetch(ctx context.Context, url string) ([]byte, error) {
	return f(ctx, url)
}

var _ ports.ImageProvider = imageProviderFunc(nil)
