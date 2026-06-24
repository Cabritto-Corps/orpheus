package tui

import (
	"context"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

func NewLoaderModel() model {
	return newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), nil)))
}

func TestHandlePlaylistKeyLoadsNewSelectedCoverImmediately(t *testing.T) {
	m := NewLoaderModel()
	items := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "two", ImageURL: "u2"}},
	}
	m.browse.playlistList.SetItems(items)
	m.browse.playlistList.Select(0)

	nextModel, _ := m.handlePlaylistKey(tea.KeyMsg{Type: tea.KeyDown})
	got := nextModel.(model)
	if sel, ok := got.selectedPlaylist(); !ok || sel.summary.ImageURL != "u2" {
		t.Fatalf("expected selection to move to u2")
	}
	if !hasInflightURL(got.ui.imgs, "u2") {
		t.Fatalf("expected immediate image load for new selection")
	}
}

func TestHandleAlbumKeyLoadsNewSelectedCoverImmediately(t *testing.T) {
	m := NewLoaderModel()
	items := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "one", ImageURL: "u1", Kind: spotify.ContextKindAlbum}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "two", ImageURL: "u2", Kind: spotify.ContextKindAlbum}},
	}
	m.ui.activeTab = tabAlbums
	m.browse.albumList.SetItems(items)
	m.browse.albumList.Select(0)

	nextModel, _ := m.handleAlbumKey(tea.KeyMsg{Type: tea.KeyDown})
	got := nextModel.(model)
	sel, ok := got.selectedAlbum()
	if !ok || sel.summary.ImageURL != "u2" {
		t.Fatalf("expected album selection to move to u2")
	}
	if !hasInflightURL(got.ui.imgs, "u2") {
		t.Fatalf("expected immediate image load for new album selection")
	}
}

func TestTabSwitchClampsTargetPaginationAndQueuesCoverLoad(t *testing.T) {
	m := NewLoaderModel()
	playlists := make([]list.Item, 0, 24)
	for i := range 24 {
		playlists = append(playlists, playlistItem{
			summary: spotify.PlaylistSummary{ID: fmt.Sprintf("p-%d", i), Name: fmt.Sprintf("playlist-%d", i), ImageURL: fmt.Sprintf("purl-%d", i)},
		})
	}
	albums := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "a-1", Kind: spotify.ContextKindAlbum, Name: "album-1", ImageURL: "aurl-1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "a-2", Kind: spotify.ContextKindAlbum, Name: "album-2", ImageURL: "aurl-2"}},
	}
	m.browse.playlistList.SetItems(playlists)
	m.browse.albumList.SetItems(albums)
	m.browse.playlistList.Paginator.PerPage = 1
	m.browse.playlistList.Paginator.Page = 20
	m.browse.playlistList.Select(20)
	m.browse.albumList.Paginator.PerPage = 1
	m.browse.albumList.Paginator.Page = 20
	m.ui.activeTab = tabPlaylists

	nextModel, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := nextModel.(model)
	if got.ui.activeTab != tabAlbums {
		t.Fatal("expected tab switch to albums")
	}
	if got.browse.albumList.Paginator.Page > 1 {
		t.Fatalf("expected album page to clamp within range, got %d", got.browse.albumList.Paginator.Page)
	}
	sel, ok := got.selectedAlbum()
	if !ok {
		t.Fatal("expected album selection to remain valid after tab switch")
	}
	if !hasInflightURL(got.ui.imgs, sel.summary.ImageURL) {
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
	for i := range maxCachedCoverRenders + 1 {
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
	m := NewLoaderModel()

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
	got := nextModel.(model)
	if cmd == nil {
		t.Fatalf("expected retry command on image load failure")
	}
	if got.ui.cover.imageRetryCount["u1"] != 1 {
		t.Fatalf("expected retry count 1, got %d", got.ui.cover.imageRetryCount["u1"])
	}
	if got.ui.cover.imageRetryToken["u1"] == 0 {
		t.Fatalf("expected retry token to be set")
	}
}

func TestHandleImageLoadedMsgForCurrentPlayerCoverForcesKittyRedraw(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabPlayer
	m.ui.imgs.protocol = imageProtocolKitty
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1"})
	got := nextModel.(model)
	if cmd != nil {
		t.Fatal("expected no follow-up command on successful image load")
	}
	if !got.ui.imgs.kittyForceRedraw {
		t.Fatal("expected successful current player cover load to force kitty redraw")
	}
}

func TestHandleImageLoadedMsgForOtherURLDoesNotForceKittyRedraw(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabPlayer
	m.ui.imgs.protocol = imageProtocolKitty
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}

	nextModel, _ := m.handleImageLoadedMsg(imageLoadedMsg{url: "u2"})
	got := nextModel.(model)
	if got.ui.imgs.kittyForceRedraw {
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
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), catalog)))
	m.browse.playlistsLoading = false
	m.ui.cover.imageRetryCount["u1"] = imageLoadRetryMax
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
	})

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
	got := nextModel.(model)
	if cmd == nil {
		t.Fatal("expected metadata resolve command after retries exhausted for referenced URL")
	}
	if _, ok := got.ui.cover.resolveInFlight[coverResolveKey(spotify.ContextKindPlaylist, "p1")]; !ok {
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
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), catalog)))
	m.browse.playlistsLoading = false
	m.ui.cover.imageRetryCount["u1"] = imageLoadRetryMax

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
	_ = nextModel.(model)
	if cmd != nil {
		t.Fatal("expected no metadata refresh command for unreferenced failed URL")
	}
}

