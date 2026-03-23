package tests

import (
	"testing"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
)

func TestStreamCloseOperation(t *testing.T) {
	closed := false
	src := &mockCloser{onClose: func() { closed = true }}
	s := &player.Stream{Source: src}

	if closer, ok := s.Source.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	if !closed {
		t.Fatal("expected stream source to be closed")
	}
}

func TestStreamCloseOnNonCloser(t *testing.T) {
	src := &mockNonCloser{}
	s := &player.Stream{Source: src}

	if closer, ok := s.Source.(interface{ Close() error }); ok {
		_ = closer.Close()
		t.Fatal("expected non-closer to not match interface")
	}
	_ = s // use the variable
}

type mockCloser struct {
	onClose func()
}

func (m *mockCloser) SetPositionMs(int64) error   { return nil }
func (m *mockCloser) PositionMs() int64           { return 0 }
func (m *mockCloser) Read([]float32) (int, error) { return 0, nil }
func (m *mockCloser) Close() error {
	if m.onClose != nil {
		m.onClose()
	}
	return nil
}

type mockNonCloser struct{}

func (m *mockNonCloser) SetPositionMs(int64) error   { return nil }
func (m *mockNonCloser) PositionMs() int64           { return 0 }
func (m *mockNonCloser) Read([]float32) (int, error) { return 0, nil }

func TestPlayerStateDefaults(t *testing.T) {
	state := golibrespot.NewPlayerState()
	if !state.IsSystemInitiated {
		t.Fatal("expected IsSystemInitiated to be true")
	}
	if state.PlaybackSpeed != 1 {
		t.Fatal("expected PlaybackSpeed to be 1")
	}
	if state.PlayOrigin == nil {
		t.Fatal("expected PlayOrigin to be non-nil")
	}
	if state.Options == nil {
		t.Fatal("expected Options to be non-nil")
	}
}

func TestTrackPositionPaused(t *testing.T) {
	state := golibrespot.NewPlayerState()
	state.IsPaused = true
	state.IsPlaying = true
	state.PositionAsOfTimestamp = 5000
	state.Timestamp = time.Now().UnixMilli() - 10000

	pos := golibrespot.TrackPosition(state, 0)
	if pos != 5000 {
		t.Fatalf("expected 5000 for paused, got %d", pos)
	}
}

func TestTrackPositionPlaying(t *testing.T) {
	state := golibrespot.NewPlayerState()
	state.IsPlaying = true
	state.IsPaused = false
	state.PositionAsOfTimestamp = 1000
	state.Timestamp = time.Now().UnixMilli() - 5000

	pos := golibrespot.TrackPosition(state, 0)
	if pos < 5000 || pos > 7000 {
		t.Fatalf("expected ~6000 for playing, got %d", pos)
	}
}

func TestTrackPositionClampsToDuration(t *testing.T) {
	state := golibrespot.NewPlayerState()
	state.IsPlaying = true
	state.PositionAsOfTimestamp = 10000
	state.Timestamp = time.Now().UnixMilli() - 1000

	pos := golibrespot.TrackPosition(state, 5000)
	if pos != 5000 {
		t.Fatalf("expected 5000 (duration clamp), got %d", pos)
	}
}

func TestNormalizeSpotifyId(t *testing.T) {
	got := golibrespot.NormalizeSpotifyId("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	if got != "7GhIk7Il098yCjg4BQjzvb" {
		t.Fatalf("expected base62 id, got %s", got)
	}
}
