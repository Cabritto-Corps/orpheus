package librespot

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/ap"
	"github.com/elxgy/go-librespot/audio"
	"github.com/elxgy/go-librespot/dealer"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
	metadatapb "github.com/elxgy/go-librespot/proto/spotify/metadata"
	"github.com/elxgy/go-librespot/spclient"
	"github.com/elxgy/go-librespot/tracks"

	"orpheus/internal/cache"
)

type fakeAudioSource struct {
	mu     sync.Mutex
	closed bool
}

func (f *fakeAudioSource) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeAudioSource) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeAudioSource) SetPositionMs(int64) error { return nil }

func (f *fakeAudioSource) PositionMs() int64 { return 0 }

// Read ends immediately, like a decoder at end of media.
func (f *fakeAudioSource) Read([]float32) (int, error) { return 0, io.EOF }

type fakeSession struct{ events player.EventManager }

func (s fakeSession) Events() player.EventManager  { return s.events }
func (s fakeSession) Spclient() *spclient.Spclient { return nil }
func (s fakeSession) Dealer() *dealer.Dealer       { return nil }
func (s fakeSession) Accesspoint() *ap.Accesspoint { return nil }

type noopEventManager struct{}

func (noopEventManager) PreStreamLoadNew([]byte, golibrespot.SpotifyId, int64) {}
func (noopEventManager) PostStreamResolveAudioFile([]byte, int32, *golibrespot.Media, *metadatapb.AudioFile) {
}
func (noopEventManager) PostStreamRequestAudioKey([]byte)                               {}
func (noopEventManager) PostStreamResolveStorage([]byte)                                {}
func (noopEventManager) PostStreamInitHttpChunkReader([]byte, *audio.HttpChunkedReader) {}
func (noopEventManager) OnPrimaryStreamUnload(*player.Stream, int64)                    {}
func (noopEventManager) PostPrimaryStreamLoad(*player.Stream, bool)                     {}
func (noopEventManager) OnPlayerPlay(*player.Stream, string, bool, *connectpb.PlayOrigin, *connectpb.ProvidedTrack, int64) {
}
func (noopEventManager) OnPlayerResume(*player.Stream, int64) {}
func (noopEventManager) OnPlayerPause(*player.Stream, string, bool, *connectpb.PlayOrigin, *connectpb.ProvidedTrack, int64) {
}
func (noopEventManager) OnPlayerSeek(*player.Stream, int64, int64)       {}
func (noopEventManager) OnPlayerSkipForward(*player.Stream, int64, bool) {}
func (noopEventManager) OnPlayerSkipBackward(*player.Stream, int64)      {}
func (noopEventManager) OnPlayerEnd(*player.Stream, int64)               {}
func (noopEventManager) Close()                                          {}

// newPipeBackedPlayer builds a real player whose output writes to a file, so
// the stream-slot bookkeeping runs exactly as it does in production.
func newPipeBackedPlayer(t *testing.T) *player.Player {
	t.Helper()

	outPath := filepath.Join(t.TempDir(), "audio.raw")
	if err := os.WriteFile(outPath, nil, 0o600); err != nil {
		t.Fatalf("create output file: %v", err)
	}
	pl, err := player.NewPlayer(&player.Options{
		Log:                   noopLogger{},
		Events:                noopEventManager{},
		AudioBackend:          "pipe",
		AudioOutputPipe:       outPath,
		AudioOutputPipeFormat: "s16le",
		VolumeUpdate:          make(chan float32, 4),
	})
	if err != nil {
		t.Fatalf("new player: %v", err)
	}
	t.Cleanup(pl.Close)
	return pl
}

func newSkipTestStream(t *testing.T, uri string, durationMs int32) (*player.Stream, *fakeAudioSource) {
	t.Helper()

	id, err := golibrespot.SpotifyIdFromUri(uri)
	if err != nil {
		t.Fatalf("parse uri %s: %v", uri, err)
	}
	name, duration := "test", durationMs
	src := &fakeAudioSource{}
	return &player.Stream{
		Media: golibrespot.NewMediaFromTrack(&metadatapb.Track{
			Gid:      id.Id(),
			Name:     &name,
			Duration: &duration,
		}),
		Source: src,
	}, src
}

