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

// Prefetch fires only on ImageURL entries: heads must resolve images even when every entry has a name.
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
	// Window bounds resolved entries, not examined: A is already warm, so one slot still yields B.
	got = headImageURIs(upcoming, p.queueHeadImageMissing, 1)
	if len(got) != 1 || got[0] != uriB {
		t.Fatalf("expected [B] within window 1, got %v", got)
	}
	if got := headImageURIs(upcoming, p.queueHeadImageMissing, 0); len(got) != 0 {
		t.Fatalf("expected empty window to select nothing, got %v", got)
	}
}

// Named entries must enter the batch anyway or heads never gain ImageURL and the prefetch never fires.
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
	p.resolveContextQueueMetadata(context.Background(), all, []string{uriA, uriB}, "")

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

func TestResolveQueueMetadataBatchSkipsWarm(t *testing.T) {
	p := newTestAppPlayer()
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		t.Fatalf("fetch must not run for warm entries: %v", uris)
		return nil, nil
	}
	p.resolveQueueMetadataBatch(context.Background(), nil)
}

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

// Identical re-resolves are the steady state: staying silent is what prevents a re-resolve loop.
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

// The warm batch reaches the screen only via a queue re-push; the TUI prefetch fires on ImageURL entries.
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

// Mid-flight skips leave new heads nothing else would warm; a failed batch warms and signals nothing.
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

// Rapid-skip pin: the first batch can cover stale heads, so a follow-up re-warm must run, then go quiet.
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

// A whole-context sweep must chunk, not megarequest: every URI resolves and no chunk exceeds the cap.
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

// The sweep warms beyond the head window only, skips already-imaged entries, and goes quiet when fully imaged.
func TestMaybeSweepQueueImagesWarmsBeyondHead(t *testing.T) {
	// Vary only low-order chars: a 22-char base62 ID can exceed 128 bits and panic FillBytes.
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
	// Head window already warm: the sweep must cover only the beyond-head region.
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
	// Upcoming excludes the playing head and the sweep skips the head window; only beyond-head
	// URIs may be requested. The now-playing cover rides the current-track path, never the sweep.
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

func sweepChunkFillingURIs(total int) []string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	uris := make([]string, 0, total)
	for i := range total {
		id := fmt.Sprintf("7GhIk7Il098yCjg4BQ%c%c", alphabet[(i/62)%62], alphabet[i%62])
		uris = append(uris, "spotify:track:"+id)
	}
	return uris
}

func TestQueueMetaBatchClearsPendingOnFirstChunk(t *testing.T) {
	p := newTestAppPlayer()
	p.queueMetaUpdated = make(chan struct{}, 1)
	p.resetQueueMetaForContext()
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod

	uris := sweepChunkFillingURIs(queueMetaBatchChunk + 10)
	release := make(chan struct{})
	var mu sync.Mutex
	fetches := 0
	resolve := func(chunk []string) map[string]spclient.ResolvedEntry {
		out := make(map[string]spclient.ResolvedEntry, len(chunk))
		for _, u := range chunk {
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000}
		}
		return out
	}
	p.metaBatchFetch = func(ctx context.Context, chunk []string) (map[string]spclient.ResolvedEntry, error) {
		mu.Lock()
		fetches++
		n := fetches
		mu.Unlock()
		if n == 1 {
			return resolve(chunk), nil
		}
		<-release
		return resolve(chunk), nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.resolveQueueMetadataBatch(context.Background(), uris)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for p.queueMetaPending.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p.queueMetaPending.Load() {
		t.Fatal("first chunk must release the pending flag")
	}
	select {
	case <-done:
		t.Fatal("pending must release before the whole batch completes")
	default:
	}
	select {
	case <-p.queueMetaUpdated:
	default:
		t.Fatal("first chunk must signal a delivery")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("batch did not finish after the blocked chunk was released")
	}
}

func TestQueueMetaRetrySpendsBudgetThenStops(t *testing.T) {
	p := newTestAppPlayer()
	p.resetQueueMetaForContext()
	prod := testQueueProdInfo(t)
	p.prodInfo = &prod
	prevDelay := queueMetaRetryDelay
	queueMetaRetryDelay = 20 * time.Millisecond
	defer func() { queueMetaRetryDelay = prevDelay }()

	var fetches atomic.Int32
	p.metaBatchFetch = func(ctx context.Context, chunk []string) (map[string]spclient.ResolvedEntry, error) {
		fetches.Add(1)
		return nil, errQueueHeadProbe
	}
	if p.resolveQueueMetadataBatch(context.Background(), []string{"spotify:track:7GhIk7Il098yCjg4BQjzvb"}) {
		t.Fatal("failed batch must report unchanged")
	}
	if left := p.queueMetaRetriesLeft.Load(); left != int32(queueMetaRetryBudget)-1 {
		t.Fatalf("first failed chunk must spend one retry, budget=%d", left)
	}
	if !p.queueMetaPending.Load() {
		t.Fatal("failed batches must not clear the pending flag")
	}
	deadline := time.Now().Add(2 * time.Second)
	for fetches.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fetches.Load() != 3 {
		t.Fatalf("chained retry did not run, fetches=%d", fetches.Load())
	}
	time.Sleep(100 * time.Millisecond)
	if left := p.queueMetaRetriesLeft.Load(); left != 0 {
		t.Fatalf("second failure must drain the budget, left=%d", left)
	}
	if n := fetches.Load(); n != 3 {
		t.Fatalf("exhausted budget must stop retrying, fetches=%d", n)
	}
}

