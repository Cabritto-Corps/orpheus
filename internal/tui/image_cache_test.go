package tui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"orpheus/internal/config"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

func NewLoaderModel() model {
	m := newModel(context.Background(), nil, config.Config{DeviceName: "orpheus"}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), nil)))
	// Past the startup gate: most tests exercise the steady-state view paths.
	m.browse.librarySettled = true
	return m
}

func TestHandlePlaylistKeyLoadsNewSelectedCoverImmediately(t *testing.T) {
	m := NewLoaderModel()
	items := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "two", ImageURL: "u2"}},
	}
	m.browse.playlistList.SetItems(items)
	m.browse.playlistList.Select(0)

	nextModel, _ := m.handlePlaylistKey(tea.KeyPressMsg{Code: tea.KeyDown})
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

	nextModel, _ := m.handleAlbumKey(tea.KeyPressMsg{Code: tea.KeyDown})
	got := nextModel.(model)
	sel, ok := got.selectedAlbum()
	if !ok || sel.summary.ImageURL != "u2" {
		t.Fatalf("expected album selection to move to u2")
	}
	if !hasInflightURL(got.ui.imgs, "u2") {
		t.Fatalf("expected immediate image load for new album selection")
	}
}

// A selection change must emit the kitty swap on the keypress itself.
// A fully prefetched cover produces no load-completion event at all
// (beginLoad short-circuits the cmd), so emission driven by
// imageLoadedMsg or the periodic tick lagged the cursor by up to a
// second when idle — prefetch made the swap slower, not faster.
func TestNavKeyEmitsKittySwapForPrefetchedCover(t *testing.T) {
	t.Setenv("TMUX", "")
	flat := func(c color.RGBA) image.Image {
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for y := range 8 {
			for x := range 8 {
				img.SetRGBA(x, y, c)
			}
		}
		return img
	}
	for _, tc := range []struct {
		name string
		tab  tab
	}{
		{"playlists", tabPlaylists},
		{"albums", tabAlbums},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := framedTestModel()
			m.ui.width = 100
			m.ui.height = 40
			m.ui.activeTab = tc.tab
			m.ui.imgs.protocol = imageProtocolKitty
			m.ui.imgs.setImage("u1", flat(color.RGBA{R: 255, A: 255}), 40, 20)
			m.ui.imgs.setImage("u2", flat(color.RGBA{B: 255, A: 255}), 40, 20)
			markerU1 := m.ui.imgs.encodedFor("u1")
			markerU2 := m.ui.imgs.encodedFor("u2")
			if markerU1 == "" || markerU2 == "" || markerU1 == markerU2 {
				t.Fatalf("need two distinct prefetched payloads, got %q / %q", markerU1, markerU2)
			}

			newItem := func(id, name, url string) playlistItem {
				it := playlistItem{summary: spotify.PlaylistSummary{ID: id, Name: name, ImageURL: url}}
				if tc.tab == tabAlbums {
					it.summary.Kind = spotify.ContextKindAlbum
				}
				return it
			}
			items := []list.Item{newItem("1", "one", "u1"), newItem("2", "two", "u2")}
			if tc.tab == tabAlbums {
				m.browse.albumList.SetItems(items)
				m.browse.albumList.Select(0)
			} else {
				m.browse.playlistList.SetItems(items)
				m.browse.playlistList.Select(0)
			}

			// Show the first cover so the keypress swaps rather than
			// initialises the overlay slot.
			primed := m.kittyOverlayCmd()
			if primed == nil {
				t.Fatal("expected an initial cover emission")
			}
			if raw, ok := primed().(tea.RawMsg); !ok || !strings.Contains(fmt.Sprint(raw.Msg), markerU1) {
				t.Fatalf("expected the primed emission to carry u1, got %v", primed())
			}

			var next tea.Model
			var cmd tea.Cmd
			if tc.tab == tabAlbums {
				next, cmd = m.handleAlbumKey(tea.KeyPressMsg{Code: tea.KeyDown})
			} else {
				next, cmd = m.handlePlaylistKey(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			got := next.(model)

			selURL := ""
			if tc.tab == tabAlbums {
				if sel, ok := got.selectedAlbum(); ok {
					selURL = sel.summary.ImageURL
				}
			} else if sel, ok := got.selectedPlaylist(); ok {
				selURL = sel.summary.ImageURL
			}
			if selURL != "u2" {
				t.Fatalf("expected the cursor on u2, got %q", selURL)
			}
			// The swap cannot ride a load completion: prefetch already
			// holds the cover, so loadImageCmd short-circuits to nil.
			if got.loadImageCmd("u2", false) != nil {
				t.Fatal("precondition broken: cover is not fully prefetched")
			}
			if !batchEmitsRaw(cmd, "a=T", markerU2) {
				t.Fatal("selection change carried no kitty transmit for the new cover; the swap waits for the tick")
			}
		})
	}
}

