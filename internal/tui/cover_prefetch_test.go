package tui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

const (
	prefetchCurrentURL = "https://cdn/current.png"
	prefetchNextURL    = "https://cdn/next.png"
)

func prefetchProbePNG(tb testing.TB) []byte {
	tb.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.NRGBA{R: 200, G: 30, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		tb.Fatalf("encode probe png: %v", err)
	}
	return buf.Bytes()
}

// prefetchProbeModel builds a player-tab model whose pushed queue heads
// with the current track, so visibleQueue()[0] is the up-next track.
func prefetchProbeModel(tb testing.TB, exec loader.Executor) model {
	tb.Helper()
	m := newModel(context.Background(), nil, config.Config{DeviceName: "orpheus"},
		nil, nil, loader.New(context.Background(), 64, exec))
	m.ui.width, m.ui.height = 120, 40
	m.ui.activeTab = tabPlayer
	m.ui.imgs = newImgCacheWithSelection("rendered", true, func(string) string { return "xterm-kitty" })
	m.transport.status = &spotify.PlaybackStatus{
		TrackID: "spotify:track:current", TrackName: "Current",
		ArtistName: "Artist", AlbumImageURL: prefetchCurrentURL,
		ProgressMS: 1000, DurationMS: 200000,
	}
	m.transport.queue = []spotify.QueueItem{
		{ID: "spotify:track:current", Name: "Current", ImageURL: prefetchCurrentURL},
		{ID: "spotify:track:next", Name: "Next", ImageURL: prefetchNextURL},
	}
	return m
}

func TestPrefetchKicksNextCoverOnce(t *testing.T) {
	var fetched atomic.Int32
	exec := func(context.Context, loader.LoadRequest) []loader.LoadResult {
		fetched.Add(1)
		return []loader.LoadResult{{Index: 0, Data: loader.ImageData{Data: prefetchProbePNG(t)}}}
	}
	m := prefetchProbeModel(t, exec)
	cmd := m.prefetchNextCoverCmd()
	if cmd == nil {
		t.Fatal("expected a prefetch kick for the up-next cover")
	}
	if _, ok := m.ui.cover.prefetched[prefetchNextURL]; !ok {
		t.Fatalf("prefetch memory = %v, want %q recorded", m.ui.cover.prefetched, prefetchNextURL)
	}
	if msg := cmd(); msg == nil {
		t.Fatal("prefetch cmd yielded nothing")
	} else if lm, ok := msg.(imageLoadedMsg); !ok || lm.err != nil || lm.url != prefetchNextURL {
		t.Fatalf("prefetch result: %#v", msg)
	}
	if n := fetched.Load(); n != 1 {
		t.Fatalf("fetched %d times, want 1", n)
	}
	if cmd := m.prefetchNextCoverCmd(); cmd != nil {
		t.Fatal("repeated push re-kicked the same head (dedupe broken)")
	}
	if n := fetched.Load(); n != 1 {
		t.Fatalf("dedupe failed to hold: fetched %d times", n)
	}
}

func TestPrefetchSkipsCurrentEmptyAndFailed(t *testing.T) {
	exec := func(context.Context, loader.LoadRequest) []loader.LoadResult {
		t.Fatal("no fetch should be kicked")
		return nil
	}
	m := prefetchProbeModel(t, exec)
	// Head is the current track's own cover.
	m.transport.queue[1].ImageURL = prefetchCurrentURL
	if cmd := m.prefetchNextCoverCmd(); cmd != nil {
		t.Fatal("prefetched the current cover")
	}
	// Head has no image.
	m.transport.queue[1].ImageURL = ""
	if cmd := m.prefetchNextCoverCmd(); cmd != nil {
		t.Fatal("prefetched an empty URL")
	}
	// No queue at all.
	m.transport.queue = nil
	if cmd := m.prefetchNextCoverCmd(); cmd != nil {
		t.Fatal("prefetched with no queue")
	}
	// Dead URL inside its fail cooldown.
	m.transport.queue = []spotify.QueueItem{
		{ID: "spotify:track:current"},
		{ID: "spotify:track:next", ImageURL: prefetchNextURL},
	}
	m.ui.imgs.markFailed(prefetchNextURL)
	if cmd := m.prefetchNextCoverCmd(); cmd != nil {
		t.Fatal("prefetched a cooldown-gated URL")
	}
	if len(m.ui.cover.prefetched) != 0 {
		t.Fatalf("skipped kick still stored memory: %v", m.ui.cover.prefetched)
	}
}

func TestPrefetchedSkipSwapsWithoutFetch(t *testing.T) {
	var fetched atomic.Int32
	exec := func(context.Context, loader.LoadRequest) []loader.LoadResult {
		fetched.Add(1)
		return []loader.LoadResult{{Index: 0, Data: loader.ImageData{Data: prefetchProbePNG(t)}}}
	}
	m := prefetchProbeModel(t, exec)
	if cmd := m.prefetchNextCoverCmd(); cmd == nil {
		t.Fatal("no prefetch kick")
	} else if msg := cmd(); msg == nil {
		t.Fatal("prefetch yielded nothing")
	}
	if n := fetched.Load(); n != 1 {
		t.Fatalf("fetched %d times, want 1", n)
	}
	// The skip: the next track becomes current. Its cover must already be
	// fully cached+encoded so the swap needs no fetch round trip.
	m.transport.status.AlbumImageURL = prefetchNextURL
	if m.ui.imgs.shouldQueueLoad(prefetchNextURL) {
		t.Fatal("prefetched cover not load-complete")
	}
	if enc := m.ui.imgs.encodedFor(prefetchNextURL); enc == "" {
		t.Fatal("prefetched cover has no kitty encoding")
	}
	if cmd := m.loadImageCmd(prefetchNextURL, true); cmd != nil {
		t.Fatal("cached cover still kicks a load (beginLoad should refuse)")
	}
	if n := fetched.Load(); n != 1 {
		t.Fatalf("skip refetched: %d fetches", n)
	}
}

func TestEmissionImmediateOnImageLoaded(t *testing.T) {
	// The swap must ride this message cycle's overlay cmd, never the next
	// tick: execute the returned cmd and demand a Raw emission now.
	m := prefetchProbeModel(t, nil)
	img, _, err := image.Decode(bytes.NewReader(prefetchProbePNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	m.ui.imgs.setImage(prefetchCurrentURL, img, 40, 20)
	_, cmd := m.handleImageLoadedMsg(imageLoadedMsg{url: prefetchCurrentURL})
	if cmd == nil {
		t.Fatal("no overlay cmd on loaded current cover")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("overlay emission deferred to a later cycle")
	}
	if _, ok := msg.(tea.RawMsg); !ok {
		t.Fatalf("expected tea.RawMsg this cycle, got %T", msg)
	}
}

func TestPrefetchFailureReleasesMemory(t *testing.T) {
	m := prefetchProbeModel(t, nil)
	m.ui.cover.prefetched = map[string]struct{}{prefetchNextURL: {}}
	_ = m.handleImageLoadFailure(prefetchNextURL, errors.New("probe fetch failure"))
	if len(m.ui.cover.prefetched) != 0 {
		t.Fatalf("failed prefetch stuck in memory: %v", m.ui.cover.prefetched)
	}
}