func TestHandleImageRetryMsgSkipsStaleOrUnneededURL(t *testing.T) {
	m := NewLoaderModel()
	m.ui.cover.imageRetryToken["u-stale"] = 2

	nextModel, cmd := m.handleImageRetryMsg(imageRetryMsg{url: "u-stale", token: 1})
	if cmd != nil {
		t.Fatalf("expected stale retry token to be ignored")
	}
	got := nextModel.(model)
	if got.ui.cover.imageRetryToken["u-stale"] != 2 {
		t.Fatalf("expected stale token state unchanged")
	}

	got.ui.cover.imageRetryToken["u-drop"] = 1
	got.ui.cover.imageRetryCount["u-drop"] = 2
	nextModel, cmd = got.handleImageRetryMsg(imageRetryMsg{url: "u-drop", token: 1})
	if cmd != nil {
		t.Fatalf("expected no retry command when URL is no longer needed")
	}
	got = nextModel.(model)
	if _, ok := got.ui.cover.imageRetryToken["u-drop"]; ok {
		t.Fatalf("expected retry token cleanup for unneeded URL")
	}
	if _, ok := got.ui.cover.imageRetryCount["u-drop"]; ok {
		t.Fatalf("expected retry count cleanup for unneeded URL")
	}
}

func TestNeedsImageURLIncludesWholeLibrary(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems([]list.Item{
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
	m := newModel(context.Background(), catalog, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), catalog)))
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", Kind: spotify.ContextKindPlaylist, ImageURL: ""}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", Kind: spotify.ContextKindPlaylist, ImageURL: "u2"}},
	})
	cmd := m.queueMissingLibraryImageResolvesCmd(4)
	if cmd == nil {
		t.Fatal("expected cover resolve command batch")
	}
	if _, ok := m.ui.cover.resolveInFlight[coverResolveKey(spotify.ContextKindPlaylist, "p1")]; !ok {
		t.Fatal("expected missing-image playlist to be queued for resolve")
	}
}

func TestLoadLibraryCoversCmdQueuesAllUniqueLibraryImages(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", ImageURL: "u2"}},
	})
	m.browse.albumList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "a1", Name: "album", Kind: spotify.ContextKindAlbum, ImageURL: "u2"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "a2", Name: "album2", Kind: spotify.ContextKindAlbum, ImageURL: "u3"}},
	})

	cmd := m.loadLibraryCoversCmd(0)
	if cmd == nil {
		t.Fatal("expected library cover preload command")
	}
	for _, url := range []string{"u1", "u2", "u3"} {
		if !hasInflightURL(m.ui.imgs, url) {
			t.Fatalf("expected %s to be queued for preload", url)
		}
	}
}

