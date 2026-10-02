package librespot

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/ap"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/spclient"
	"github.com/elxgy/go-librespot/tracks"
)

var errQueueHeadProbe = errors.New("probe metadata fetch failure")

func testQueueHeadURIs() (a, b, c string) {
	return "spotify:track:7GhIk7Il098yCjg4BQjzvb",
		"spotify:track:1GhIk7Il098yCjg4BQjzva",
		"spotify:track:2GhIk7Il098yCjg4BQjzvb"
}

func testQueueProdInfo(t *testing.T) ap.ProductInfo {
	t.Helper()
	var prod ap.ProductInfo
	if err := xml.Unmarshal([]byte(`<products><product><type>premium</type><head-files-url>https://x</head-files-url><image-url>https://i.scdn.co/image/{file_id}</image-url></product></products>`), &prod); err != nil {
		t.Fatalf("failed building test product info: %v", err)
	}
	return prod
}

func testProvidedTrack(uri, title, artist string) *connectpb.ProvidedTrack {
	return &connectpb.ProvidedTrack{
		Uri: uri,
		Metadata: map[string]string{
			"title":       title,
			"artist_name": artist,
		},
	}
}

// The prefetch only fires on queue entries carrying ImageURL, but context
// metadata carries names and no art: the head window must be selected for
// image resolution even when every entry already has a name.
func TestHeadImageURIsSelectsMissingOnly(t *testing.T) {
	uriA, uriB, uriC := testQueueHeadURIs()
	p := newTestAppPlayer()
	p.resetQueueMetaForContext()
	p.setCachedQueueMeta("7GhIk7Il098yCjg4BQjzvb", PlaybackStateQueueEntry{ID: "7GhIk7Il098yCjg4BQjzvb", Name: "A", ImageURL: "https://i.scdn.co/image/has"})
	p.setCachedQueueMeta("1GhIk7Il098yCjg4BQjzva", PlaybackStateQueueEntry{ID: "1GhIk7Il098yCjg4BQjzva", Name: "B"})

	upcoming := []*connectpb.ProvidedTrack{
		testProvidedTrack(uriA, "A", "Art"),
		testProvidedTrack(uriB, "B", "Art"),
		testProvidedTrack(uriC, "C", "Art"),
	}
	got := headImageURIs(upcoming, p.queueHeadImageMissing, 8)
	if len(got) != 2 || got[0] != uriB || got[1] != uriC {
		t.Fatalf("expected [B C] imageless head URIs, got %v", got)
	}
	// The window bounds resolved entries, not examined ones: A is skipped
	// (already warm), so one slot still yields B.
	got = headImageURIs(upcoming, p.queueHeadImageMissing, 1)
	if len(got) != 1 || got[0] != uriB {
		t.Fatalf("expected [B] within window 1, got %v", got)
	}
	if got := headImageURIs(upcoming, p.queueHeadImageMissing, 0); len(got) != 0 {
		t.Fatalf("expected empty window to select nothing, got %v", got)
	}
}

// Named entries must still enter the metadata batch when their images are
// missing — otherwise the head never gains ImageURL and the prefetch
// never fires for the whole context.
func TestResolveContextQueueMetadataIncludesHeadURIs(t *testing.T) {
	uriA, uriB, _ := testQueueHeadURIs()
	p := newTestAppPlayer()
	p.resetQueueMetaForContext()
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod

	var requested []string
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		requested = append(requested, uris...)
		out := make(map[string]spclient.ResolvedEntry, len(uris))
		for _, u := range uris {
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000, AlbumCoverFileId: []byte{9}}
		}
		return out, nil
	}

	all := []*connectpb.ProvidedTrack{
		testProvidedTrack(uriA, "Track A", "Artist A"),
		testProvidedTrack(uriB, "Track B", "Artist B"),
	}
	p.resolveContextQueueMetadata(context.Background(), all, []string{uriA, uriB})

	for _, u := range []string{uriA, uriB} {
		found := slices.Contains(requested, u)
		if !found {
			t.Fatalf("expected head URI %s in batch request %v", u, requested)
		}
	}
	for _, uri := range []string{uriA, uriB} {
		e := p.getCachedQueueMeta(golibrespot.NormalizeSpotifyId(uri))
		if e == nil || strings.TrimSpace(e.ImageURL) == "" {
			t.Fatalf("expected cached ImageURL for %s, got %+v", uri, e)
		}
		if !strings.Contains(e.ImageURL, "09") {
			t.Fatalf("expected cover file id in URL, got %q", e.ImageURL)
		}
	}
}

