package tui

import (
	"fmt"
	"image"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func TestNormalizeSpotifyId(t *testing.T) {
	if got := golibrespot.NormalizeSpotifyId("spotify:track:7GhIk7Il098yCjg4BQjzvb"); got != "7GhIk7Il098yCjg4BQjzvb" {
		t.Fatalf("expected spotify URI to normalize to base62 id, got %q", got)
	}
	if got := golibrespot.NormalizeSpotifyId("plain-id"); got != "plain-id" {
		t.Fatalf("expected plain id unchanged, got %q", got)
	}
}

func TestMergeStatusFromPreviousUsesPreviousOnSameTrack(t *testing.T) {
	prev := &spotify.PlaybackStatus{
		TrackID:       "same",
		TrackName:     "Prev Name",
		ArtistName:    "Prev Artist",
		AlbumName:     "Prev Album",
		AlbumImageURL: "img",
		DurationMS:    12345,
	}
	next := &spotify.PlaybackStatus{TrackID: "same"}

	merged := mergeStatusFromPrevious(prev, nil, next)
	if merged.TrackName != "Prev Name" || merged.ArtistName != "Prev Artist" || merged.DurationMS != 12345 {
		t.Fatalf("expected previous metadata to be reused on same track, got %+v", merged)
	}
}

func TestMergeStatusFromPreviousUsesQueueFallback(t *testing.T) {
	next := &spotify.PlaybackStatus{TrackID: "track-1"}
	queue := []spotify.QueueItem{{ID: "track-1", Name: "Queue Name", Artist: "Queue Artist", DurationMS: 456}}

	merged := mergeStatusFromPrevious(nil, queue, next)
	if merged.TrackName != "Queue Name" || merged.ArtistName != "Queue Artist" || merged.DurationMS != 456 {
		t.Fatalf("expected queue fallback metadata, got %+v", merged)
	}
}

func TestMergeStatusFromPreviousUsesNonHeadQueueMatch(t *testing.T) {
	next := &spotify.PlaybackStatus{TrackID: "track-2"}
	queue := []spotify.QueueItem{
		{ID: "track-1", Name: "One", Artist: "A"},
		{ID: "track-2", Name: "Two", Artist: "B", DurationMS: 789},
	}
	merged := mergeStatusFromPrevious(nil, queue, next)
	if merged.TrackName != "Two" || merged.ArtistName != "B" || merged.DurationMS != 789 {
		t.Fatalf("expected queue match on track id, got %+v", merged)
	}
}

func TestMergeStatusFromPreviousDoesNotCarryAlbumImageURLOnTrackChange(t *testing.T) {
	prev := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "https://album-a.jpg"}
	next := &spotify.PlaybackStatus{TrackID: "track-2"}

	merged := mergeStatusFromPrevious(prev, nil, next)
	if merged.AlbumImageURL != "" {
		t.Fatalf("expected empty AlbumImageURL on track change when next has none, got %q", merged.AlbumImageURL)
	}
}

func TestMergeStatusFromPreviousCarriesAlbumImageURLOnSameTrack(t *testing.T) {
	prev := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "https://album-a.jpg"}
	next := &spotify.PlaybackStatus{TrackID: "track-1"}

	merged := mergeStatusFromPrevious(prev, nil, next)
	if merged.AlbumImageURL != "https://album-a.jpg" {
		t.Fatalf("expected prev AlbumImageURL carried on same-track push when next has none, got %q", merged.AlbumImageURL)
	}
}

func TestSeekSettleProgressUsesPendingAndSentTarget(t *testing.T) {
	m := model{
		transport: transportModel{
			seekDebouncePending: 15000,
			seekSentTarget:      10000,
			seekSentAt:          time.Now(),
			status:              &spotify.PlaybackStatus{ProgressMS: 5000},
		},
	}
	if got := m.seekSettleProgress(); got != 15000 {
		t.Fatalf("expected pending seek to win, got %d", got)
	}
	m.transport.seekDebouncePending = -1
	if got := m.seekSettleProgress(); got != 10000 {
		t.Fatalf("expected sent target while settling, got %d", got)
	}
}

