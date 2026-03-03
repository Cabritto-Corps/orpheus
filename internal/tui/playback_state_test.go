package tui

import (
	"fmt"
	"testing"
	"time"

	"orpheus/internal/spotify"
)

func TestNormalizeQueueID(t *testing.T) {
	if got := normalizeQueueID("spotify:track:abc123"); got != "abc123" {
		t.Fatalf("expected spotify URI to normalize to id, got %q", got)
	}
	if got := normalizeQueueID("plain-id"); got != "plain-id" {
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
		pendingContextFrom: "track-a",
		queue:              []spotify.QueueItem{{ID: "old"}},
		stableQueueLen:     1,
		queueHasMore:       true,
	}
	if m.shouldApplyIncomingQueue("track-a") {
		t.Fatal("expected queue update to be gated for matching pending context")
	}
	if m.queue != nil || m.stableQueueLen != 0 || m.queueHasMore {
		t.Fatalf("expected queue state reset while waiting for context switch, got queue=%v stable=%d hasMore=%t", m.queue, m.stableQueueLen, m.queueHasMore)
	}
}

func TestApplyMergedQueueRebuildsPreloadedIDs(t *testing.T) {
	m := model{
		status:            &spotify.PlaybackStatus{},
		queue:             []spotify.QueueItem{{ID: "spotify:track:stale"}},
		preloadedTrackIDs: map[string]struct{}{"stale": {}, "ghost": {}},
		trackCache:        map[string]spotify.QueueItem{},
	}
	m.applyMergedQueue(
		[]spotify.QueueItem{
			{ID: "spotify:track:new-1", Name: "Track 1", Artist: "Artist 1"},
			{ID: "plain-2", Name: "Track 2", Artist: "Artist 2"},
		},
		false,
		true,
		true,
	)

	if _, ok := m.preloadedTrackIDs["new-1"]; !ok {
		t.Fatal("expected normalized spotify id to be preloaded")
	}
	if _, ok := m.preloadedTrackIDs["plain-2"]; !ok {
		t.Fatal("expected plain id to be preloaded")
	}
	if _, ok := m.preloadedTrackIDs["stale"]; ok {
		t.Fatal("expected stale preloaded ids to be removed")
	}
	if len(m.preloadedTrackIDs) != 2 {
		t.Fatalf("expected preloaded id set to rebuild from merged queue, got %d entries", len(m.preloadedTrackIDs))
	}
}

func TestMergeQueueWithRestPreservesTailWithoutDuplicates(t *testing.T) {
	prev := make([]spotify.QueueItem, 34)
	for i := range prev {
		prev[i] = spotify.QueueItem{ID: fmt.Sprintf("track-%d", i)}
	}
	next := []spotify.QueueItem{
		{ID: prev[0].ID},
		{ID: prev[1].ID},
		{ID: prev[33].ID},
	}

	merged := mergeQueueWithRest(prev, next, nil, true)
	if len(merged) != 4 {
		t.Fatalf("expected merged queue to append only unseen tail tracks, got %d entries", len(merged))
	}
	if merged[3].ID != prev[32].ID {
		t.Fatalf("expected unseen tail track %q to be appended, got %q", prev[32].ID, merged[3].ID)
	}
}
