package tui

import (
	"context"
	"fmt"
	"image"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/cache"
	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func TestNormalizeSpotifyId(t *testing.T) {
	// Valid Spotify URI: 22-char base62 ID
	if got := golibrespot.NormalizeSpotifyId("spotify:track:7GhIk7Il098yCjg4BQjzvb"); got != "7GhIk7Il098yCjg4BQjzvb" {
		t.Fatalf("expected spotify URI to normalize to base62 id, got %q", got)
	}
	// Non-URI strings pass through unchanged
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

	merged := mergeStatusFromPrevious(prev, nil, next, nil)
	if merged.TrackName != "Prev Name" || merged.ArtistName != "Prev Artist" || merged.DurationMS != 12345 {
		t.Fatalf("expected previous metadata to be reused on same track, got %+v", merged)
	}
}

func TestMergeStatusFromPreviousUsesQueueFallback(t *testing.T) {
	next := &spotify.PlaybackStatus{TrackID: "track-1"}
	queue := []spotify.QueueItem{{ID: "track-1", Name: "Queue Name", Artist: "Queue Artist", DurationMS: 456}}

	merged := mergeStatusFromPrevious(nil, queue, next, nil)
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
	merged := mergeStatusFromPrevious(nil, queue, next, nil)
	if merged.TrackName != "Two" || merged.ArtistName != "B" || merged.DurationMS != 789 {
		t.Fatalf("expected queue match on track id, got %+v", merged)
	}
}

func TestMergeStatusFromPreviousUsesTrackCacheFallback(t *testing.T) {
	cache := cache.NewTTL[string, spotify.QueueItem](16, time.Hour)
	cache.Set("cached-track", spotify.QueueItem{Name: "Cached Name", Artist: "Cached Artist", DurationMS: 654})
	next := &spotify.PlaybackStatus{TrackID: "cached-track"}
	merged := mergeStatusFromPrevious(nil, nil, next, cache)
	if merged.TrackName != "Cached Name" || merged.ArtistName != "Cached Artist" || merged.DurationMS != 654 {
		t.Fatalf("expected cache fallback metadata, got %+v", merged)
	}
}

func TestMergeStatusFromPreviousFillsAlbumImageURLOnTrackChange(t *testing.T) {
	prev := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "https://same-album.jpg"}
	next := &spotify.PlaybackStatus{TrackID: "track-2"}

	merged := mergeStatusFromPrevious(prev, nil, next, nil)
	if merged.AlbumImageURL != "https://same-album.jpg" {
		t.Fatalf("expected AlbumImageURL from prev on track change when next has none, got %q", merged.AlbumImageURL)
	}
}

func TestSeekSettleProgressUsesPendingAndSentTarget(t *testing.T) {
	m := model{
		seekDebouncePending: 15000,
		seekSentTarget:      10000,
		seekSentAt:          time.Now(),
		status:              &spotify.PlaybackStatus{ProgressMS: 5000},
	}
	if got := m.seekSettleProgress(); got != 15000 {
		t.Fatalf("expected pending seek to win, got %d", got)
	}
	m.seekDebouncePending = -1
	if got := m.seekSettleProgress(); got != 10000 {
		t.Fatalf("expected sent target while settling, got %d", got)
	}
}

func TestShouldApplySeekSettleRequiresSameTrack(t *testing.T) {
	m := model{
		seekSentTarget: 10000,
		seekSentAt:     time.Now(),
		status:         &spotify.PlaybackStatus{TrackID: "track-a"},
	}
	if m.shouldApplySeekSettle(&spotify.PlaybackStatus{TrackID: "track-b"}) {
		t.Fatalf("expected seek settle to be skipped across track switch")
	}
	if !m.shouldApplySeekSettle(&spotify.PlaybackStatus{TrackID: "track-a"}) {
		t.Fatalf("expected seek settle to apply on same track")
	}
}

func TestClampSeekTargetAvoidsExactEnd(t *testing.T) {
	m := model{status: &spotify.PlaybackStatus{DurationMS: 200000}}
	if got := m.clampSeekTarget(200000); got != 199750 {
		t.Fatalf("expected clamp below duration end, got %d", got)
	}
}

func TestSeekSettleProgressClampsPendingAtEnd(t *testing.T) {
	m := model{
		status:              &spotify.PlaybackStatus{DurationMS: 10000},
		seekDebouncePending: 10000,
		seekSentTarget:      -1,
	}
	if got := m.seekSettleProgress(); got != 9750 {
		t.Fatalf("expected pending settle target clamped below end, got %d", got)
	}
}

