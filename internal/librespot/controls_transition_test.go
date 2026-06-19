package librespot

import (
	"testing"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	metadatapb "github.com/elxgy/go-librespot/proto/spotify/metadata"
)

type noopLogger struct{}

func (noopLogger) Tracef(string, ...any) {}
func (noopLogger) Debugf(string, ...any) {}
func (noopLogger) Infof(string, ...any)  {}
func (noopLogger) Warnf(string, ...any)  {}
func (noopLogger) Errorf(string, ...any) {}
func (noopLogger) Trace(...any)          {}
func (noopLogger) Debug(...any)          {}
func (noopLogger) Info(...any)           {}
func (noopLogger) Warn(...any)           {}
func (noopLogger) Error(...any)          {}
func (noopLogger) WithField(string, any) golibrespot.Logger {
	return noopLogger{}
}
func (noopLogger) WithError(error) golibrespot.Logger {
	return noopLogger{}
}

func newTestStreamWithDuration(durationMs int32) *player.Stream {
	name := "test"
	duration := durationMs
	media := golibrespot.NewMediaFromTrack(&metadatapb.Track{
		Name:     &name,
		Duration: &duration,
	})
	return &player.Stream{Media: media}
}

func TestMaybeAdvanceOnTrackEndGuardSkipsWhenTransitionInFlight(t *testing.T) {
	now := time.Now().UnixMilli()
	p := &AppPlayer{
		runtime: &Runtime{
			Log: noopLogger{},
			Cfg: &Config{DeviceName: "test-device"},
		},
		state: &State{
			player: &connectpb.PlayerState{
				IsPlaying:             true,
				IsPaused:              false,
				Timestamp:             now,
				PositionAsOfTimestamp: 900,
				Options:               &connectpb.ContextPlayerOptions{},
			},
		},
		primaryStream: newTestStreamWithDuration(1000),
	}
	p.advanceInFlight.Store(true)

	p.maybeAdvanceOnTrackEndGuard()

	if !p.advanceInFlight.Load() {
		t.Fatal("expected in-flight transition guard to remain enabled")
	}
	if !p.state.player.IsPlaying {
		t.Fatal("expected playback state to remain unchanged when guard skips duplicate transition")
	}
}