func TestShouldApplySeekSettleRequiresSameTrack(t *testing.T) {
	m := model{
		transport: transportModel{
			seekSentTarget: 10000,
			seekSentAt:     time.Now(),
			status:         &spotify.PlaybackStatus{TrackID: "track-a"},
		},
	}
	if m.shouldApplySeekSettle(&spotify.PlaybackStatus{TrackID: "track-b"}) {
		t.Fatalf("expected seek settle to be skipped across track switch")
	}
	if !m.shouldApplySeekSettle(&spotify.PlaybackStatus{TrackID: "track-a", Playing: true}) {
		t.Fatalf("expected seek settle to apply on same track")
	}
}

func TestClampSeekTargetAvoidsExactEnd(t *testing.T) {
	m := model{transport: transportModel{status: &spotify.PlaybackStatus{DurationMS: 200000}}}
	if got := m.clampSeekTarget(200000); got != 199750 {
		t.Fatalf("expected clamp below duration end, got %d", got)
	}
}

func TestSeekSettleProgressClampsPendingAtEnd(t *testing.T) {
	m := model{
		transport: transportModel{
			status:              &spotify.PlaybackStatus{DurationMS: 10000},
			seekDebouncePending: 10000,
			seekSentTarget:      -1,
		},
	}
	if got := m.seekSettleProgress(); got != 9750 {
		t.Fatalf("expected pending settle target clamped below end, got %d", got)
	}
}

func TestSeekSettleProgressInterpolatedSentTargetClampsAtEnd(t *testing.T) {
	m := model{
		transport: transportModel{
			status:              &spotify.PlaybackStatus{DurationMS: 5000, Playing: true},
			seekDebouncePending: -1,
			seekSentTarget:      4900,
			seekSentAt:          time.Now().Add(-450 * time.Millisecond),
		},
	}
	if got := m.seekSettleProgress(); got != 4750 {
		t.Fatalf("expected interpolated settle target clamped below end, got %d", got)
	}
}

func TestShouldApplySeekSettleSkipsAfterWindowExpires(t *testing.T) {
	m := model{
		transport: transportModel{
			status:              &spotify.PlaybackStatus{TrackID: "track-a"},
			seekDebouncePending: -1,
			seekSentTarget:      10000,
			seekSentAt:          time.Now().Add(-seekSettleWindow - 10*time.Millisecond),
		},
	}
	if m.shouldApplySeekSettle(&spotify.PlaybackStatus{TrackID: "track-a"}) {
		t.Fatal("expected seek settle to skip once settle window expires")
	}
}

func TestClearSeekSettleTargetToleranceAndTimeout(t *testing.T) {
	m := model{
		transport: transportModel{
			seekSentTarget: 10000,
			seekSentAt:     time.Now(),
		},
	}
	m.clearSeekSettleTarget(10850)
	if m.transport.seekSentTarget != -1 {
		t.Fatalf("expected settle target to clear within tolerance, got %d", m.transport.seekSentTarget)
	}

	m = model{
		transport: transportModel{
			seekSentTarget: 10000,
			seekSentAt:     time.Now(),
		},
	}
	m.clearSeekSettleTarget(11000)
	if m.transport.seekSentTarget != 10000 {
		t.Fatalf("expected settle target to remain when outside tolerance, got %d", m.transport.seekSentTarget)
	}

	m.transport.seekSentAt = time.Now().Add(-seekSettleWindow - 10*time.Millisecond)
	m.clearSeekSettleTarget(0)
	if m.transport.seekSentTarget != -1 {
		t.Fatalf("expected settle target to clear after timeout, got %d", m.transport.seekSentTarget)
	}
}

func TestShouldApplyIncomingQueueClearsPendingContextQueue(t *testing.T) {
	m := model{
		transport: transportModel{
			pendingContextFrom:   "track-a",
			pendingContextFromAt: time.Now(),
			queue:                []spotify.QueueItem{{ID: "old"}},
			stableQueueLen:       1,
			queueHasMore:         true,
		},
	}
	if m.shouldApplyIncomingQueue("track-a") {
		t.Fatal("expected queue update to be gated for matching pending context")
	}
	if m.transport.queue != nil || m.transport.stableQueueLen != 0 || m.transport.queueHasMore {
		t.Fatalf("expected queue state reset while waiting for context switch, got queue=%v stable=%d hasMore=%t", m.transport.queue, m.transport.stableQueueLen, m.transport.queueHasMore)
	}
}