func TestSeekSettleProgressInterpolatedSentTargetClampsAtEnd(t *testing.T) {
	m := model{
		status:              &spotify.PlaybackStatus{DurationMS: 5000, Playing: true},
		seekDebouncePending: -1,
		seekSentTarget:      4900,
		seekSentAt:          time.Now().Add(-450 * time.Millisecond),
	}
	if got := m.seekSettleProgress(); got != 4750 {
		t.Fatalf("expected interpolated settle target clamped below end, got %d", got)
	}
}

func TestShouldApplySeekSettleSkipsAfterWindowExpires(t *testing.T) {
	m := model{
		status:              &spotify.PlaybackStatus{TrackID: "track-a"},
		seekDebouncePending: -1,
		seekSentTarget:      10000,
		seekSentAt:          time.Now().Add(-seekSettleWindow - 10*time.Millisecond),
	}
	if m.shouldApplySeekSettle(&spotify.PlaybackStatus{TrackID: "track-a"}) {
		t.Fatal("expected seek settle to skip once settle window expires")
	}
}

func TestClearSeekSettleTargetToleranceAndTimeout(t *testing.T) {
	m := model{
		seekSentTarget: 10000,
		seekSentAt:     time.Now(),
	}
	m.clearSeekSettleTarget(10850)
	if m.seekSentTarget != -1 {
		t.Fatalf("expected settle target to clear within tolerance, got %d", m.seekSentTarget)
	}

	m = model{
		seekSentTarget: 10000,
		seekSentAt:     time.Now(),
	}
	m.clearSeekSettleTarget(11000)
	if m.seekSentTarget != 10000 {
		t.Fatalf("expected settle target to remain when outside tolerance, got %d", m.seekSentTarget)
	}

	m.seekSentAt = time.Now().Add(-seekSettleWindow - 10*time.Millisecond)
	m.clearSeekSettleTarget(0)
	if m.seekSentTarget != -1 {
		t.Fatalf("expected settle target to clear after timeout, got %d", m.seekSentTarget)
	}
}

func TestShouldApplyIncomingQueueClearsPendingContextQueue(t *testing.T) {
	m := model{
		pendingContextFrom:   "track-a",
		pendingContextFromAt: time.Now(),
		queue:                []spotify.QueueItem{{ID: "old"}},
		stableQueueLen:       1,
		queueHasMore:         true,
	}
	if m.shouldApplyIncomingQueue("track-a") {
		t.Fatal("expected queue update to be gated for matching pending context")
	}
	if m.queue != nil || m.stableQueueLen != 0 || m.queueHasMore {
		t.Fatalf("expected queue state reset while waiting for context switch, got queue=%v stable=%d hasMore=%t", m.queue, m.stableQueueLen, m.queueHasMore)
	}
}

func TestShouldApplyIncomingQueueTimeoutAllowsApply(t *testing.T) {
	m := model{
		pendingContextFrom:   "track-a",
		pendingContextFromAt: time.Now().Add(-(pendingContextTimeout + time.Second)),
		queue:                []spotify.QueueItem{{ID: "old"}},
	}
	if !m.shouldApplyIncomingQueue("track-a") {
		t.Fatal("expected timeout to override pending context guard and allow queue apply")
	}
	if m.pendingContextFrom != "" {
		t.Fatal("expected pendingContextFrom to be cleared after timeout")
	}
}

func TestApplyMergedQueueRebuildsPreloadedIDs(t *testing.T) {
	m := model{
		status:           &spotify.PlaybackStatus{},
		queue:            []spotify.QueueItem{{ID: "spotify:track:7GhIk7Il098yCjg4BQjzvb"}},
		preloadedItemIDs: map[string]struct{}{"7GhIk7Il098yCjg4BQjzvb": {}, "ghost": {}},
		trackCache:       cache.NewTTL[string, spotify.QueueItem](16, time.Hour),
	}
	m.applyMergedQueue(
		[]spotify.QueueItem{
			{ID: "spotify:track:2WfaOiMkCvy7F5fcp2zZ8L", Name: "Track 1", Artist: "Artist 1"},
			{ID: "plain-2", Name: "Track 2", Artist: "Artist 2"},
		},
		false,
		true,
		true,
		false,
	)

	if _, ok := m.preloadedItemIDs["2WfaOiMkCvy7F5fcp2zZ8L"]; !ok {
		t.Fatal("expected normalized spotify id to be preloaded")
	}
	if _, ok := m.preloadedItemIDs["plain-2"]; !ok {
		t.Fatal("expected plain id to be preloaded")
	}
	if _, ok := m.preloadedItemIDs["7GhIk7Il098yCjg4BQjzvb"]; ok {
		t.Fatal("expected stale preloaded ids to be removed")
	}
	if len(m.preloadedItemIDs) != 2 {
		t.Fatalf("expected preloaded id set to rebuild from merged queue, got %d entries", len(m.preloadedItemIDs))
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
		status:           &spotify.PlaybackStatus{ShuffleState: false},
		queue:            prev,
		preloadedItemIDs: make(map[string]struct{}),
		trackCache:       cache.NewTTL[string, spotify.QueueItem](16, time.Hour),
	}
	m.applyMergedQueue(next, false, true, true, false)
	if len(m.queue) != len(next) {
		t.Fatalf("expected queue to be replaced by incoming entries, got %d queue entries", len(m.queue))
	}
}

