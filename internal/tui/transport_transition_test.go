package tui

import (
	"testing"
	"time"

	"orpheus/internal/spotify"
)

func TestTransportTransitionIdleByDefault(t *testing.T) {
	var tr transportTransition
	if tr.Pending() {
		t.Fatal("expected idle transition to not be pending")
	}
	if tr.RecoveryPending() {
		t.Fatal("expected no recovery pending when idle")
	}
	if tr.StuckCount() != 0 {
		t.Fatalf("expected zero stuck count, got %d", tr.StuckCount())
	}
	if tr.FromTrack() != "" {
		t.Fatalf("expected empty FromTrack, got %q", tr.FromTrack())
	}
}

func TestTransportTransitionBeginSetsAwaiting(t *testing.T) {
	var tr transportTransition
	now := time.Now()
	tr.Begin(now, "track-1")
	if !tr.Pending() {
		t.Fatal("expected pending after Begin")
	}
	if tr.FromTrack() != "track-1" {
		t.Fatalf("expected FromTrack=track-1, got %q", tr.FromTrack())
	}
	if !tr.StartedAt().Equal(now) {
		t.Fatalf("expected StartedAt=%v, got %v", now, tr.StartedAt())
	}
}

func TestTransportTransitionMaybeClearNoOpsWhenIdle(t *testing.T) {
	var tr transportTransition
	event := tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "any"}, time.Now())
	if event != transportEventNone {
		t.Fatalf("expected None, got %v", event)
	}
}

func TestTransportTransitionMaybeClearNoOpsOnNilStatus(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-1")
	if tr.MaybeClear(nil, time.Now()) != transportEventNone {
		t.Fatal("expected None for nil status")
	}
	if !tr.Pending() {
		t.Fatal("expected still pending when status is nil")
	}
}

func TestTransportTransitionMaybeClearTrackChanged(t *testing.T) {
	var tr transportTransition
	start := time.Now()
	tr.Begin(start, "track-1")
	event := tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-2"}, start.Add(100*time.Millisecond))
	if event != transportEventTrackChanged {
		t.Fatalf("expected TrackChanged event, got %v", event)
	}
	if tr.Pending() {
		t.Fatal("expected pending cleared after TrackChanged")
	}
	if tr.RecoveryPending() {
		t.Fatal("expected no recovery pending after TrackChanged")
	}
	if tr.StuckCount() != 0 {
		t.Fatalf("expected stuck count unchanged, got %d", tr.StuckCount())
	}
}

func TestTransportTransitionMaybeClearTrackPlaying(t *testing.T) {
	var tr transportTransition
	start := time.Now()
	tr.Begin(start, "track-1")
	event := tr.MaybeClear(&spotify.PlaybackStatus{
		TrackID:    "track-1",
		ProgressMS: 100,
	}, start.Add(500*time.Millisecond))
	if event != transportEventTrackPlaying {
		t.Fatalf("expected TrackPlaying event, got %v", event)
	}
	if tr.Pending() {
		t.Fatal("expected pending cleared after TrackPlaying")
	}
	if tr.RecoveryPending() {
		t.Fatal("expected no recovery pending after TrackPlaying")
	}
}

func TestTransportTransitionMaybeClearSameTrackProgressTooHigh(t *testing.T) {
	var tr transportTransition
	start := time.Now()
	tr.Begin(start, "track-1")
	event := tr.MaybeClear(&spotify.PlaybackStatus{
		TrackID:    "track-1",
		ProgressMS: transportTransitionProgressMaxMS + 1,
	}, start.Add(500*time.Millisecond))
	if event != transportEventNone {
		t.Fatalf("expected None while progress above threshold, got %v", event)
	}
	if !tr.Pending() {
		t.Fatal("expected still pending when progress too high")
	}
}