func TestShouldApplyIncomingQueueTimeoutAllowsApply(t *testing.T) {
	m := model{
		transport: transportModel{
			pendingContextFrom:   "track-a",
			pendingContextFromAt: time.Now().Add(-(pendingContextTimeout + time.Second)),
			queue:                []spotify.QueueItem{{ID: "old"}},
		},
	}
	if !m.shouldApplyIncomingQueue("track-a") {
		t.Fatal("expected timeout to override pending context guard and allow queue apply")
	}
	if m.transport.pendingContextFrom != "" {
		t.Fatal("expected pendingContextFrom to be cleared after timeout")
	}
}

func TestApplyMergedQueueClampsCursorAndTracksStableLen(t *testing.T) {
	m := model{
		transport: transportModel{
			status:      &spotify.PlaybackStatus{},
			queue:       []spotify.QueueItem{{ID: "old-1"}, {ID: "old-2"}, {ID: "old-3"}},
			queueCursor: 2,
		},
	}
	m.applyMergedQueue(
		[]spotify.QueueItem{
			{ID: "new-1", Name: "Track 1", Artist: "Artist 1"},
		},
		false,
		true,
		true,
	)

	if len(m.transport.queue) != 1 || m.transport.queue[0].ID != "new-1" {
		t.Fatalf("expected queue to be replaced by incoming entries, got %+v", m.transport.queue)
	}
	if m.transport.queueCursor != 0 {
		t.Fatalf("expected cursor to clamp to the surviving queue, got %d", m.transport.queueCursor)
	}
	if m.transport.stableQueueLen != 1 {
		t.Fatalf("expected stable length to track the merged queue, got %d", m.transport.stableQueueLen)
	}
}

func TestApplyMergedQueueReplacesQueueWithoutTailPreservation(t *testing.T) {
	prev := make([]spotify.QueueItem, 40)
	for i := range prev {
		prev[i] = spotify.QueueItem{ID: fmt.Sprintf("track-%d", i), Name: fmt.Sprintf("Track %d", i)}
	}
	next := []spotify.QueueItem{
		{ID: "next-a"},
		{ID: "next-b"},
	}
	m := model{
		transport: transportModel{
			status: &spotify.PlaybackStatus{ShuffleState: false},
			queue:  prev,
		},
	}
	m.applyMergedQueue(next, false, true, true)
	if len(m.transport.queue) != len(next) {
		t.Fatalf("expected queue to be replaced by incoming entries, got %d queue entries", len(m.transport.queue))
	}
}

func TestApplyMergedQueueDoesNotPreserveTailWhenShuffleTurnsOff(t *testing.T) {
	prev := make([]spotify.QueueItem, 40)
	for i := range prev {
		prev[i] = spotify.QueueItem{ID: fmt.Sprintf("track-%d", i), Name: fmt.Sprintf("Track %d", i)}
	}
	next := prev[:3]
	m := model{
		transport: transportModel{
			status: &spotify.PlaybackStatus{ShuffleState: true},
			queue:  prev,
		},
	}
	m.applyMergedQueue(next, false, true, true)
	if len(m.transport.queue) != len(next) {
		t.Fatalf("expected queue to match incoming length after shuffle toggle, got %d queue entries", len(m.transport.queue))
	}
}

func TestMergeQueueNamesDoesNotAppendTailEntries(t *testing.T) {
	prev := make([]spotify.QueueItem, 34)
	for i := range prev {
		prev[i] = spotify.QueueItem{ID: fmt.Sprintf("track-%d", i)}
	}
	next := []spotify.QueueItem{
		{ID: prev[0].ID},
		{ID: prev[1].ID},
		{ID: prev[33].ID},
	}

	merged := mergeQueueNames(prev, next)
	if len(merged) != len(next) {
		t.Fatalf("expected merged queue length to match incoming queue, got %d entries", len(merged))
	}
}