// Entries already carrying images must not trigger network work.
func TestResolveQueueMetadataBatchSkipsWarm(t *testing.T) {
	p := newTestAppPlayer()
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		t.Fatalf("fetch must not run for warm entries: %v", uris)
		return nil, nil
	}
	p.resolveQueueMetadataBatch(context.Background(), nil)
}

// Without a fetch seam and without a session, resolution is a safe no-op.
func TestResolveQueueMetadataBatchNoSessionNoOp(t *testing.T) {
	p := &AppPlayer{}
	p.resolveQueueMetadataBatch(context.Background(), []string{"spotify:track:7GhIk7Il098yCjg4BQjzvb"})
}

func TestMaybeWarmQueueHeadImagesNoops(t *testing.T) {
	p := newTestAppPlayer()
	p.maybeWarmQueueHeadImages()
	if p.queueHeadWarmInFlight.Load() {
		t.Fatal("nil state must not arm the in-flight flag")
	}
	p.state = &State{}
	p.maybeWarmQueueHeadImages()
	if p.queueHeadWarmInFlight.Load() {
		t.Fatal("nil tracks must not arm the in-flight flag")
	}
	p.queueHeadWarmInFlight.Store(true)
	p.maybeWarmQueueHeadImages()
	if !p.queueHeadWarmInFlight.Load() {
		t.Fatal("in-flight resolution must not be disturbed")
	}
	p.queueHeadWarmInFlight.Store(false)
}

// The delivery signal must fire only when the cache learned something:
// re-resolving identical data is the steady state and must stay silent,
// or every background batch would re-arm a re-resolve loop through Run.
func TestMergeQueueBatchResultReportsChange(t *testing.T) {
	p := newTestAppPlayer()
	p.resetQueueMetaForContext()
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod

	uri := "spotify:track:7GhIk7Il098yCjg4BQjzvb"
	batch := map[string]spclient.ResolvedEntry{
		uri: {Name: "N", Artist: "A", DurationMS: 200000, AlbumCoverFileId: []byte{9}},
	}
	if !p.mergeQueueBatchResult(batch) {
		t.Fatal("new entry must report changed")
	}
	if p.mergeQueueBatchResult(batch) {
		t.Fatal("identical re-resolve must report unchanged")
	}
	changedURL := map[string]spclient.ResolvedEntry{
		uri: {Name: "N", Artist: "A", DurationMS: 200000, AlbumCoverFileId: []byte{10}},
	}
	if !p.mergeQueueBatchResult(changedURL) {
		t.Fatal("changed cover image must report changed")
	}
	if p.mergeQueueBatchResult(map[string]spclient.ResolvedEntry{}) {
		t.Fatal("empty batch must report unchanged")
	}
}

// A failed batch teaches nothing: no signal, no re-push, no retry loop.
func TestResolveQueueMetadataBatchErrorReportsUnchanged(t *testing.T) {
	p := newTestAppPlayer()
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		return nil, errQueueHeadProbe
	}
	if p.resolveQueueMetadataBatch(context.Background(), []string{"spotify:track:7GhIk7Il098yCjg4BQjzvb"}) {
		t.Fatal("failed batch must report unchanged")
	}
}

func TestSignalQueueMetaUpdatedCoalesces(t *testing.T) {
	// A bare player (nil channel) must never block the resolving goroutine.
	(&AppPlayer{}).signalQueueMetaUpdated()
	p := newTestAppPlayer()
	p.queueMetaUpdated = make(chan struct{}, 1)
	p.signalQueueMetaUpdated()
	p.signalQueueMetaUpdated()
	p.signalQueueMetaUpdated()
	select {
	case <-p.queueMetaUpdated:
	default:
		t.Fatal("expected a pending delivery signal")
	}
	select {
	case <-p.queueMetaUpdated:
		t.Fatal("overlapping batches must coalesce into one signal")
	default:
	}
}

