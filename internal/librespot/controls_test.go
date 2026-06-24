package librespot

import (
	"testing"
	"time"

	"github.com/elxgy/go-librespot/player"
)

func TestCloseStreamNil(t *testing.T) {
	closeStream(nil) // should not panic
}

func TestCloseStreamWithCloser(t *testing.T) {
	closed := false
	mock := &mockAudioSource{onClose: func() { closed = true }}
	s := &player.Stream{Source: mock}
	closeStream(s)
	if !closed {
		t.Fatal("expected source to be closed")
	}
}

type mockAudioSource struct {
	onClose func()
}

func (m *mockAudioSource) SetPositionMs(int64) error   { return nil }
func (m *mockAudioSource) PositionMs() int64           { return 0 }
func (m *mockAudioSource) Read([]float32) (int, error) { return 0, nil }
func (m *mockAudioSource) Close() error {
	if m.onClose != nil {
		m.onClose()
	}
	return nil
}

func TestStopAndResetTimerNil(t *testing.T) {
	stopAndResetTimer(nil, time.Second) // should not panic
}

func TestStopAndResetTimer(t *testing.T) {
	tmr := time.NewTimer(time.Hour)
	stopAndResetTimer(tmr, 100*time.Millisecond)
	select {
	case <-tmr.C:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timer didn't fire after reset")
	}
}
