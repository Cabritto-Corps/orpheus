package librespot

import (
	"testing"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
)

const stopRecoveryTestURI = "spotify:track:1111111111111111111111"

func newStopRecoveryTestPlayer(t *testing.T) (*AppPlayer, chan *PlaybackStateUpdate) {
	t.Helper()
	p := newQueueEditTestPlayer(t, nil)
	p.state.player.Track = &connectpb.ProvidedTrack{Uri: stopRecoveryTestURI}
	p.state.player.IsPlaying = true
	p.state.player.IsPaused = false
	p.primaryStream = newTestStreamWithDuration(240000)
	ch := make(chan *PlaybackStateUpdate, 8)
	p.runtime.PlaybackStateCh = ch
	return p, ch
}

func drainPlaybackUpdate(t *testing.T, ch chan *PlaybackStateUpdate) *PlaybackStateUpdate {
	t.Helper()
	select {
	case u := <-ch:
		return u
	default:
		t.Fatal("expected a playback state push")
		return nil
	}
}

func assertNoPlaybackUpdate(t *testing.T, ch chan *PlaybackStateUpdate) {
	t.Helper()
	select {
	case u := <-ch:
		t.Fatalf("expected no further push, got %+v", u)
	default:
	}
}

func TestPlanStopRecoveryFirstStopReloads(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)

	action, uri := p.planStopRecovery()

	if action != stopActionReload || uri != stopRecoveryTestURI {
		t.Fatalf("first stop must reload, got action=%d uri=%q", action, uri)
	}
	if p.stopRecoveryURI != stopRecoveryTestURI || p.stopRecoveryFailures != 1 {
		t.Fatalf("reload must record the guard, got uri=%q failures=%d", p.stopRecoveryURI, p.stopRecoveryFailures)
	}
}

func TestPlanStopRecoverySameTrackAdvances(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)
	p.stopRecoveryURI = stopRecoveryTestURI
	p.stopRecoveryFailures = 1

	action, uri := p.planStopRecovery()

	if action != stopActionAdvance || uri != stopRecoveryTestURI {
		t.Fatalf("second stop on the same track must advance, got action=%d uri=%q", action, uri)
	}
}

func TestPlanStopRecoveryGivesUpAtCap(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)

	var actions []stopRecoveryAction
	for range endGuardMaxFailures + 1 {
		action, _ := p.planStopRecovery()
		actions = append(actions, action)
	}

	if actions[0] != stopActionReload {
		t.Fatalf("first stop must reload, got %d", actions[0])
	}
	for i := 1; i < endGuardMaxFailures; i++ {
		if actions[i] != stopActionAdvance {
			t.Fatalf("stop %d must advance, got %d", i+1, actions[i])
		}
	}
	if actions[endGuardMaxFailures] != stopActionGiveUp {
		t.Fatalf("stop %d must give up, got %d", endGuardMaxFailures+1, actions[endGuardMaxFailures])
	}
}

func TestPlanStopRecoveryIgnoresStaleStop(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)
	p.primaryStream = nil

	if action, _ := p.planStopRecovery(); action != stopActionIgnore {
		t.Fatalf("stop with no primary (own Stop call) must be ignored, got %d", action)
	}

	p.primaryStream = newTestStreamWithDuration(240000)
	p.state.player.IsPlaying = false
	p.state.player.IsPaused = false

	if action, _ := p.planStopRecovery(); action != stopActionIgnore {
		t.Fatalf("stop while fully stopped must be ignored, got %d", action)
	}
}