func TestResolveContextQueueMetadataOrdersUpcomingFirst(t *testing.T) {
	p := newTestAppPlayer()
	p.resetQueueMetaForContext()

	var requested []string
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		requested = append(requested, uris...)
		out := make(map[string]spclient.ResolvedEntry, len(uris))
		for _, u := range uris {
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000}
		}
		return out, nil
	}

	uris := []string{
		"spotify:track:0000000000000000000000",
		"spotify:track:1111111111111111111111",
		"spotify:track:2222222222222222222222",
		"spotify:track:3333333333333333333333",
	}
	var all []*connectpb.ProvidedTrack
	for _, u := range uris {
		all = append(all, &connectpb.ProvidedTrack{Uri: u})
	}
	p.resolveContextQueueMetadata(context.Background(), all, nil, uris[1])

	if len(requested) != len(uris) {
		t.Fatalf("expected %d resolutions, got %v", len(uris), requested)
	}
	if requested[0] != uris[2] {
		t.Fatalf("visible rows must resolve first, got %v", requested)
	}
	if requested[len(requested)-2] != uris[0] || requested[len(requested)-1] != uris[1] {
		t.Fatalf("played rows must resolve last, got %v", requested)
	}
}

func TestResolveQueueMetadataBatchCollectsFailedChunksIntoOneRetry(t *testing.T) {
	p := newTestAppPlayer()
	p.resetQueueMetaForContext()
	prevDelay := queueMetaRetryDelay
	queueMetaRetryDelay = 20 * time.Millisecond
	defer func() { queueMetaRetryDelay = prevDelay }()

	var mu sync.Mutex
	fetches := 0
	p.metaBatchFetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		mu.Lock()
		fetches++
		n := fetches
		mu.Unlock()
		if n <= 2 {
			return nil, errQueueHeadProbe
		}
		out := make(map[string]spclient.ResolvedEntry, len(uris))
		for _, u := range uris {
			out[u] = spclient.ResolvedEntry{Name: "N", Artist: "A", DurationMS: 200000}
		}
		return out, nil
	}

	uris := sweepChunkFillingURIs(queueMetaBatchChunk + 10)
	if p.resolveQueueMetadataBatch(context.Background(), uris) {
		t.Fatal("all-failed batch must report unchanged")
	}
	if left := p.queueMetaRetriesLeft.Load(); left != int32(queueMetaRetryBudget)-1 {
		t.Fatalf("one pass must spend exactly one retry, budget=%d", left)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := fetches
		mu.Unlock()
		if n >= 3 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	n := fetches
	mu.Unlock()
	if n != 4 {
		t.Fatalf("single retry must re-run every failed chunk once, fetches=%d", n)
	}
	if p.queueMetaPending.Load() {
		t.Fatal("recovered retry must clear the pending flag")
	}
}

func TestResetQueueMetaForContextRearmsBudgetAndPending(t *testing.T) {
	p := newTestAppPlayer()
	p.queueMetaRetriesLeft.Store(0)
	p.queueMetaPending.Store(false)
	p.resetQueueMetaForContext()
	if left := p.queueMetaRetriesLeft.Load(); left != int32(queueMetaRetryBudget) {
		t.Fatalf("context reset must rearm the retry budget, left=%d", left)
	}
	if !p.queueMetaPending.Load() {
		t.Fatal("context reset must arm the pending flag")
	}
}

func TestPlaybackStateCarriesQueueMetaPending(t *testing.T) {
	p, _ := newQueueHeadSignalPlayer(t, []string{"spotify:track:7GhIk7Il098yCjg4BQjzvb"})
	p.queueMetaPending.Store(true)
	out := p.BuildPlaybackStateUpdate()
	if !out.QueueMetaPending {
		t.Fatal("pending flag must ride a state push")
	}
	p.queueMetaPending.Store(false)
	if out := p.BuildPlaybackStateUpdate(); out.QueueMetaPending {
		t.Fatal("cleared flag must be absent from the push")
	}
}