// batchEmitsRaw unwraps tea.BatchMsg layers and reports whether any
// command delivers a tea.RawMsg whose bytes carry every want substring.
func batchEmitsRaw(cmd tea.Cmd, wants ...string) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if batchEmitsRaw(c, wants...) {
				return true
			}
		}
	case tea.RawMsg:
		s := fmt.Sprint(msg.Msg)
		for _, w := range wants {
			if !strings.Contains(s, w) {
				return false
			}
		}
		return true
	}
	return false
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

	nextModel, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
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
	cache.preRenderCovers("u-0", [][2]int{{8, 4}}, colorprofile.TrueColor)

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

func TestCachedImageDownscaledToBound(t *testing.T) {
	cache := newImgCache()
	src := image.NewRGBA(image.Rect(0, 0, 800, 600))
	cache.setImage("big", src, 0, 0)
	got, ok := cache.getImage("big")
	if !ok {
		t.Fatal("expected cached image")
	}
	if d := got.Bounds().Dx(); d != maxCachedImageLongestSide {
		t.Fatalf("expected cached width %d, got %d", maxCachedImageLongestSide, d)
	}
	if h := got.Bounds().Dy(); h != 600*maxCachedImageLongestSide/800 {
		t.Fatalf("expected proportional height, got %d", h)
	}
}

func TestCachedSmallImageUntouched(t *testing.T) {
	cache := newImgCache()
	src := image.NewRGBA(image.Rect(0, 0, 100, 80))
	cache.setImage("small", src, 0, 0)
	got, ok := cache.getImage("small")
	if !ok {
		t.Fatal("expected cached image")
	}
	if d, h := got.Bounds().Dx(), got.Bounds().Dy(); d != 100 || h != 80 {
		t.Fatalf("expected 100x80 untouched, got %dx%d", d, h)
	}
}