func TestApplyMergedQueueDoesNotPreserveTailWhenShuffleTurnsOff(t *testing.T) {
	prev := make([]spotify.QueueItem, 40)
	for i := range prev {
		prev[i] = spotify.QueueItem{ID: fmt.Sprintf("track-%d", i), Name: fmt.Sprintf("Track %d", i)}
	}
	next := prev[:3]
	m := model{
		status:           &spotify.PlaybackStatus{ShuffleState: true},
		queue:            prev,
		preloadedItemIDs: make(map[string]struct{}),
		trackCache:       cache.NewTTL[string, spotify.QueueItem](16, time.Hour),
	}
	m.applyMergedQueue(next, false, true, true, false)
	if len(m.queue) != len(next) {
		t.Fatalf("expected queue to match incoming length after shuffle toggle, got %d queue entries", len(m.queue))
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

	merged := mergeQueueNames(prev, next, nil)
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
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	prev := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	next := &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	if !m.shouldEnsureAlbumImageLoad(prev, next) {
		t.Fatal("expected load when current track cover is not cached")
	}

	m.imgs.setImage("u1", image.NewRGBA(image.Rect(0, 0, 2, 2)), 0, 0)
	if m.shouldEnsureAlbumImageLoad(prev, next) {
		t.Fatal("expected no load when current track cover is already cached")
	}
}

func TestAdvancePlayerCoverEpochOnQueueHeadChange(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.imgs.protocol = imageProtocolKitty
	prev := &spotify.PlaybackStatus{AlbumImageURL: "u1", ProgressMS: 10000}
	next := &spotify.PlaybackStatus{AlbumImageURL: "u1", ProgressMS: 2000}

	m.advancePlayerCoverEpochIfNeeded(prev, next, "q1", "q2")
	if m.playerCoverEpoch == 0 {
		t.Fatal("expected player cover epoch to advance when queue head changes")
	}
	if !m.imgs.kittyForceRedraw {
		t.Fatal("expected kitty redraw to be forced when epoch advances")
	}
}

func TestAdvancePlayerCoverEpochNoChangeWhenSignalsMissing(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.imgs.protocol = imageProtocolKitty
	prev := &spotify.PlaybackStatus{AlbumImageURL: "u1", TrackID: "t1", ProgressMS: 10000}
	next := &spotify.PlaybackStatus{AlbumImageURL: "u1", TrackID: "t1", ProgressMS: 10200}

	m.advancePlayerCoverEpochIfNeeded(prev, next, "q1", "q1")
	if m.playerCoverEpoch != 0 {
		t.Fatal("expected player cover epoch to remain unchanged")
	}
	if m.imgs.kittyForceRedraw {
		t.Fatal("expected no kitty redraw force when transition signals are absent")
	}
}

func TestTransportTransitionBlocksTransportKeys(t *testing.T) {
	m := model{keys: newKeys()}
	m.beginTransportTransition()
	if !m.shouldBlockTransportInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}) {
		t.Fatal("expected transport key to be blocked while transition pending")
	}
	if m.shouldBlockTransportInput(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}) {
		t.Fatal("expected non-transport key to remain allowed")
	}
}

func TestHandlePlaybackKeyQueuesSkipWhenBlocked(t *testing.T) {
	ch := make(chan librespot.TUICommand, 1)
	m := model{keys: newKeys(), tuiCmdCh: ch}
	m.beginTransportTransition()
	next, _ := m.handlePlaybackKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	got := next.(model)
	if len(got.inputQueue) != 1 || got.inputQueue[0].kind != playbackInputNext {
		t.Fatalf("expected one queued next input action, got %+v", got.inputQueue)
	}
}