func TestLoadLibraryCoversCmdRespectsLimit(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", ImageURL: "u2"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p3", Name: "three", ImageURL: "u3"}},
	})

	cmd := m.loadLibraryCoversCmd(2)
	if cmd == nil {
		t.Fatal("expected limited library cover preload command")
	}
	for _, url := range []string{"u1", "u2"} {
		if !hasInflightURL(m.ui.imgs, url) {
			t.Fatalf("expected %s to be queued within limit", url)
		}
	}
	if hasInflightURL(m.ui.imgs, "u3") {
		t.Fatal("expected third URL to remain unqueued when limit is reached")
	}
}

func TestHandleCoverImageResolvedMsgUpdatesItemAndQueuesImageLoad(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", Kind: spotify.ContextKindPlaylist, ImageURL: ""}},
	})
	key := coverResolveKey(spotify.ContextKindPlaylist, "p1")
	m.ui.cover.resolveInFlight[key] = struct{}{}

	nextModel, cmd := m.handleCoverImageResolvedMsg(coverImageResolvedMsg{
		kind: spotify.ContextKindPlaylist,
		id:   "p1",
		url:  "u1",
	})
	got := nextModel.(model)
	if cmd == nil {
		t.Fatal("expected image load command after resolving image URL")
	}
	if _, ok := got.ui.cover.resolveInFlight[key]; ok {
		t.Fatal("expected resolve inflight marker to be cleared")
	}
	if !hasInflightURL(got.ui.imgs, "u1") {
		t.Fatal("expected resolved URL to be queued for image load")
	}
}

func TestApplyResolvedContextImageURLKeepsSelectionIndex(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", Kind: spotify.ContextKindPlaylist, ImageURL: ""}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "p2", Name: "two", Kind: spotify.ContextKindPlaylist, ImageURL: "u2"}},
	})
	m.browse.playlistList.Select(1)

	if !m.applyResolvedContextImageURL(spotify.ContextKindPlaylist, "p1", "u1") {
		t.Fatal("expected playlist image URL update")
	}
	sel, ok := m.selectedPlaylist()
	if !ok || sel.summary.ID != "p2" {
		t.Fatal("expected playlist selection to remain on previously selected item")
	}
}

func TestHandlePlaylistsMsgQueuesInitialPlaylistAndAlbumPreviewCover(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistsLoading = true

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
	if !hasInflightURL(got.ui.imgs, "u-playlist") {
		t.Fatal("expected startup selected playlist cover to queue image load")
	}
	if !hasInflightURL(got.ui.imgs, "u-album") {
		t.Fatal("expected startup selected album cover to queue image load")
	}
}

func TestHandleTickMsgPlayerTabQueuesCurrentAlbumImageRefresh(t *testing.T) {
	m := NewLoaderModel()
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "player-u1"}
	m.ui.playerCoverRefreshTick = playerCoverRefreshEvery - 1

	nextModel, _ := m.handleTickMsg()
	got := nextModel.(model)
	if !hasInflightURL(got.ui.imgs, "player-u1") {
		t.Fatal("expected periodic player cover refresh to queue current album image")
	}
}

func TestCoverQueueDedupesAndDrains(t *testing.T) {
	m := NewLoaderModel()
	m.enqueueCoverURL("u1")
	m.enqueueCoverURL("u1")
	if len(m.ui.cover.queue) != 1 {
		t.Fatalf("expected deduped cover queue size 1, got %d", len(m.ui.cover.queue))
	}
	cmd := m.drainCoverQueueCmd(4)
	if cmd == nil {
		t.Fatal("expected drain command")
	}
	if !hasInflightURL(m.ui.imgs, "u1") {
		t.Fatal("expected queued URL to launch image load")
	}
}