func TestCachedImageRendersDeterministicallyAtUsedSizes(t *testing.T) {
	cache := newImgCache()
	src := image.NewRGBA(image.Rect(0, 0, 640, 640))
	for y := range 640 {
		for x := range 640 {
			src.Set(x, y, color.NRGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	cache.setImage("art", src, 0, 0)
	for _, size := range [][2]int{{30, 20}, {40, 30}, {12, 8}} {
		first, ok := cache.cover("art", size[0], size[1], colorprofile.TrueColor)
		if !ok || first == "" {
			t.Fatalf("expected render at %dx%d", size[0], size[1])
		}
		second, ok := cache.cover("art", size[0], size[1], colorprofile.TrueColor)
		if !ok || second != first {
			t.Fatalf("expected deterministic render at %dx%d", size[0], size[1])
		}
	}
	if enc := cache.encodedFor("art"); enc == "" {
		t.Skip("kitty encoding asserted only under kitty protocol")
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
	cache.preRenderCovers("u", sizes, colorprofile.TrueColor)

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
	intent := overlayIntent{tab: tabPlayer, url: "u1"}
	if emit, _, _ := m.ui.imgs.commitOverlayIntent(intent); !emit {
		t.Fatal("expected initial overlay commit to emit")
	}
	if emit, _, _ := m.ui.imgs.commitOverlayIntent(intent); emit {
		t.Fatal("expected unchanged overlay to suppress redraw before the load")
	}

	nextModel, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: "u1"})
	got := nextModel.(model)
	// The forced redraw rides the overlay command straight to the wire
	// instead of waiting for a tick.
	if cmd == nil {
		t.Fatal("expected the forced redraw to ride the overlay command")
	}
	msg, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("expected tea.RawMsg, got %T", cmd())
	}
	if !strings.Contains(fmt.Sprint(msg.Msg), "d=i") {
		t.Fatalf("expected the overlay command to carry the redraw, got %q", fmt.Sprint(msg.Msg))
	}
	// The load forces retransmission: the same intent must emit again so
	// the newly available encoding actually reaches the terminal.
	if emit, _, _ := got.ui.imgs.commitOverlayIntent(intent); !emit {
		t.Fatal("expected successful current player cover load to force kitty redraw")
	}
}

func TestHandleImageLoadedMsgForOtherURLDoesNotForceKittyRedraw(t *testing.T) {
	m := NewLoaderModel()
	// A laid-out terminal: at zero width the overlay evaluation inside the
	// handler would clear the slot via the empty-rect path, which is
	// correct bookkeeping but not what this test is about.
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.ui.imgs.protocol = imageProtocolKitty
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	intent := overlayIntent{tab: tabPlayer, url: "u1"}
	if emit, _, _ := m.ui.imgs.commitOverlayIntent(intent); !emit {
		t.Fatal("expected initial overlay commit to emit")
	}

	nextModel, _ := m.handleImageLoadedMsg(imageLoadedMsg{url: "u2"})
	got := nextModel.(model)
	if emit, _, _ := got.ui.imgs.commitOverlayIntent(intent); emit {
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
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), catalog)))
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
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), catalog)))
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
	m := newModel(context.Background(), catalog, config.Config{DeviceName: "orpheus"}, nil, nil, loader.New(context.Background(), 64, NewTUIExecutor(context.Background(), catalog)))
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
		items: []spotify.PlaylistSummary{
			{ID: "p1", Name: "playlist", Kind: spotify.ContextKindPlaylist, ImageURL: "u-playlist"},
			{ID: "a1", Name: "album", Kind: spotify.ContextKindAlbum, ImageURL: "u-album"},
		},
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
	if m.ui.cover.queue.Len() != 1 {
		t.Fatalf("expected deduped cover queue size 1, got %d", m.ui.cover.queue.Len())
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

// An unchanged visible intent must emit NOTHING: the image and its
// placement are already in the terminal, and re-placing would churn an
// erase/put loop the steady flow shows as flicker. Force slots (repaints,
// protocol resets) still retransmit deliberately.
func TestKittyOverlayStaysSilentWhenUnchanged(t *testing.T) {
	t.Setenv("TMUX", "")
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
	if second := m.kittyOverlay(); second != "" {
		t.Fatalf("expected unchanged overlay to stay silent, got %q", second)
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
	if !strings.Contains(overlay, "a=d,d=i") {
		t.Fatal("expected delete-all when image disappears")
	}
	if again := m.kittyOverlay(); again != "" {
		t.Fatalf("expected repeated empty state to avoid repeated delete, got %q", again)
	}
}

// placementIDOf extracts the placement ID (p=<n>) from a nil-payload
// placement packet. Re-place and hide packets carry no base64, so the
// p= is the placement field (the a=p action never pairs p with =).
func placementIDOf(t *testing.T, packet string) string {
	t.Helper()
	idx := strings.LastIndex(packet, "p=")
	if idx < 0 {
		t.Fatalf("expected a placement ID in %q", packet)
	}
	rest := packet[idx+2:]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		t.Fatalf("expected digits after p= in %q", packet)
	}
	return rest[:end]
}

func TestKittyOverlayHidesUnderAnyModal(t *testing.T) {
	// Every modal frame re-deletes image-scoped (no p=) while the slot
	// believes an image is live — never once-and-silent, so a missed delete
	// or resurrected placement self-heals. The delete names the image only:
	// placement IDs churn every frame and Ghostty has point-delete gaps.
	// Re-placing during a modal would redraw the cover over the scrim:
	// z=-1 shows through default-background cells.
	t.Setenv("TMUX", "")
	cases := []struct {
		name string
		open func(*model)
	}{
		{"help", func(m *model) { m.ui.helpOpen = true }},
		{"settings", func(m *model) { m.ui.settings.open = true }},
		{"track popup", func(m *model) { m.ui.trackPopupOpen = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewLoaderModel()
			m.ui.width = 120
			m.ui.height = 40
			m.ui.activeTab = tabPlayer
			m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
			m.ui.imgs.protocol = imageProtocolKitty
			m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

			first := m.kittyOverlay()
			if !strings.Contains(first, "a=T") {
				t.Fatalf("expected initial transmit, got %q", first)
			}
			shownID := transmitImageID(t, first)
			tc.open(&m)

			hide := m.kittyOverlay()
			if !strings.Contains(hide, "a=d,d=i,i="+shownID) {
				t.Fatalf("expected an image-scoped modal hide, got %q", hide)
			}
			if strings.Contains(hide, "p=") {
				t.Fatalf("expected no placement targeting in the hide, got %q", hide)
			}
			if strings.Contains(hide, "d=I") || strings.Contains(hide, "a=T") || strings.Contains(hide, "a=p") {
				t.Fatalf("expected a placement-only delete with no transmit or re-place, got %q", hide)
			}
			if again := m.kittyOverlay(); !strings.Contains(again, "a=d,d=i,i="+shownID) {
				t.Fatalf("expected the hide to repeat while the modal is open, got %q", again)
			}
		})
	}
}

func TestKittyOverlayHidesTransmitBeforeFirstReplace(t *testing.T) {
	// A modal opened between the transmit and the first re-place hides
	// with the same image-scoped delete (the stored data must survive
	// for close); the hide repeats while the modal stays open.
	t.Setenv("TMUX", "")
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if out := m.kittyOverlay(); !strings.Contains(out, "a=T") {
		t.Fatalf("expected initial transmit, got %q", out)
	}
	m.ui.helpOpen = true
	hide := m.kittyOverlay()
	if !strings.Contains(hide, "a=d,d=i") || strings.Contains(hide, "d=I") {
		t.Fatalf("expected a placement-only delete, got %q", hide)
	}
	if strings.Contains(hide, "p=") || strings.Contains(hide, "a=T") {
		t.Fatalf("expected no placement field and no transmit, got %q", hide)
	}
	if again := m.kittyOverlay(); !strings.Contains(again, "a=d,d=i") {
		t.Fatalf("expected the hide to repeat while the modal is open, got %q", again)
	}
}

func TestKittyOverlayRestoresAfterModalCloses(t *testing.T) {
	// The modal-close frame re-places from the still-stored data (fresh
	// placement ID, no retransmit): the hide delete was placement-only
	// precisely so the data survives the modal.
	t.Setenv("TMUX", "")
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	if out := m.kittyOverlay(); !strings.Contains(out, "a=T") {
		t.Fatalf("expected initial transmit, got %q", out)
	}
	oldP := ""
	if placed := m.kittyOverlay(); placed != "" {
		oldP = placementIDOf(t, placed)
	}

	m.ui.helpOpen = true
	hide := m.kittyOverlay()
	if !strings.Contains(hide, "a=d,d=i") {
		t.Fatalf("expected the modal hide, got %q", hide)
	}
	if strings.Contains(hide, "p=") {
		t.Fatalf("expected the hide to be image-scoped, got %q", hide)
	}

	m.ui.helpOpen = false
	restored := m.kittyOverlay()
	if restored == "" {
		t.Fatal("expected a re-place on the close frame")
	}
	if strings.Contains(restored, "d=I") {
		t.Fatalf("expected close without spurious purge, got %q", restored)
	}
	if !strings.Contains(restored, "a=p") || strings.Contains(restored, "a=T") {
		t.Fatalf("expected close to re-place without retransmitting, got %q", restored)
	}
	if newP := placementIDOf(t, restored); oldP != "" && newP == oldP {
		t.Fatalf("expected a fresh placement ID on restore, got reused %s", newP)
	}
	if again := m.kittyOverlay(); again != "" {
		t.Fatalf("expected settled overlay to stay silent (drawn placement persists), got %q", again)
	}
}

// transmitIDOf extracts the image ID (i=<n>) from the transmit (a=T)
// packet's control section (before its payload ';'): base64 payload
// bytes — and purge packets riding the same emission — must never be
// scanned for the ID.
func transmitIDOf(t *testing.T, emission string) string {
	t.Helper()
	for seg := range strings.SplitSeq(emission, "\x1b_G") {
		ctrl := seg
		if i := strings.Index(ctrl, ";"); i >= 0 {
			ctrl = ctrl[:i]
		}
		if !strings.Contains(ctrl, "a=T") {
			continue
		}
		if idx := strings.Index(ctrl, "i="); idx >= 0 {
			rest := ctrl[idx+2:]
			end := 0
			for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
				end++
			}
			if end > 0 {
				return rest[:end]
			}
		}
	}
	t.Fatalf("expected a transmit packet in %q", emission)
	return ""
}

