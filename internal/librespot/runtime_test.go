package librespot

import "testing"

func TestEmitPlaybackStateNilChannel(t *testing.T) {
	r := &Runtime{}
	r.EmitPlaybackState(&PlaybackStateUpdate{})
}

func TestEmitPlaybackStateNilUpdate(t *testing.T) {
	ch := make(chan *PlaybackStateUpdate, 1)
	r := &Runtime{PlaybackStateCh: ch}
	r.EmitPlaybackState(nil)
	if len(ch) != 0 {
		t.Fatal("expected nil update to not be sent")
	}
}

func TestEmitPlaybackStateDrop(t *testing.T) {
	ch := make(chan *PlaybackStateUpdate) // unbuffered
	r := &Runtime{PlaybackStateCh: ch}
	r.EmitPlaybackState(&PlaybackStateUpdate{})
	if r.DroppedPlaybackStateUpdates() != 1 {
		t.Fatalf("expected 1 dropped, got %d", r.DroppedPlaybackStateUpdates())
	}
}
