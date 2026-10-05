package librespot

import (
	"context"
	"errors"
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

func TestAutoplayUsesSourceContextAfterStationRollovers(t *testing.T) {
	tests := []struct {
		name, current, source, want string
	}{
		{"regular context", "spotify:playlist:abc", "spotify:playlist:origin", "spotify:playlist:abc"},
		{"station uses origin", "spotify:station:abc", "spotify:playlist:origin", "spotify:playlist:origin"},
		{"station without origin", "spotify:station:abc", "", "spotify:station:abc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := autoplayContextURI(test.current, test.source); got != test.want {
				t.Fatalf("autoplay context = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveAutoplayContextRetriesUntilStationIsGenerated(t *testing.T) {
	attempts := 0
	want := &connectpb.Context{Uri: "spotify:station:generated"}
	got, err := resolveAutoplayContextWithRetry(context.Background(), func(context.Context) (*connectpb.Context, error) {
		attempts++
		if attempts < 4 {
			return nil, errors.New("autoplay context not ready")
		}
		return want, nil
	}, func(context.Context, time.Duration) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got != want || attempts != 4 {
		t.Fatalf("got context %p after %d attempts; want %p after 4", got, attempts, want)
	}
}

func TestResolveAutoplayContextRetriesUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	_, err := resolveAutoplayContextWithRetry(ctx, func(context.Context) (*connectpb.Context, error) {
		attempts++
		return nil, errors.New("autoplay unavailable")
	}, func(context.Context, time.Duration) error {
		if attempts == 5 {
			cancel()
			return context.Canceled
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	if attempts != 5 {
		t.Fatalf("attempts = %d, want 5", attempts)
	}
}