func TestShouldQueueAlbumImageLoad(t *testing.T) {
	prev := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "a"}
	if !shouldQueueAlbumImageLoad(nil, &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "a"}) {
		t.Fatal("expected initial non-empty image to load")
	}
	if shouldQueueAlbumImageLoad(prev, &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "a"}) {
		t.Fatal("expected same image URL to be skipped")
	}
	if !shouldQueueAlbumImageLoad(prev, &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "b"}) {
		t.Fatal("expected changed image URL to load")
	}
	if !shouldQueueAlbumImageLoad(prev, &spotify.PlaybackStatus{TrackID: "track-2", AlbumImageURL: "a"}) {
		t.Fatal("expected same image URL to load when track changes")
	}
	prevNoID := &spotify.PlaybackStatus{TrackName: "track-one", ArtistName: "artist", DurationMS: 180000, AlbumImageURL: "a"}
	nextNoID := &spotify.PlaybackStatus{TrackName: "track-two", ArtistName: "artist", DurationMS: 180000, AlbumImageURL: "a"}
	if !shouldQueueAlbumImageLoad(prevNoID, nextNoID) {
		t.Fatal("expected same image URL to load when metadata subject changes and track ids are missing")
	}
}

func TestShouldEnsureAlbumImageLoadWhenCoverNotCached(t *testing.T) {
	m := NewLoaderModel()
	prev := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	next := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	if !m.shouldEnsureAlbumImageLoad(prev, next) {
		t.Fatal("expected load when current track cover is not cached")
	}

	m.ui.imgs.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)), 0, 0)
	if m.shouldEnsureAlbumImageLoad(prev, next) {
		t.Fatal("expected no load when current track cover is already cached")
	}
}

func TestAdvancePlayerCoverEpochOnQueueHeadChange(t *testing.T) {
	m := NewLoaderModel()
	m.ui.imgs.protocol = imageProtocolKitty
	prev := &spotify.PlaybackStatus{AlbumImageURL: "u1", ProgressMS: 10000}
	next := &spotify.PlaybackStatus{AlbumImageURL: "u1", ProgressMS: 2000}

	m.advancePlayerCoverEpochIfNeeded(prev, next, "q1", "q2")
	if m.transport.playerCoverEpoch == 0 {
		t.Fatal("expected player cover epoch to advance when queue head changes")
	}
	// Retransmission flows through the intent revision, not a force flag:
	// the overlay commit path re-emits on the epoch change by itself.
	if m.ui.imgs.overlay.force {
		t.Fatal("expected no force flag when epoch advances; the revision drives retransmission")
	}
}

func TestAdvancePlayerCoverEpochNoChangeWhenSignalsMissing(t *testing.T) {
	m := NewLoaderModel()
	m.ui.imgs.protocol = imageProtocolKitty
	prev := &spotify.PlaybackStatus{AlbumImageURL: "u1", TrackID: "t1", ProgressMS: 10000}
	next := &spotify.PlaybackStatus{AlbumImageURL: "u1", TrackID: "t1", ProgressMS: 10200}

	m.advancePlayerCoverEpochIfNeeded(prev, next, "q1", "q1")
	if m.transport.playerCoverEpoch != 0 {
		t.Fatal("expected player cover epoch to remain unchanged")
	}
	if m.ui.imgs.overlay.force {
		t.Fatal("expected no kitty redraw force when transition signals are absent")
	}
}

func TestAdvancePlayerCoverEpochOnTrackChangeEvenWithEmptyURL(t *testing.T) {
	m := NewLoaderModel()
	m.ui.imgs.protocol = imageProtocolKitty
	prev := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "https://album-a.jpg"}
	next := &spotify.PlaybackStatus{TrackID: "track-2", AlbumImageURL: ""}

	m.advancePlayerCoverEpochIfNeeded(prev, next, "q1", "q1")
	if m.transport.playerCoverEpoch == 0 {
		t.Fatal("expected player cover epoch to advance on track change even when next push lacks an AlbumImageURL")
	}
	if m.ui.imgs.overlay.force {
		t.Fatal("expected no force flag on track change with empty URL; the clear path handles it")
	}
}

