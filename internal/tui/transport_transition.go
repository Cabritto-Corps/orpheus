package tui

import (
	"time"

	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/spotify"
)

type transportState int

const (
	transportIdle transportState = iota
	transportAwaitingTrack
)

type transportEvent int

const (
	transportEventNone transportEvent = iota
	transportEventTrackChanged
	transportEventTrackPlaying
	transportEventStuck
)

const (
	transportTransitionStuckTimeout  = 4 * time.Second
	transportTransitionProgressMaxMS = 2000
)

type transportTransition struct {
	state           transportState
	fromTrack       string
	startedAt       time.Time
	recoveryPending bool
	stuckCount      int
}

func (t *transportTransition) Pending() bool {
	return t.state == transportAwaitingTrack
}

func (t *transportTransition) RecoveryPending() bool {
	return t.recoveryPending
}

func (t *transportTransition) StuckCount() int {
	return t.stuckCount
}

func (t *transportTransition) FromTrack() string {
	return t.fromTrack
}

func (t *transportTransition) StartedAt() time.Time {
	return t.startedAt
}

func (t *transportTransition) Begin(now time.Time, fromTrackID string) {
	t.state = transportAwaitingTrack
	t.startedAt = now
	t.fromTrack = fromTrackID
}

func (t *transportTransition) Clear() {
	t.state = transportIdle
}

func (t *transportTransition) ConsumeRecovery() bool {
	if !t.recoveryPending {
		return false
	}
	t.recoveryPending = false
	return true
}

func (t *transportTransition) MaybeClear(next *spotify.PlaybackStatus, now time.Time) transportEvent {
	if t.state != transportAwaitingTrack || next == nil {
		return transportEventNone
	}
	nextTrack := golibrespot.NormalizeSpotifyId(next.TrackID)
	if nextTrack != "" && nextTrack != t.fromTrack {
		t.state = transportIdle
		return transportEventTrackChanged
	}
	if nextTrack == t.fromTrack && next.ProgressMS < transportTransitionProgressMaxMS &&
		now.Sub(t.startedAt) < transportTransitionStuckTimeout {
		t.state = transportIdle
		return transportEventTrackPlaying
	}
	if now.Sub(t.startedAt) > transportTransitionStuckTimeout {
		t.state = transportIdle
		t.recoveryPending = true
		t.stuckCount++
		return transportEventStuck
	}
	return transportEventNone
}