func TestUnexpectedStopWhilePausedSurfacesError(t *testing.T) {
	p, ch := newStopRecoveryTestPlayer(t)
	p.state.player.IsPaused = true

	p.handlePlayerEvent(&player.Event{Type: player.EventTypeStop})

	if p.state.player.Track.Uri != stopRecoveryTestURI {
		t.Fatalf("paused stop must not change tracks, got %q", p.state.player.Track.Uri)
	}
	if !p.state.player.IsPaused {
		t.Fatal("paused stop must stay paused")
	}
	if p.stopRecoveryURI != "" || p.stopRecoveryFailures != 0 {
		t.Fatal("paused stop must not arm the reload guard")
	}
	if p.state.player.IsPlaying {
		t.Fatal("paused stop must mark the transport dead so the toggle reaches Resume")
	}
	if !p.outputRecreateOnPlay {
		t.Fatal("paused stop must arm output recreation for the next play")
	}
	if first := drainPlaybackUpdate(t, ch); first.Error != "" {
		t.Fatalf("state push must precede the error push, got error %q", first.Error)
	}
	if second := drainPlaybackUpdate(t, ch); second.Error == "" {
		t.Fatal("paused stop must surface a user-facing error")
	}
	assertNoPlaybackUpdate(t, ch)
}

func TestUnexpectedStopWithNoPrimaryIsIgnored(t *testing.T) {
	p, ch := newStopRecoveryTestPlayer(t)
	p.primaryStream = nil

	p.handlePlayerEvent(&player.Event{Type: player.EventTypeStop})

	if first := drainPlaybackUpdate(t, ch); first.Error != "" {
		t.Fatalf("stale stop must not surface an error, got %q", first.Error)
	}
	assertNoPlaybackUpdate(t, ch)
}

func TestUnexpectedStopGiveUpSurfacesError(t *testing.T) {
	p, ch := newStopRecoveryTestPlayer(t)
	p.stopRecoveryURI = stopRecoveryTestURI
	p.stopRecoveryFailures = endGuardMaxFailures

	p.handlePlayerEvent(&player.Event{Type: player.EventTypeStop})

	if p.state.player.IsPlaying || p.state.player.IsBuffering {
		t.Fatal("give-up must report the transport stopped, not playing")
	}
	if !p.outputRecreateOnPlay {
		t.Fatal("give-up must arm output recreation for the next play")
	}

	if first := drainPlaybackUpdate(t, ch); first.Error != "" {
		t.Fatalf("state push must precede the error push, got error %q", first.Error)
	}
	if second := drainPlaybackUpdate(t, ch); second.Error == "" {
		t.Fatal("give-up must surface a user-facing error")
	}
	assertNoPlaybackUpdate(t, ch)
}

func TestStopRecoveryGuardKeepsSameTrackCommit(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)

	if action, _ := p.planStopRecovery(); action != stopActionReload {
		t.Fatalf("first stop must reload, got %d", action)
	}
	// A same-track commit is the recovery's own reload: it must not clear the guard.
	p.maybeResetStopRecoveryGuard(stopRecoveryTestURI)
	if p.stopRecoveryURI != stopRecoveryTestURI {
		t.Fatal("same-track commit must keep the guard armed")
	}
	if action, _ := p.planStopRecovery(); action != stopActionAdvance {
		t.Fatalf("second stop on the same track must advance, got %d", action)
	}
}

func TestStopRecoveryGuardResetsOnTrackChange(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)

	if action, _ := p.planStopRecovery(); action != stopActionReload {
		t.Fatalf("first stop must reload, got %d", action)
	}
	p.maybeResetStopRecoveryGuard("spotify:track:2222222222222222222222")
	if p.stopRecoveryURI != "" || p.stopRecoveryFailures != 0 {
		t.Fatal("other-track commit must clear the guard")
	}
	if action, _ := p.planStopRecovery(); action != stopActionReload {
		t.Fatalf("stop on a fresh track must reload, got %d", action)
	}
}

func TestReloadPathDoesNotArmRecreateFlag(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)

	if action, _ := p.planStopRecovery(); action != stopActionReload {
		t.Fatalf("first stop must reload, got %d", action)
	}
	if p.outputRecreateOnPlay {
		t.Fatal("reload path must not arm output recreation; only terminal branches do")
	}
}

func TestAdvanceAfterOutputFailurePushOrder(t *testing.T) {
	p, ch := newStopRecoveryTestPlayer(t)
	// Contend the transition so the advance short-circuits; this guards push ordering.
	p.advanceInFlight.Store(true)

	p.advanceAfterOutputFailure()

	if first := drainPlaybackUpdate(t, ch); first.Error != "" {
		t.Fatalf("state push must precede the error push, got error %q", first.Error)
	}
	second := drainPlaybackUpdate(t, ch)
	if second.Error == "" {
		t.Fatal("advance failure must surface a user-facing error")
	}
	if second.Error != "output error — press next to continue" {
		t.Fatalf("contended advance must not claim end of queue, got %q", second.Error)
	}
	assertNoPlaybackUpdate(t, ch)
}