func TestTransportTransitionBlocksTransportKeys(t *testing.T) {
	m := model{ui: uiModel{keys: newKeys()}}
	m.beginTransportTransition()
	if !m.shouldBlockTransportInput(tea.KeyPressMsg{Code: 'n', Text: "n"}) {
		t.Fatal("expected transport key to be blocked while transition pending")
	}
	if m.shouldBlockTransportInput(tea.KeyPressMsg{Code: '?', Text: "?"}) {
		t.Fatal("expected non-transport key to remain allowed")
	}
}

func TestHandlePlaybackKeyQueuesSkipWhenBlocked(t *testing.T) {
	ch := make(chan librespot.TUICommand, 1)
	m := model{ui: uiModel{keys: newKeys()}, tuiCmdCh: ch}
	m.beginTransportTransition()
	next, _ := m.handlePlaybackKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
	got := next.(model)
	if len(got.transport.inputQueue) != 1 || got.transport.inputQueue[0].kind != playbackInputNext {
		t.Fatalf("expected one queued next input action, got %+v", got.transport.inputQueue)
	}
}

func TestExecutorStateTracksInFlightFlags(t *testing.T) {
	m := model{}
	m.syncExecutorState()
	if m.transport.executorState != executorStateIdle {
		t.Fatalf("expected idle executor, got %s", m.transport.executorState)
	}
	m.transport.transition.Begin(time.Now(), "")
	m.syncExecutorState()
	if m.transport.executorState != executorStateAwaitingTransport {
		t.Fatalf("expected awaiting-transport, got %s", m.transport.executorState)
	}
}

func TestInputQueueCoalescesSeekAndVolumeAndDedupsToggle(t *testing.T) {
	m := model{}
	m.enqueuePlaybackInput(playbackInputVolUp)
	m.enqueuePlaybackInput(playbackInputVolDown)
	m.enqueuePlaybackInput(playbackInputSeekBack)
	m.enqueuePlaybackInput(playbackInputSeekFwd)
	m.enqueuePlaybackInput(playbackInputShuffle)
	m.enqueuePlaybackInput(playbackInputShuffle)
	kinds := make([]playbackInputKind, 0, len(m.transport.inputQueue))
	for _, it := range m.transport.inputQueue {
		kinds = append(kinds, it.kind)
	}
	if len(kinds) != 3 || kinds[0] != playbackInputVolDown || kinds[1] != playbackInputSeekFwd || kinds[2] != playbackInputShuffle {
		t.Fatalf("unexpected queue policy result: %+v", kinds)
	}
}

func TestInputQueueDoesNotDedupLoopCycle(t *testing.T) {
	m := model{}
	m.enqueuePlaybackInput(playbackInputLoop)
	m.enqueuePlaybackInput(playbackInputLoop)
	if len(m.transport.inputQueue) != 2 || m.transport.inputQueue[0].kind != playbackInputLoop || m.transport.inputQueue[1].kind != playbackInputLoop {
		t.Fatalf("expected loop presses to be preserved for repeat cycling, got %+v", m.transport.inputQueue)
	}
}

func TestInputPriorityPrefersTransport(t *testing.T) {
	m := model{}
	m.enqueuePlaybackInput(playbackInputVolUp)
	m.enqueuePlaybackInput(playbackInputNext)
	if idx := m.dequeueNextInputIndex(); idx != 1 {
		t.Fatalf("expected transport action priority, got index %d", idx)
	}
}

func TestStuckTransportTransitionSetsPlaybackErr(t *testing.T) {
	m := model{}
	m.beginTransportTransition()
	m.transport.transition.startedAt = time.Now().Add(-5 * time.Second)
	m.maybeClearTransportTransition(&spotify.PlaybackStatus{TrackID: m.transport.transition.FromTrack()})
	if m.transport.transition.Pending() {
		t.Fatal("expected transition to clear on timeout")
	}
	if m.transport.playbackErr == nil {
		t.Fatal("expected playbackErr to be set after stuck transition")
	}
}

