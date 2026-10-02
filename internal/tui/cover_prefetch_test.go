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
	"time"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/config"
	"orpheus/internal/loader"
	"orpheus/internal/spotify"
)

const (
	prefetchCurrentURL = "https://cdn/current.png"
	prefetchNextURL    = "https://cdn/next.png"
	prefetchAfterURL   = "https://cdn/after.png"
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

// The pushed queue already excludes the playing entry.
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
		{ID: "spotify:track:next", Name: "Next", ImageURL: prefetchNextURL, Queued: true},
		{ID: "spotify:track:after", Name: "After", ImageURL: prefetchAfterURL},
	}
	return m
}

// A fake executor that answers every requested item, so batch drains
// complete the whole sweep instead of stranding unanswered indexes.
func sweepAnsweringExec(tb testing.TB, fetched *atomic.Int32) loader.Executor {
	tb.Helper()
	return func(_ context.Context, req loader.LoadRequest) []loader.LoadResult {
		fetched.Add(1)
		results := make([]loader.LoadResult, 0, len(req.Items))
		for i := range req.Items {
			results = append(results, loader.LoadResult{Index: i, Data: loader.ImageData{Data: prefetchProbePNG(tb)}})
		}
		return results
	}
}

func runSweepCmd(tb testing.TB, cmd tea.Cmd) {
	tb.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch batch := msg.(type) {
	case imagesBatchLoadedMsg:
		// Decodes and caching already ran inside the drain closure.
	case tea.BatchMsg:
		for _, c := range []tea.Cmd(batch) {
			if c != nil {
				_ = c()
			}
		}
	default:
		_ = batch
	}
}

func TestSweepWarmsFullQueueHeadFirst(t *testing.T) {
	var fetched atomic.Int32
	m := prefetchProbeModel(t, sweepAnsweringExec(t, &fetched))
	cmd := m.sweepQueueCoversCmd()
	if cmd == nil {
		t.Fatal("expected a sweep kick for a queue with image URLs")
	}
	runSweepCmd(t, cmd)
	if n := fetched.Load(); n != 1 {
		t.Fatalf("sweep drained in %d fetches, want 1 batch", n)
	}
	for _, url := range []string{prefetchNextURL, prefetchAfterURL} {
		if m.ui.imgs.shouldQueueLoad(url) {
			t.Fatalf("swept cover %q is not load-complete", url)
		}
		if enc := m.ui.imgs.encodedFor(url); enc == "" {
			t.Fatalf("swept cover %q has no kitty encoding", url)
		}
	}
	// Steady state: a second push re-enqueues nothing cached.
	if cmd := m.sweepQueueCoversCmd(); cmd != nil {
		runSweepCmd(t, cmd)
	}
	if n := fetched.Load(); n != 1 {
		t.Fatalf("warm sweep refetched: %d fetches, want 1", n)
	}
}

func TestSweepSkipsCurrentEmptyFailedAndPaused(t *testing.T) {
	exec := func(context.Context, loader.LoadRequest) []loader.LoadResult {
		t.Fatal("no fetch should be kicked")
		return nil
	}
	m := prefetchProbeModel(t, exec)
	// Head carries the current cover (stale window): same URL, no kick.
	m.transport.queue[0].ImageURL = prefetchCurrentURL
	m.transport.queue[1].ImageURL = ""
	m.ui.imgs.markFailed(prefetchAfterURL)
	// Everything skippable: current, empty, cooldown-gated.
	if cmd := m.sweepQueueCoversCmd(); cmd != nil {
		runSweepCmd(t, cmd)
	}
	if n := m.ui.cover.queue.Len(); n != 0 {
		t.Fatalf("sweep queued %d URLs, want 0", n)
	}
	// No queue at all.
	m.transport.queue = nil
	if cmd := m.sweepQueueCoversCmd(); cmd != nil {
		t.Fatal("swept with no queue")
	}
	// A parked sweep enqueues nothing.
	m = prefetchProbeModel(t, exec)
	m.ui.cover.pauseSweep(time.Hour)
	if cmd := m.sweepQueueCoversCmd(); cmd != nil {
		t.Fatal("parked sweep still kicked")
	}
	// Expiry resumes: no timer, no flag to clear — the wait elapses.
	m.ui.cover.sweepPausedUntil = time.Now().Add(-time.Second)
	if cmd := m.sweepQueueCoversCmd(); cmd == nil {
		t.Fatal("expired park must resume the sweep")
	}
}

func TestSweepPromotesNewHeadOnRapidSkip(t *testing.T) {
	m := prefetchProbeModel(t, nil)
	// Stale sweep entries sit ahead; a rapid skip must re-prioritize the
	// new head to the drain front (the sweep calls promoteURL per push).
	for _, u := range []string{"https://cdn/stale1.png", "https://cdn/stale2.png", "https://cdn/new.png"} {
		m.enqueueCoverURL(u)
	}
	m.ui.cover.promoteURL("https://cdn/new.png")
	for _, want := range []string{"https://cdn/new.png", "https://cdn/stale1.png", "https://cdn/stale2.png"} {
		got, ok := m.ui.cover.popURL()
		if !ok || got != want {
			t.Fatalf("drain order: got %q, want %q", got, want)
		}
	}
}