func TestKittyStyleSwitchToPixelatedPurgesShownImage(t *testing.T) {
	// kitty -> pixelated must purge the shown image's data and placements
	// exactly once: the switch resets the slot without naming the image,
	// so the pending purge goes out on the next command and the path
	// stays silent after (the half-block art underneath is healthy — the
	// bug was the stranded corpse on top of it).
	t.Setenv("TMUX", "")
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	id := transmitIDOf(t, first)

	m.ui.settings.imageStyle = config.ImageStylePixelated
	updated, cmd := m.applyImageStyleWithEnv(func(string) string { return "" })
	m = updated.(model)
	if cmd != nil {
		t.Fatalf("expected no cover command on a pixelated switch, got %v", cmd)
	}
	if m.ui.imgs.protocolForRender() == imageProtocolKitty {
		t.Fatal("expected the switch to leave the kitty protocol")
	}

	purge := m.kittyOverlayCmd()
	if purge == nil {
		t.Fatal("expected the pending purge command after the switch")
	}
	raw, ok := purge().(tea.RawMsg)
	if !ok {
		t.Fatalf("expected tea.RawMsg, got %T", purge())
	}
	out := fmt.Sprint(raw.Msg)
	if !strings.Contains(out, "a=d,d=I") || !strings.Contains(out, "i="+id) {
		t.Fatalf("expected one data purge of image %s, got %q", id, out)
	}
	if strings.Contains(out, "a=T") || strings.Contains(out, "a=p") {
		t.Fatalf("expected no transmit or re-place on the purge frame, got %q", out)
	}
	if again := m.kittyOverlayCmd(); again != nil {
		t.Fatal("expected silence after the purge")
	}
	if out := m.kittyOverlay(); strings.Contains(out, "\x1b_G") {
		t.Fatalf("expected no graphics bytes after the purge, got %q", out)
	}
}