func TestTransportTransitionMaybeClearStuck(t *testing.T) {
	var tr transportTransition
	start := time.Now()
	tr.Begin(start, "track-1")
	event := tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-1", ProgressMS: 500},
		start.Add(transportTransitionStuckTimeout+time.Second))
	if event != transportEventStuck {
		t.Fatalf("expected Stuck event, got %v", event)
	}
	if tr.Pending() {
		t.Fatal("expected pending cleared after Stuck")
	}
	if !tr.RecoveryPending() {
		t.Fatal("expected recovery pending after Stuck")
	}
	if tr.StuckCount() != 1 {
		t.Fatalf("expected stuck count=1, got %d", tr.StuckCount())
	}
}

func TestTransportTransitionStuckCountAccumulates(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-a")
	tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-a"},
		time.Now().Add(transportTransitionStuckTimeout+time.Second))
	tr.Begin(time.Now(), "track-b")
	tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-b"},
		time.Now().Add(transportTransitionStuckTimeout+time.Second))
	if tr.StuckCount() != 2 {
		t.Fatalf("expected stuck count to accumulate to 2, got %d", tr.StuckCount())
	}
}

func TestTransportTransitionConsumeRecoveryDrainsFlag(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-1")
	tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-1"},
		time.Now().Add(transportTransitionStuckTimeout+time.Second))
	if !tr.RecoveryPending() {
		t.Fatal("expected recovery pending after stuck")
	}
	if !tr.ConsumeRecovery() {
		t.Fatal("expected ConsumeRecovery to return true when flag set")
	}
	if tr.RecoveryPending() {
		t.Fatal("expected recovery flag drained after ConsumeRecovery")
	}
	if tr.ConsumeRecovery() {
		t.Fatal("expected ConsumeRecovery to return false when flag already consumed")
	}
}

func TestTransportTransitionClearUnconditionallyResetsPending(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-1")
	tr.Clear()
	if tr.Pending() {
		t.Fatal("expected Clear to clear pending state")
	}
	if tr.RecoveryPending() {
		t.Fatal("expected Clear to not affect recovery flag (recovery is separate)")
	}
}

func TestTransportTransitionBeginOverwritesPrevious(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-1")
	tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-1"},
		time.Now().Add(transportTransitionStuckTimeout+time.Second))
	if tr.StuckCount() != 1 {
		t.Fatal("expected one stuck before rebegin")
	}
	start2 := time.Now()
	tr.Begin(start2, "track-2")
	if !tr.Pending() {
		t.Fatal("expected pending after re-Begin")
	}
	if tr.FromTrack() != "track-2" {
		t.Fatalf("expected FromTrack=track-2 after re-Begin, got %q", tr.FromTrack())
	}
	if !tr.StartedAt().Equal(start2) {
		t.Fatalf("expected StartedAt reset on re-Begin, got %v", tr.StartedAt())
	}
	if tr.StuckCount() != 1 {
		t.Fatalf("expected stuck count preserved across re-Begin, got %d", tr.StuckCount())
	}
}

func TestTransportTransitionMaybeClearTrackChangedEmptyNextTrackIgnored(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-1")
	event := tr.MaybeClear(&spotify.PlaybackStatus{TrackID: ""}, time.Now())
	if event != transportEventNone {
		t.Fatalf("expected None when next track is empty, got %v", event)
	}
	if !tr.Pending() {
		t.Fatal("expected still pending when next track empty")
	}
}

func TestTransportTransitionMaybeClearBoundaryJustUnderStuckTimeout(t *testing.T) {
	var tr transportTransition
	start := time.Now()
	tr.Begin(start, "track-1")
	event := tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-1", ProgressMS: transportTransitionProgressMaxMS + 100},
		start.Add(transportTransitionStuckTimeout-time.Millisecond))
	if event != transportEventNone {
		t.Fatalf("expected None just under stuck timeout, got %v", event)
	}
	if !tr.Pending() {
		t.Fatal("expected still pending just under stuck timeout")
	}
}