func newSkipTestPlayer(t *testing.T, pl *player.Player, uris []string) (*AppPlayer, <-chan *PlaybackStateUpdate) {
	t.Helper()

	tracksInCtx := make([]*connectpb.ContextTrack, 0, len(uris))
	for _, uri := range uris {
		tracksInCtx = append(tracksInCtx, &connectpb.ContextTrack{Uri: uri})
	}
	tl, err := tracks.NewTrackListFromContext(context.Background(), &golibrespot.NullLogger{}, nil, &connectpb.Context{
		Uri:   "spotify:playlist:test",
		Pages: []*connectpb.ContextPage{{Tracks: tracksInCtx}},
	}, 0)
	if err != nil {
		t.Fatalf("new track list: %v", err)
	}

	updates := make(chan *PlaybackStateUpdate, 8)
	p := &AppPlayer{
		runtime: &Runtime{
			Log:             noopLogger{},
			Cfg:             DefaultConfig(),
			PlaybackStateCh: updates,
		},
		sess:                fakeSession{events: noopEventManager{}},
		baseCtx:             context.Background(),
		player:              pl,
		volumeUpdate:        make(chan float32, 1),
		prefetchTimer:       time.NewTimer(time.Hour),
		shuffleRefreshTimer: time.NewTimer(time.Hour),
		connectStateTimer:   time.NewTimer(time.Hour),
		queueTopUpTimer:     time.NewTimer(time.Hour),
		transitionCache:     newTransitionCache(),
		queueMetaCache:      cache.NewLRU[string, PlaybackStateQueueEntry](8),
	}
	p.state = &State{
		device: golibrespot.DefaultDeviceInfo(golibrespot.DeviceInfoOpts{
			DeviceName:  "test",
			DeviceType:  devicespb.DeviceType_COMPUTER,
			ClientId:    golibrespot.ClientIdHex,
			VolumeSteps: p.runtime.Cfg.VolumeSteps,
		}),
		player: golibrespot.NewPlayerState(),
		tracks: tl,
	}
	// A fresh track list has no current track until playback starts on it.
	tl.GoStart(context.Background())
	p.syncPlayerTrackState(tl, nil)
	return p, updates
}