func TestKittyAutoFallbackPurgesShownImage(t *testing.T) {
	// The automatic kitty -> half-block fallback (repeated player cover
	// failures) strands the same way a manual style switch does: the
	// pending purge must land on the next overlay command.
	t.Setenv("TMUX", "")
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	first := m.kittyOverlay()
	id := transmitIDOf(t, first)

	m.ui.cover.playerCoverFailStreak = kittyProtocolFallbackFailures
	m.maybeFallbackFromKittyOnPlayerFailures("u1")
	if m.ui.imgs.protocolForRender() == imageProtocolKitty {
		t.Fatal("expected the fallback to leave the kitty protocol")
	}

	purge := m.kittyOverlayCmd()
	if purge == nil {
		t.Fatal("expected the pending purge command after the fallback")
	}
	raw, ok := purge().(tea.RawMsg)
	if !ok {
		t.Fatalf("expected tea.RawMsg, got %T", purge())
	}
	if out := fmt.Sprint(raw.Msg); !strings.Contains(out, "a=d,d=I") || !strings.Contains(out, "i="+id) {
		t.Fatalf("expected one data purge of image %s, got %q", id, out)
	}
	if again := m.kittyOverlayCmd(); again != nil {
		t.Fatal("expected silence after the purge")
	}
}