// Delivery proof: after the batch fills the cache, the Run-side handler
// must push a queue that carries the new cover URLs — the TUI prefetch
// only fires on entries carrying ImageURL, so without this push the warm
// batch never reaches the screen.
func TestHandleQueueMetaUpdatedDeliversQueueImages(t *testing.T) {
	uriA, uriB, uriC := testQueueHeadURIs()
	p, updates := newQueueHeadSignalPlayer(t, []string{uriA, uriB, uriC})
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		out := make(map[string]spclient.ResolvedEntry, len(uris))
		for _, u := range uris {
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000, AlbumCoverFileId: []byte{9}}
		}
		return out, nil
	}
	for _, uri := range []string{uriA, uriB, uriC} {
		p.setCachedQueueMeta(golibrespot.NormalizeSpotifyId(uri), PlaybackStateQueueEntry{
			ID: golibrespot.NormalizeSpotifyId(uri), Name: "N", Artist: "A",
			ImageURL: "https://i.scdn.co/image/09",
		})
	}

	p.handleQueueMetaUpdated()

	select {
	case u := <-updates:
		if !u.QueueIncluded || len(u.Queue) == 0 {
			t.Fatalf("delivery push must include the queue, got %+v", u)
		}
		for _, e := range u.Queue {
			if strings.TrimSpace(e.ImageURL) == "" {
				t.Fatalf("delivered queue entry %q carries no cover URL", e.ID)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery push after metadata completion")
	}
}

// The handler must also re-warm heads a batch didn't cover: skips taken
// while a batch is in flight leave new heads nothing else would warm.
// A failed batch warms nothing and signals nothing.
func TestHandleQueueMetaUpdatedRewarmsMissing(t *testing.T) {
	uriA, uriB, uriC := testQueueHeadURIs()
	p, _ := newQueueHeadSignalPlayer(t, []string{uriA, uriB, uriC})
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		return nil, errQueueHeadProbe
	}
	p.handleQueueMetaUpdated()
	select {
	case <-p.queueMetaUpdated:
		t.Fatal("failed batch must not arm a delivery signal")
	case <-time.After(300 * time.Millisecond):
	}
	if p.queueHeadWarmInFlight.Load() {
		t.Fatal("failed warm batch must release the in-flight flag")
	}
}

func newQueueHeadSignalPlayer(t *testing.T, uris []string) (*AppPlayer, <-chan *PlaybackStateUpdate) {
	t.Helper()
	ctxTracks := make([]*connectpb.ContextTrack, 0, len(uris))
	for _, uri := range uris {
		ctxTracks = append(ctxTracks, &connectpb.ContextTrack{Uri: uri})
	}
	tl, err := tracks.NewTrackListFromContext(context.Background(), &golibrespot.NullLogger{}, nil, &connectpb.Context{
		Uri:   "spotify:playlist:test",
		Pages: []*connectpb.ContextPage{{Tracks: ctxTracks}},
	}, 0)
	if err != nil {
		t.Fatalf("new track list: %v", err)
	}
	if !tl.GoStart(context.Background()) {
		t.Fatal("track list failed to start")
	}
	updates := make(chan *PlaybackStateUpdate, 8)
	p := &AppPlayer{
		runtime: &Runtime{
			Log:             noopLogger{},
			Cfg:             DefaultConfig(),
			PlaybackStateCh: updates,
		},
		baseCtx:          context.Background(),
		queueMetaUpdated: make(chan struct{}, 1),
	}
	p.state = &State{
		device: &connectpb.DeviceInfo{Volume: 32768},
		player: golibrespot.NewPlayerState(),
		tracks: tl,
	}
	return p, updates
}

// The success re-warm pin: heads missing after the handler's emit must be
// warmed by a follow-up batch (the rapid-skip case: the first batch covered
// stale heads), and once everything is imaged the loop must go quiet —
// no second batch, no second signal.
func TestHandleQueueMetaUpdatedRewarmsThenQuiets(t *testing.T) {
	uriA, uriB, uriC := testQueueHeadURIs()
	p, updates := newQueueHeadSignalPlayer(t, []string{uriA, uriB, uriC})
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod
	var fetches atomic.Int32
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		fetches.Add(1)
		out := make(map[string]spclient.ResolvedEntry, len(uris))
		for _, u := range uris {
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000, AlbumCoverFileId: []byte{9}}
		}
		return out, nil
	}

	p.handleQueueMetaUpdated()

	select {
	case <-updates:
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery push from handler")
	}
	deadline := time.Now().Add(2 * time.Second)
	for fetches.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fetches.Load() != 1 {
		t.Fatalf("re-warm batch did not run, fetches=%d", fetches.Load())
	}
	select {
	case <-p.queueMetaUpdated:
	case <-time.After(2 * time.Second):
		t.Fatal("completed re-warm batch signaled nothing")
	}

	// Second delivery with everything imaged: quiet — no batch, no signal.
	p.handleQueueMetaUpdated()
	select {
	case <-updates:
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery push from second handler call")
	}
	time.Sleep(300 * time.Millisecond)
	if n := fetches.Load(); n != 1 {
		t.Fatalf("warm heads re-fetched: fetches=%d, want 1", n)
	}
	select {
	case <-p.queueMetaUpdated:
		t.Fatal("fully-imaged re-warm must not signal")
	default:
	}
}