func TestPlayerCoverFailuresFallbackFromKitty(t *testing.T) {
	m := NewLoaderModel()
	m.ui.imgs.protocol = imageProtocolKitty
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	for range kittyProtocolFallbackFailures {
		nextModel, _ := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1", err: fmt.Errorf("network")})
		m = nextModel.(model)
	}
	if m.ui.imgs.protocol != imageProtocolNone {
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
	m := NewLoaderModel()
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)), 0, 0)
	m.ui.imgs.mu.Lock()
	delete(m.ui.imgs.encoded, "u1")
	m.ui.imgs.mu.Unlock()

	cmd := m.loadImageCmd("u1", false)
	if cmd == nil {
		t.Fatal("expected image load command for cached kitty image missing encoded payload")
	}
	msg := cmd()
	loaded, ok := msg.(imageLoadedMsg)
	if !ok || loaded.err != nil {
		t.Fatalf("expected successful imageLoadedMsg, got %#v", msg)
	}
	if strings.TrimSpace(m.ui.imgs.encodedFor("u1")) == "" {
		t.Fatal("expected kitty encoded payload to be repaired from cached image")
	}
}

func TestKittyOverlayStateAvoidsRedrawWhenUnchanged(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first overlay render")
	}
	second := m.kittyOverlay()
	if second != "" {
		t.Fatalf("expected unchanged overlay to skip redraw, got %q", second)
	}
	m.ui.imgs.forceKittyRedraw()
	forced := m.kittyOverlay()
	if forced == "" {
		t.Fatal("expected force redraw to emit overlay even when key is unchanged")
	}
	if forced == first {
		t.Fatal("expected force redraw payload to differ so renderer dedup cannot skip write")
	}
	m.ui.imgs.resetKittyOverlayState()
	third := m.kittyOverlay()
	if third == "" {
		t.Fatal("expected overlay redraw after kitty state reset")
	}
}

func TestKittyOverlayDeletesOnceWhenImageDisappears(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if m.kittyOverlay() == "" {
		t.Fatal("expected initial overlay render")
	}
	m.transport.status.AlbumImageURL = ""

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, kittyDeleteAll) {
		t.Fatal("expected delete-all when image disappears")
	}
	if again := m.kittyOverlay(); again != "" {
		t.Fatalf("expected repeated empty state to avoid repeated delete, got %q", again)
	}
}

func TestKittyOverlayDeletesWhenHelpOpens(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if m.kittyOverlay() == "" {
		t.Fatal("expected initial overlay render")
	}
	m.ui.helpOpen = true

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, kittyDeleteAll) {
		t.Fatal("expected help modal to clear Kitty image")
	}
	if again := m.kittyOverlay(); !strings.Contains(again, kittyDeleteAll) {
		t.Fatalf("expected help-open state to keep emitting delete-all, got %q", again)
	}
}

func TestKittyOverlayPlayerClearsPreviousImageWhileNextLoads(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.transport.status.AlbumImageURL = "u2"

	loading := m.kittyOverlay()
	if !strings.Contains(loading, kittyDeleteAll) {
		t.Fatal("expected stale player image to be cleared while next cover is loading")
	}
}

func TestKittyOverlayPlayerClearsWhileTransportTransitionPending(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.transport.transition.Begin(time.Now(), "track-1")
	m.transport.status.AlbumImageURL = "u2"
	loading := m.kittyOverlay()
	if !strings.Contains(loading, kittyDeleteAll) {
		t.Fatal("expected kitty overlay to clear while transport transition is pending")
	}
}

func TestKittyOverlayPlayerDoesNotForceClearForSameCoverDuringTransition(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-2", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	m.transport.transition.Begin(time.Now(), "track-1")

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, "ZmFrZQ==") {
		t.Fatal("expected kitty overlay to keep rendering for same cover during transition")
	}
}

func TestKittyOverlayResetStateNextLoadReturnsEmptyWhenNothingDisplayed(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.ui.imgs.resetKittyOverlayState()
	m.transport.status.AlbumImageURL = "u2"

	loading := m.kittyOverlay()
	if loading != "" {
		t.Fatalf("expected no overlay when reset and next url has no encoding and nothing was displayed, got %q", loading)
	}
}