func TestKittySwitchRoundTripPurgesEveryTransmittedImage(t *testing.T) {
	// kitty -> pixelated -> kitty -> pixelated with no emissions between
	// the first two switches: every transmitted ID must appear in a d=I
	// purge (no leak per round trip), and the stranded purge rides up
	// front of the round-trip transmit rather than dropping.
	t.Setenv("TMUX", "")
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	noEnv := func(string) string { return "" }
	ghosttyEnv := func(k string) string {
		if k == "TERM_PROGRAM" {
			return "ghostty"
		}
		return ""
	}

	first := m.kittyOverlay()
	id1 := transmitIDOf(t, first)

	m.ui.settings.imageStyle = config.ImageStylePixelated
	updated, _ := m.applyImageStyleWithEnv(noEnv)
	m = updated.(model)
	m.ui.settings.imageStyle = config.ImageStyleRendered
	updated, _ = m.applyImageStyleWithEnv(ghosttyEnv)
	m = updated.(model)
	if m.ui.imgs.protocolForRender() != imageProtocolKitty {
		t.Fatal("expected the switch-back to restore the kitty protocol")
	}
	// Switches clear payload caches; the reload path re-encodes the
	// retained source image.
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	second := m.kittyOverlay()
	id2 := transmitIDOf(t, second)
	if id2 == id1 {
		t.Fatalf("expected a fresh image ID after the reset, got reused %s", id1)
	}
	if !strings.Contains(second, "a=d,d=I") || !strings.Contains(second, "i="+id1) {
		t.Fatalf("expected the stranded purge of image %s up front, got %q", id1, second)
	}
	if strings.Index(second, "d=I") > strings.Index(second, "a=T") {
		t.Fatalf("expected the purge to precede the new transmit, got %q", second)
	}

	m.ui.settings.imageStyle = config.ImageStylePixelated
	updated, _ = m.applyImageStyleWithEnv(noEnv)
	m = updated.(model)
	third := m.kittyOverlay()
	if !strings.Contains(third, "a=d,d=I") || !strings.Contains(third, "i="+id2) {
		t.Fatalf("expected the data purge of image %s, got %q", id2, third)
	}
	if out := m.kittyOverlay(); out != "" {
		t.Fatalf("expected silence after the purge, got %q", out)
	}
}

func TestPixelatedStartupEmitsNoGraphicsBytes(t *testing.T) {
	// Explicit pixelated from the start: the overlay path stays silent
	// across ticks — no transmit, no purge, no placement.
	t.Setenv("TMUX", "")
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolNone
	m.ui.imgs.protocolExplicit = true
	for i := range 5 {
		if out := m.kittyOverlay(); out != "" {
			t.Fatalf("expected silence on pixelated tick %d, got %q", i, out)
		}
		if cmd := m.kittyOverlayCmd(); cmd != nil {
			t.Fatalf("expected no command on pixelated tick %d", i)
		}
	}
}