func TestStuckTransportTransitionErrorSurvivesPlaybackStateMsg(t *testing.T) {
	m := NewLoaderModel()
	m.beginTransportTransition()
	m.transport.transition.startedAt = time.Now().Add(-5 * time.Second)
	next, _ := m.handlePlaybackStateMsg(playbackStateMsg{
		seq:    1,
		status: &spotify.PlaybackStatus{TrackID: m.transport.transition.FromTrack()},
	})
	got := next.(model)
	if got.transport.transition.Pending() {
		t.Fatal("expected transition to clear on timeout")
	}
	if got.transport.playbackErr == nil {
		t.Fatal("expected stuck playbackErr to survive handlePlaybackStateMsg")
	}
	next, _ = got.handlePlaybackStateMsg(playbackStateMsg{
		seq:    2,
		status: &spotify.PlaybackStatus{TrackID: "other-track"},
	})
	if next.(model).transport.playbackErr != nil {
		t.Fatal("expected a later healthy push to clear the stuck error")
	}
}

func TestHandlePlaybackStateMsgIgnoresOutOfOrderSeq(t *testing.T) {
	m := NewLoaderModel()
	m.ui.lastPlaybackStateSeq = 10
	msg := playbackStateMsg{
		seq:    9,
		status: &spotify.PlaybackStatus{TrackID: "old"},
	}
	next, _ := m.handlePlaybackStateMsg(msg)
	got := next.(model)
	if got.transport.status != nil {
		t.Fatal("expected out-of-order playback state message to be ignored")
	}
}

func TestMergeStatusFromPreviousUsesQueueImageFallback(t *testing.T) {
	next := &spotify.PlaybackStatus{TrackID: "track-1", TrackName: "New Track", ArtistName: "A", DurationMS: 100}
	queue := []spotify.QueueItem{{ID: "track-1", Name: "New Track", ImageURL: "https://img/track-1"}}

	merged := mergeStatusFromPrevious(nil, queue, next)
	if merged.AlbumImageURL != "https://img/track-1" {
		t.Fatalf("expected queue image fallback for a track change with empty URL, got %+v", merged)
	}

	withURL := &spotify.PlaybackStatus{TrackID: "track-1", TrackName: "New Track", AlbumImageURL: "https://img/direct"}
	merged = mergeStatusFromPrevious(nil, queue, withURL)
	if merged.AlbumImageURL != "https://img/direct" {
		t.Fatalf("expected direct URL to win over queue fallback, got %+v", merged)
	}
}

func volSettleTestModel(vol int) model {
	m := model{ui: uiModel{keys: newKeys()}}
	m.transport.status = &spotify.PlaybackStatus{Volume: vol, TrackID: "t1", Playing: true}
	m.transport.volDebouncePending = -1
	m.transport.volSentTarget = -1
	return m
}

func TestVolumePushDuringPendingBurstKeepsOptimistic(t *testing.T) {
	m := volSettleTestModel(60)
	// Previous burst committed 50 a second ago (inside the settle window)
	// while a new burst is still pending at 65.
	m.transport.volSentTarget = 50
	m.transport.volSentAt = time.Now().Add(-1 * time.Second)
	m.transport.volDebouncePending = 65
	incoming := &spotify.PlaybackStatus{Volume: 50, TrackID: "t1", Playing: true, DurationMS: 200000, ProgressMS: 1000}
	out, _ := m.handlePlaybackStateMsg(playbackStateMsg{status: incoming})
	if got := out.(model).transport.status.Volume; got != 60 {
		t.Fatalf("push during pending burst snapped the bar to %d, want optimistic 60", got)
	}
}

func TestVolumePushAfterCommitPinsToSentTarget(t *testing.T) {
	m := volSettleTestModel(60)
	// Burst fully committed a second ago: divergent pushes (e.g. another
	// client) stay pinned for the settle window, as before.
	m.transport.volSentTarget = 60
	m.transport.volSentAt = time.Now().Add(-1 * time.Second)
	incoming := &spotify.PlaybackStatus{Volume: 55, TrackID: "t1", Playing: true, DurationMS: 200000, ProgressMS: 1000}
	out, _ := m.handlePlaybackStateMsg(playbackStateMsg{status: incoming})
	if got := out.(model).transport.status.Volume; got != 60 {
		t.Fatalf("push after commit did not pin to sent target: %d", got)
	}
}