func TestDropSuspectCachedStream(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)
	p.transitionCache = newTransitionCache()
	closedCh := make(chan struct{}, 1)
	id, err := golibrespot.SpotifyIdFromUri(stopRecoveryTestURI)
	if err != nil {
		t.Fatalf("failed parsing test uri: %v", err)
	}
	if !p.putTransitionCachedStream(*id, &player.Stream{Source: &mockAudioSource{onClose: func() {
		select {
		case closedCh <- struct{}{}:
		default:
		}
	}}}) {
		t.Fatal("failed seeding transition cache")
	}

	p.dropSuspectCachedStream(stopRecoveryTestURI)

	if p.hasTransitionCachedStream(*id) {
		t.Fatal("suspect cached stream must be dropped before reload")
	}
	// The drop closes asynchronously by design; wait instead of racing the closer.
	select {
	case <-closedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("dropped cached stream must be closed")
	}
}

func TestDropSuspectCachedStreamNoop(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)

	p.dropSuspectCachedStream(stopRecoveryTestURI)
	p.dropSuspectCachedStream("")
	p.dropSuspectCachedStream("not a uri")

	p.transitionCache = newTransitionCache()
	p.dropSuspectCachedStream("spotify:track:2222222222222222222222")
}

func TestIsStaleStopSource(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)
	current := &mockAudioSource{}
	other := &mockAudioSource{}
	p.primaryStream.Source = current

	if p.isStaleStopSource(nil) {
		t.Fatal("nil source (explicit stops, untagged failures) is never stale")
	}
	if p.isStaleStopSource(current) {
		t.Fatal("a stop for the current primary is live, not stale")
	}
	if !p.isStaleStopSource(other) {
		t.Fatal("a stop for a superseded source must be stale")
	}

	p.primaryStream.Source = nil
	if p.isStaleStopSource(other) {
		t.Fatal("an unknown current source cannot prove staleness")
	}

	p.primaryStream = nil
	if p.isStaleStopSource(other) {
		t.Fatal("no primary cannot prove staleness")
	}
}

// A Stop predating the committed track (the output loop reads ahead while Run
// loads synchronously) must not restart the healthy track from zero.
func TestUnexpectedStopIgnoresStaleSource(t *testing.T) {
	p, ch := newStopRecoveryTestPlayer(t)
	current := &mockAudioSource{}
	p.primaryStream.Source = current

	p.handlePlayerEvent(&player.Event{Type: player.EventTypeStop, Source: &mockAudioSource{}})

	if p.state.player.Track.Uri != stopRecoveryTestURI {
		t.Fatalf("stale stop must not change tracks, got %q", p.state.player.Track.Uri)
	}
	if p.stopRecoveryURI != "" || p.stopRecoveryFailures != 0 {
		t.Fatal("stale stop must not arm the recovery guard")
	}
	if p.outputRecreateOnPlay {
		t.Fatal("stale stop must not arm output recreation")
	}
	// Only the light push goes out; predated failures surface no error.
	if first := drainPlaybackUpdate(t, ch); first.Error != "" {
		t.Fatalf("stale stop must not push an error, got %q", first.Error)
	}
	assertNoPlaybackUpdate(t, ch)
}

// A Stop tagged with the current primary is live and reaches normal planning.
func TestUnexpectedStopMatchingSourceReachesRecovery(t *testing.T) {
	p, _ := newStopRecoveryTestPlayer(t)
	current := &mockAudioSource{}
	p.primaryStream.Source = current
	p.stopRecoveryURI = stopRecoveryTestURI
	p.stopRecoveryFailures = 1

	// A re-failing same track must advance, not reload: the tag matched.
	if action, _ := p.planStopRecovery(); action != stopActionAdvance {
		t.Fatalf("matching stop must advance on a repeated failure, got %d", action)
	}
}
