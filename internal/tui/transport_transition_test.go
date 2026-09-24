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
}

func TestTransportTransitionStuckLeavesNoRecoveryState(t *testing.T) {
	var tr transportTransition
	start := time.Now()
	tr.Begin(start, "track-1")
	tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-1", ProgressMS: 500},
		start.Add(transportTransitionStuckTimeout+time.Second))
	tr.Begin(time.Now(), "track-2")
	if !tr.Pending() {
		t.Fatal("expected pending after re-Begin")
	}
	if tr.FromTrack() != "track-2" {
		t.Fatalf("expected FromTrack=track-2 after re-Begin, got %q", tr.FromTrack())
	}
}

func TestTransportTransitionClearUnconditionallyResetsPending(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-1")
	tr.Clear()
	if tr.Pending() {
		t.Fatal("expected Clear to clear pending state")
	}
}

func TestTransportTransitionBeginOverwritesPrevious(t *testing.T) {
	var tr transportTransition
	tr.Begin(time.Now(), "track-1")
	tr.MaybeClear(&spotify.PlaybackStatus{TrackID: "track-1"},
		time.Now().Add(transportTransitionStuckTimeout+time.Second))
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