// A whole-context sweep must go out as bounded chunks, not one megarequest:
// every URI resolves, no chunk exceeds the cap, and the merge reports change.
func TestResolveQueueMetadataBatchChunksLargeSweep(t *testing.T) {
	p := newTestAppPlayer()
	p.resetQueueMetaForContext()
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod

	const total = queueMetaBatchChunk*2 + 7
	var mu sync.Mutex
	var chunkSizes []int
	seen := make(map[string]struct{})
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		mu.Lock()
		chunkSizes = append(chunkSizes, len(uris))
		mu.Unlock()
		out := make(map[string]spclient.ResolvedEntry, len(uris))
		for _, u := range uris {
			mu.Lock()
			seen[u] = struct{}{}
			mu.Unlock()
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000, AlbumCoverFileId: []byte{9}}
		}
		return out, nil
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	uris := make([]string, 0, total)
	for i := range total {
		// Valid 22-char base62 IDs: NormalizeSpotifyId decodes them.
		id := fmt.Sprintf("7GhIk7Il098yCjg4BQ%c%c", alphabet[(i/62)%62], alphabet[i%62])
		uris = append(uris, "spotify:track:"+id)
	}
	if !p.resolveQueueMetadataBatch(context.Background(), uris) {
		t.Fatal("large sweep must report changed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chunkSizes) != 3 {
		t.Fatalf("expected 3 chunks, got %v", chunkSizes)
	}
	for _, n := range chunkSizes {
		if n > queueMetaBatchChunk {
			t.Fatalf("chunk of %d exceeds the cap", n)
		}
	}
	if len(seen) != total {
		t.Fatalf("resolved %d of %d URIs", len(seen), total)
	}
}

// The beyond-head sweep must cover what the head window never touches, skip
// what is already imaged, and go quiet once the context is fully imaged.
func TestMaybeSweepQueueImagesWarmsBeyondHead(t *testing.T) {
	// Vary only the low-order chars of a known-good ID: a 22-char base62
	// string can exceed 128 bits and FillBytes panics on those.
	const sweepIDPrefix = "spotify:track:7GhIk7Il098yCjg4BQjz"
	suffixes := []string{"va", "vb", "vc", "vd", "ve", "vf", "vg", "vh", "vi", "vj", "vk", "vl", "vm", "vn", "vo", "vp"}
	uris := make([]string, 0, len(suffixes))
	for _, s := range suffixes {
		uris = append(uris, sweepIDPrefix+s)
	}
	p, _ := newQueueHeadSignalPlayer(t, uris)
	p.resetQueueMetaForContext()
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod
	// The head window is already warm (the head warm owns it): the sweep
	// must cover only the beyond-head region.
	for _, u := range uris[1 : headImageWindow+1] {
		id := golibrespot.NormalizeSpotifyId(u)
		p.setCachedQueueMeta(id, PlaybackStateQueueEntry{ID: id, Name: "N", Artist: "A", ImageURL: "https://i.scdn.co/image/09"})
	}
	var mu sync.Mutex
	var requested []string
	p.metaBatchFetch = func(ctx context.Context, batch []string) (map[string]spclient.ResolvedEntry, error) {
		mu.Lock()
		requested = append(requested, batch...)
		mu.Unlock()
		out := make(map[string]spclient.ResolvedEntry, len(batch))
		for _, u := range batch {
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000, AlbumCoverFileId: []byte{9}}
		}
		return out, nil
	}

	p.maybeSweepQueueImages()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(requested)
		mu.Unlock()
		if n >= len(uris) || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	got := append([]string(nil), requested...)
	mu.Unlock()
	// Upcoming excludes the playing head and the sweep skips the head
	// window outright (the head warm owns it): only the beyond-head
	// region may be requested. The now-playing cover arrives via the
	// current-track path, never the sweep.
	want := uris[headImageWindow+1:]
	if len(got) != len(want) {
		t.Fatalf("sweep requested %d URIs, want %d beyond-head: %v", len(got), len(want), got)
	}
	for _, u := range want {
		found := slices.Contains(got, u)
		if !found {
			t.Fatalf("sweep missed upcoming URI %s: %v", u, got)
		}
	}
	select {
	case <-p.queueMetaUpdated:
	case <-time.After(2 * time.Second):
		t.Fatal("completed sweep signaled nothing")
	}
	if p.queueSweepWarmInFlight.Load() {
		t.Fatal("completed sweep must release the in-flight flag")
	}

	// Fully imaged: the sweep must not warm anything again.
	p.handleQueueMetaUpdated()
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	n := len(requested)
	mu.Unlock()
	if n != len(uris)-headImageWindow-1 {
		t.Fatalf("imaged context re-swept: %d requests, want %d", n, len(uris)-headImageWindow-1)
	}
}