func TestExecutorStateTracksInFlightFlags(t *testing.T) {
	m := model{}
	m.syncExecutorState()
	if m.executorState != executorStateIdle {
		t.Fatalf("expected idle executor, got %s", m.executorState)
	}
	m.actionInFlight = true
	m.syncExecutorState()
	if m.executorState != executorStateAwaitingAction {
		t.Fatalf("expected awaiting-action, got %s", m.executorState)
	}
	m.actionInFlight = false
	m.transportTransitionPending = true
	m.syncExecutorState()
	if m.executorState != executorStateAwaitingTransport {
		t.Fatalf("expected awaiting-transport, got %s", m.executorState)
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
	kinds := make([]playbackInputKind, 0, len(m.inputQueue))
	for _, it := range m.inputQueue {
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
	if len(m.inputQueue) != 2 || m.inputQueue[0].kind != playbackInputLoop || m.inputQueue[1].kind != playbackInputLoop {
		t.Fatalf("expected loop presses to be preserved for repeat cycling, got %+v", m.inputQueue)
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

func TestStuckTransportTransitionSetsRecovery(t *testing.T) {
	m := model{}
	m.beginTransportTransition()
	m.transportTransitionStartedAt = time.Now().Add(-5 * time.Second)
	m.maybeClearTransportTransition(&spotify.PlaybackStatus{TrackID: m.transportTransitionFromTrack})
	if m.transportTransitionPending {
		t.Fatal("expected transition to clear on timeout")
	}
	if !m.transportRecoveryPending {
		t.Fatal("expected recovery pending after stuck transition")
	}
	if m.transportStuckCount != 1 {
		t.Fatalf("expected stuck count to increment, got %d", m.transportStuckCount)
	}
}

func TestHandlePollMsgIgnoresStaleToken(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.stateFetchToken = 3
	m.playbackErr = nil
	msg := pollMsg{
		token: 2,
		status: &spotify.PlaybackStatus{
			TrackID: "stale-track",
		},
	}
	next, _ := m.handlePollMsg(msg)
	got := next.(model)
	if got.status != nil {
		t.Fatal("expected stale poll message to be ignored")
	}
}

func TestHandleActionReconcileMsgIgnoresStaleToken(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.stateFetchToken = 5
	m.status = &spotify.PlaybackStatus{TrackID: "current"}
	msg := actionReconcileMsg{
		token: 4,
		status: &spotify.PlaybackStatus{
			TrackID: "stale-track",
		},
	}
	next, _ := m.handleActionReconcileMsg(msg)
	got := next.(model)
	if got.status == nil || got.status.TrackID != "current" {
		t.Fatal("expected stale reconcile message to be ignored")
	}
}

func TestHandleActionMsgIgnoresStaleToken(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.stateFetchToken = 7
	m.status = &spotify.PlaybackStatus{TrackID: "current"}
	msg := actionMsg{
		token:    6,
		action:   "play-from-browser",
		err:      nil,
		rollback: &spotify.PlaybackStatus{TrackID: "stale"},
	}
	next, _ := m.handleActionMsg(msg)
	got := next.(model)
	if got.activeTab == tabPlayer {
		t.Fatal("expected stale action message to be ignored")
	}
	if got.status == nil || got.status.TrackID != "current" {
		t.Fatal("expected stale action message to leave state untouched")
	}
}

func TestHandlePlaybackStateMsgIgnoresOutOfOrderSeq(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.lastPlaybackStateSeq = 10
	msg := playbackStateMsg{
		seq:    9,
		status: &spotify.PlaybackStatus{TrackID: "old"},
	}
	next, _ := m.handlePlaybackStateMsg(msg)
	got := next.(model)
	if got.status != nil {
		t.Fatal("expected out-of-order playback state message to be ignored")
	}
}

func TestHandlePollMsgClearsQueueOnTrackChangeWithoutQueueFetch(t *testing.T) {
	m := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m.stateFetchToken = 1
	m.status = &spotify.PlaybackStatus{TrackID: "track-a"}
	m.queue = []spotify.QueueItem{{ID: "track-a"}, {ID: "track-b"}}
	m.stableQueueLen = len(m.queue)
	m.queueHasMore = true

	msg := pollMsg{
		token:        1,
		status:       &spotify.PlaybackStatus{TrackID: "track-c"},
		queueFetched: false,
	}
	next, _ := m.handlePollMsg(msg)
	got := next.(model)
	if got.queue != nil || got.stableQueueLen != 0 || got.queueHasMore {
		t.Fatalf("expected stale queue to clear on track change without queue fetch, got queue=%v stable=%d hasMore=%t", got.queue, got.stableQueueLen, got.queueHasMore)
	}
}

func TestStatusQueueCacheScopedPerModel(t *testing.T) {
	m1 := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	m2 := newModel(context.Background(), nil, nil, config.Config{DeviceName: "orpheus", PollInterval: time.Second}, nil)
	if m1.statusQueueCache == nil || m2.statusQueueCache == nil {
		t.Fatal("expected status queue cache to be initialized")
	}
	if m1.statusQueueCache == m2.statusQueueCache {
		t.Fatal("expected each model to own an isolated status queue cache")
	}
}
