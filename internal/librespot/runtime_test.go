package librespot

import "testing"

func TestEmitNilChannel(t *testing.T) {
	r := &Runtime{}
	r.Emit(&ApiEvent{Type: "test"})
}

func TestEmitSuccess(t *testing.T) {
	ch := make(chan *ApiEvent, 1)
	r := &Runtime{StateCh: ch}
	ev := &ApiEvent{Type: "test"}
	r.Emit(ev)
	got := <-ch
	if got.Type != "test" {
		t.Fatal("expected event to arrive")
	}
}

func TestEmitDropIncrementsCounter(t *testing.T) {
	ch := make(chan *ApiEvent) // unbuffered, will block
	r := &Runtime{StateCh: ch}
	r.Emit(&ApiEvent{Type: "first"}) // fills buffer (0 slots), drops
	if r.DroppedStateEvents() != 1 {
		t.Fatalf("expected 1 dropped, got %d", r.DroppedStateEvents())
	}
}

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