func TestSweptSkipSwapsWithoutFetch(t *testing.T) {
	var fetched atomic.Int32
	m := prefetchProbeModel(t, sweepAnsweringExec(t, &fetched))
	runSweepCmd(t, m.sweepQueueCoversCmd())
	if n := fetched.Load(); n != 1 {
		t.Fatalf("fetched %d times, want 1", n)
	}
	// The skip: the next track becomes current. Its cover must already be
	// fully cached+encoded so the swap needs no fetch round trip.
	m.transport.status.AlbumImageURL = prefetchNextURL
	if m.ui.imgs.shouldQueueLoad(prefetchNextURL) {
		t.Fatal("swept cover not load-complete")
	}
	if enc := m.ui.imgs.encodedFor(prefetchNextURL); enc == "" {
		t.Fatal("swept cover has no kitty encoding")
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

func TestRateLimitFailureParksSweepWithoutRetries(t *testing.T) {
	m := prefetchProbeModel(t, nil)
	// Drive the real batch path: the pause lives on the propagated model
	// (coverManager travels by value), so assert on the returned model.
	next, cmd := m.handleImagesBatchLoadedMsg(imagesBatchLoadedMsg{
		results: []imageLoadedMsg{{url: prefetchNextURL, err: &spotify.RateLimitError{RetryAfter: 5 * time.Minute}}},
	})
	got := next.(model)
	if cmd != nil {
		// Only the chained drain/overlay may remain; no retry tick.
		if _, ok := got.ui.cover.imageRetryCount[prefetchNextURL]; ok {
			t.Fatal("penalty failure must not schedule a retry")
		}
	}
	if _, stamped := m.ui.imgs.failedAt[prefetchNextURL]; !stamped {
		t.Fatal("penalty failure must mark failed so the cooldown paces re-evaluation")
	}
	if _, ok := m.ui.cover.imageRetryCount[prefetchNextURL]; ok {
		t.Fatal("penalty failure must not enter the retry ladder")
	}
	if !got.ui.cover.sweepPaused() {
		t.Fatal("penalty failure must park the background sweep")
	}
	// Transient errors still climb the ladder.
	if _, cmd := m.handleImageLoadFailure(prefetchAfterURL, errors.New("transient")); cmd == nil {
		t.Fatal("transient failure must still schedule a retry")
	}
	if got.ui.cover.sweepPausedUntil.After(time.Now().Add(sweepPauseMax + time.Minute)) {
		t.Fatal("sweep park exceeds the cap")
	}
}

// End-to-end delivery through the real state handler: an imageless push
// kicks no sweep; the re-push carrying resolved URLs (the backend's
// post-batch delivery) must sweep the whole queue; the subsequent skip
// must be a warm hit with no refetch of the now-current cover.
func TestRepushDeliversHeadCoverForWarmSkip(t *testing.T) {
	var fetched atomic.Int32
	exec := func(context.Context, loader.LoadRequest) []loader.LoadResult {
		fetched.Add(1)
		return []loader.LoadResult{{Index: 0, Data: loader.ImageData{Data: prefetchProbePNG(t)}}}
	}
	m := prefetchProbeModel(t, exec)
	runCmds := func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range []tea.Cmd(batch) {
				if c != nil {
					_ = c()
				}
			}
		}
	}
	pushStatus := func(trackID, coverURL string) *spotify.PlaybackStatus {
		return &spotify.PlaybackStatus{
			TrackID: trackID, TrackName: "T", ArtistName: "A",
			AlbumImageURL: coverURL, ProgressMS: 1000, DurationMS: 200000,
		}
	}

	// Push 1: cold start, head imageless (pre-batch state). Only the
	// current cover may fetch; no sweep without URLs.
	_, cmd := m.handlePlaybackStateMsg(playbackStateMsg{
		status:        pushStatus("spotify:track:current", prefetchCurrentURL),
		queue:         []spotify.QueueItem{{ID: "spotify:track:next", Name: "Next"}},
		queueIncluded: true,
	})
	runCmds(cmd)
	if n := fetched.Load(); n != 1 {
		t.Fatalf("cold push fetched %d times, want 1 (current cover only)", n)
	}

	// Push 2: the post-batch re-push carries resolved URLs for the whole
	// queue; one sweep batch must warm both covers.
	_, cmd = m.handlePlaybackStateMsg(playbackStateMsg{
		status: pushStatus("spotify:track:current", prefetchCurrentURL),
		queue: []spotify.QueueItem{
			{ID: "spotify:track:next", Name: "Next", ImageURL: prefetchNextURL, Queued: true},
			{ID: "spotify:track:after", Name: "After", ImageURL: prefetchAfterURL},
		},
		queueIncluded: true,
	})
	runCmds(cmd)
	if n := fetched.Load(); n != 2 {
		t.Fatalf("re-push fetched %d times, want 2 (one sweep batch for the queue)", n)
	}

	// The skip: the swept cover is fully cached, so becoming current
	// must not refetch it.
	_, cmd = m.handlePlaybackStateMsg(playbackStateMsg{
		status: pushStatus("spotify:track:next", prefetchNextURL),
		queue: []spotify.QueueItem{
			{ID: "spotify:track:after", Name: "After", ImageURL: prefetchAfterURL},
		},
		queueIncluded: true,
	})
	runCmds(cmd)
	if m.ui.imgs.shouldQueueLoad(prefetchNextURL) {
		t.Fatal("skipped-to cover is not warm after sweep")
	}
}