func waitClosed(t *testing.T, src *fakeAudioSource) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !src.isClosed() {
		if time.Now().After(deadline) {
			t.Fatal("expected stream to be closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A manual skip lands on the prefetched track mid-track, before the audio
// source has auto-promoted it. Promoting it must not close it: clearing the
// player's secondary slot first closes the displaced source, which is the
// very stream being promoted.
func TestSkipNextKeepsPrefetchedStreamAlive(t *testing.T) {
	currentURI := "spotify:track:0000000000000000000000"
	nextURI := "spotify:track:0000000000000000000001"

	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{currentURI, nextURI})

	current, currentSrc := newSkipTestStream(t, currentURI, 30_000)
	prefetched, prefetchedSrc := newSkipTestStream(t, nextURI, 30_000)
	p.primaryStream = current
	p.secondaryStream = prefetched
	// Mirror handlePrefetchResult: the prefetched stream is aliased into the
	// player's secondary slot while the current track is still playing.
	p.player.SetSecondaryStream(prefetched.Source)

	if err := p.skipNext(context.Background(), nil); err != nil {
		t.Fatalf("skipNext: %v", err)
	}

	if prefetchedSrc.isClosed() {
		t.Fatal("skip closed the prefetched stream while promoting it")
	}
	if p.primaryStream != prefetched {
		t.Fatal("expected the prefetched stream to become the primary stream")
	}
	if p.secondaryStream != nil {
		t.Fatal("expected the secondary slot to be empty after promotion")
	}
	waitClosed(t, currentSrc)
}

// A skip whose load fails must still push playback state: the TUI holds a
// transport transition open and only releases it when an update arrives.
func TestSkipNextEmitsStateWhenLoadFails(t *testing.T) {
	currentURI := "spotify:track:0000000000000000000000"

	p, updates := newSkipTestPlayer(t, nil, []string{currentURI, "not-a-spotify-uri"})
	current, _ := newSkipTestStream(t, currentURI, 30_000)
	p.primaryStream = current

	if err := p.skipNext(context.Background(), nil); err == nil {
		t.Fatal("expected skipNext to fail on an unloadable track")
	}

	select {
	case u := <-updates:
		if u == nil {
			t.Fatal("expected a non-nil playback state update")
		}
	default:
		t.Fatal("expected a playback state push so the TUI can leave the awaiting-transport state")
	}
}

func TestDeadTrackMemoryBound(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)

	if p.isDeadTrack("spotify:track:0000000000000000000000") {
		t.Fatal("empty memory must know nothing")
	}
	p.rememberDeadTrack("")
	if p.isDeadTrack("") {
		t.Fatal("empty URIs must never be recorded")
	}

	uri := "spotify:track:0000000000000000000000"
	p.rememberDeadTrack(uri)
	p.rememberDeadTrack(uri)
	if !p.isDeadTrack(uri) {
		t.Fatal("recorded URI must be dead")
	}
	if len(p.deadTrackOrder) != 1 {
		t.Fatalf("duplicate records must not grow the order, got %d", len(p.deadTrackOrder))
	}

	for i := range deadTrackMemoryCap + 10 {
		p.rememberDeadTrack("spotify:track:dead" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	if len(p.deadTrackOrder) != deadTrackMemoryCap {
		t.Fatalf("memory must stay capped at %d, got %d", deadTrackMemoryCap, len(p.deadTrackOrder))
	}
	if len(p.deadTracks) != deadTrackMemoryCap {
		t.Fatalf("map and order must agree, got %d vs %d", len(p.deadTracks), len(p.deadTrackOrder))
	}
	if p.isDeadTrack(uri) {
		t.Fatal("oldest entry must have been evicted past the cap")
	}

	p.clearDeadTracks()
	if p.isDeadTrack("spotify:track:deadab") || len(p.deadTrackOrder) != 0 {
		t.Fatal("clear must empty the memory")
	}
}

func TestIsUnplayableMediaError(t *testing.T) {
	if !isUnplayableMediaError(wrapForTest(t, golibrespot.ErrMediaRestricted)) {
		t.Fatal("restricted media is permanently unplayable")
	}
	if !isUnplayableMediaError(wrapForTest(t, golibrespot.ErrNoSupportedFormats)) {
		t.Fatal("unsupported formats are permanently unplayable")
	}
	if isUnplayableMediaError(context.DeadlineExceeded) {
		t.Fatal("transient failures must stay retryable")
	}
	if isUnplayableMediaError(nil) {
		t.Fatal("nil is not a failure")
	}
}

func wrapForTest(t *testing.T, err error) error {
	t.Helper()
	return &testWrappingError{err: err}
}

type testWrappingError struct{ err error }

func (e *testWrappingError) Error() string { return "wrapped: " + e.err.Error() }
func (e *testWrappingError) Unwrap() error { return e.err }

// A context with more dead tracks ahead than the skip cap must still reach
// the live track: known-dead tracks are stepped over without a load attempt
// and without burning skip attempts. Without the memory the loop gives up
// after maxRetries and the live track never plays.
func TestAdvanceNextSkipsKnownDeadWithoutBurningAttempts(t *testing.T) {
	const liveIdx = 12
	var uris []string
	for i := 0; i <= liveIdx; i++ {
		uris = append(uris, "spotify:track:40000000000000000000"+string([]byte{'0' + byte(i/10), '0' + byte(i%10)}))
	}

	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), uris)
	current, _ := newSkipTestStream(t, uris[0], 30_000)
	p.primaryStream = current
	for i := 1; i <= liveIdx; i++ {
		stream, _ := newSkipTestStream(t, uris[i], 30_000)
		id, err := golibrespot.SpotifyIdFromUri(uris[i])
		if err != nil {
			t.Fatalf("parse uri: %v", err)
		}
		if !p.putTransitionCachedStream(*id, stream) {
			t.Fatalf("seed cache for %s", uris[i])
		}
	}
	// Eleven dead tracks ahead with a cap of ten skip attempts: only the
	// no-burn memory can cross them.
	for i := 1; i < liveIdx; i++ {
		p.rememberDeadTrack(uris[i])
	}

	hasNext, err := p.advanceNext(context.Background(), true, false)
	if err != nil {
		t.Fatalf("advanceNext: %v", err)
	}
	if !hasNext {
		t.Fatal("expected to reach the live track, not exhaust the queue")
	}
	if !p.state.player.IsPlaying {
		t.Fatal("expected playback to continue on the live track, not give up")
	}
	if got := p.state.player.Track.Uri; got != uris[liveIdx] {
		t.Fatalf("expected to land on %s, got %s", uris[liveIdx], got)
	}
	// The dead tracks were never loaded: their streams sit untouched.
	id1, _ := golibrespot.SpotifyIdFromUri(uris[1])
	if !p.hasTransitionCachedStream(*id1) {
		t.Fatal("known-dead streams must not be promoted")
	}
}