func TestAlbumCoverPanelKittyShowsPlaceholderWhileLoading(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.imgs.protocol = imageProtocolKitty
	m.transport.status = &spotify.PlaybackStatus{
		AlbumImageURL: "u-missing",
		TrackName:     "track",
		ArtistName:    "artist",
	}

	panel := m.albumCoverPanel(40, 20, 30, 15)
	if !strings.Contains(panel, "╭") {
		t.Fatal("expected kitty loading state to show placeholder like ansi")
	}
}

func TestAlbumCoverPanelAnsiShowsPlaceholderWhileLoading(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.imgs.protocol = imageProtocolNone
	m.transport.status = &spotify.PlaybackStatus{
		AlbumImageURL: "u-missing",
		TrackName:     "track",
		ArtistName:    "artist",
	}

	panel := m.albumCoverPanel(40, 20, 30, 15)
	if !strings.Contains(panel, "╭") {
		t.Fatal("expected ansi loading state to keep placeholder box")
	}
}

func TestKittyOverlayPlayerSameCoverDifferentTrackStillRedraws(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{
		TrackID:       "track-1",
		TrackName:     "one",
		ArtistName:    "artist",
		AlbumImageURL: "u1",
	}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first kitty render")
	}
	m.transport.status.TrackID = "track-2"
	m.transport.status.TrackName = "two"

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
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{
		TrackID:       "track-1",
		TrackName:     "one",
		ArtistName:    "artist",
		AlbumImageURL: "u1",
	}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first kitty render")
	}
	if second := m.kittyOverlay(); second != "" {
		t.Fatalf("expected unchanged state to skip redraw, got %q", second)
	}
	m.transport.playerCoverEpoch++
	third := m.kittyOverlay()
	if third == "" {
		t.Fatal("expected epoch increment to force kitty redraw even with same cover/subject")
	}
}

func TestKittyOverlaySameURLWithoutEncodingKeepsCurrentImage(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	if first == "" {
		t.Fatal("expected first kitty render")
	}
	delete(m.ui.imgs.encoded, "u1")

	loading := m.kittyOverlay()
	if strings.Contains(loading, kittyDeleteAll) {
		t.Fatal("expected same-url missing encoding to keep current kitty image visible")
	}
}

func TestKittyOverlayClearsStaleImageOnTabSwitchWithoutEncodedCover(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlaylists
	m.browse.playlistList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "p1", Name: "one", ImageURL: "u1"}},
	})
	m.browse.playlistList.Select(0)
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial playlist kitty render")
	}

	m.ui.activeTab = tabAlbums
	m.browse.albumList.SetItems([]list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "a1", Name: "album", Kind: spotify.ContextKindAlbum, ImageURL: "u2"}},
	})
	m.browse.albumList.Select(0)

	overlay := m.kittyOverlay()
	if !strings.Contains(overlay, kittyDeleteAll) {
		t.Fatal("expected stale kitty image to clear when switched tab cover is not yet encoded")
	}
}

func TestKittyOverlayPlayerDeletesOldImageWhenURLChangesAtSamePlacement(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	m.ui.imgs.encoded["u2"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.transport.status.TrackID = "track-2"
	m.transport.status.AlbumImageURL = "u2"

	second := m.kittyOverlay()
	if second == "" {
		t.Fatal("expected redraw when player cover URL changes")
	}
	if !strings.Contains(second, kittyDeleteAll) {
		t.Fatal("expected old kitty image to be explicitly deleted before drawing new cover at the same placement")
	}
	if !strings.Contains(second, "ZmFrZQ==") {
		t.Fatal("expected redraw to include the new cover payload")
	}
}

func TestKittyOverlayPlayerSameURLRedrawOmitsDeleteAll(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	m.transport.playerCoverEpoch++

	second := m.kittyOverlay()
	if second == "" {
		t.Fatal("expected redraw on epoch bump")
	}
	if strings.Contains(second, kittyDeleteAll) {
		t.Fatal("expected same-URL epoch redraw not to globally delete (no URL change)")
	}
}

func hasInflightURL(cache *imgCache, url string) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, ok := cache.inflight[url]
	return ok
}