func TestKittyOverlayPlayerHoldsPreviousImageWhileNextLoads(t *testing.T) {
	// Same surface, content still loading: the old cover stays up until
	// the new one is ready, so track changes never flash a blank gap.
	// (A tab switch is a different surface and still clears: see
	// TestKittyOverlayClearsStaleImageOnTabSwitchWithoutEncodedCover.)
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

	if loading := m.kittyOverlay(); loading != "" {
		t.Fatalf("expected the previous image held silently while the next cover loads, got %q", loading)
	}
	if got := strings.TrimSpace(m.ui.imgs.kittyDisplayedURL()); got != "u1" {
		t.Fatalf("expected the displayed URL to stay u1 while u2 loads, got %q", got)
	}
}

func TestKittyOverlayPlayerHoldsWhileTransportTransitionPending(t *testing.T) {
	// A pending transport transition is still the same surface: hold the
	// old cover instead of blanking it while the next one loads.
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
	if loading := m.kittyOverlay(); loading != "" {
		t.Fatalf("expected the previous image held while the transition is pending, got %q", loading)
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

func TestAlbumCoverPanelKittyLoadingShellMatchesLoaded(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.imgs.protocol = imageProtocolKitty
	m.transport.status = &spotify.PlaybackStatus{
		AlbumImageURL: "u-missing",
		TrackName:     "track",
		ArtistName:    "artist",
	}

	loading := m.albumCoverPanel(40, 20, 30, 15)
	// Shell parity: the loading state renders the same shell the decoded
	// cover gets — the default theme is unframed, so no border may pop
	// in and out around decode.
	if strings.Contains(loading, "╭") {
		t.Fatalf("loading shell grew a border the default cover never shows: %q", loading)
	}
}

func TestAlbumCoverPanelAnsiLoadingShellMatchesLoaded(t *testing.T) {
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.imgs.protocol = imageProtocolNone
	m.transport.status = &spotify.PlaybackStatus{
		AlbumImageURL: "u-missing",
		TrackName:     "track",
		ArtistName:    "artist",
	}

	loading := m.albumCoverPanel(40, 20, 30, 15)
	if strings.Contains(loading, "╭") {
		t.Fatalf("ansi loading shell grew a border the decoded cover never shows: %q", loading)
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
	if strings.Contains(second, "a=d,d=A") {
		t.Fatal("expected same-cover redraw not to clear globally before drawing")
	}
}

func TestKittyOverlayPlayerEpochForcesRedrawWithSameKeyInputs(t *testing.T) {
	t.Setenv("TMUX", "")
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
		t.Fatalf("expected unchanged state to stay silent, got %q", second)
	}
	m.transport.playerCoverEpoch++
	third := m.kittyOverlay()
	if third == "" {
		t.Fatal("expected epoch increment to force kitty redraw even with same cover/subject")
	}
	if strings.Contains(third, "a=d,d=A") {
		t.Fatalf("expected same-URL epoch redraw never to delete globally, got %q", third)
	}
	if again := m.kittyOverlay(); again != "" {
		t.Fatalf("expected the redrawn overlay to settle silent, got %q", again)
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
	if strings.Contains(loading, "a=d,d=i") {
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
	if !strings.Contains(overlay, "a=d,d=i") {
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
	if !strings.Contains(second, "d=I") {
		t.Fatal("expected old kitty image data to be purged once the new cover lands at the same placement")
	}
	if !strings.Contains(second, "ZmFrZQ==") {
		t.Fatal("expected redraw to include the new cover payload")
	}
}

func hasInflightURL(cache *imgCache, url string) bool {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	_, ok := cache.inflight[url]
	return ok
}
