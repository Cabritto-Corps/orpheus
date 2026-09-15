package librespot

import (
	"context"
	"testing"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	devicespb "github.com/elxgy/go-librespot/proto/spotify/connectstate/devices"
	"github.com/elxgy/go-librespot/tracks"
)

func newQueueEditTestPlayer(t *testing.T, queueUris []string) *AppPlayer {
	t.Helper()

	spotCtx := &connectpb.Context{
		Uri: "spotify:playlist:test",
		Pages: []*connectpb.ContextPage{
			{Tracks: []*connectpb.ContextTrack{{Uri: "spotify:track:0000000000000000000000"}}},
		},
	}
	tl, err := tracks.NewTrackListFromContext(context.Background(), &golibrespot.NullLogger{}, nil, spotCtx, 0)
	if err != nil {
		t.Fatalf("failed building track list: %v", err)
	}
	for _, uri := range queueUris {
		tl.AddToQueue(&connectpb.ContextTrack{Uri: uri})
	}

	runtime := &Runtime{
		Log:             noopLogger{},
		PlaybackStateCh: make(chan *PlaybackStateUpdate, 8),
	}
	p := &AppPlayer{
		runtime:           runtime,
		connectStateTimer: time.NewTimer(time.Hour),
		prefetchTimer:     time.NewTimer(time.Hour),
	}
	p.runtime.Cfg = DefaultConfig()
	p.state = &State{
		device: golibrespot.DefaultDeviceInfo(golibrespot.DeviceInfoOpts{
			DeviceName:  "test",
			DeviceType:  devicespb.DeviceType_COMPUTER,
			ClientId:    golibrespot.ClientIdHex,
			VolumeSteps: p.runtime.Cfg.VolumeSteps,
		}),
		player: golibrespot.NewPlayerState(),
	}
	p.state.tracks = tl
	return p
}

func visibleIDs(p *AppPlayer) []string {
	var out []string
	for _, tr := range p.state.tracks.UpcomingTracksLoaded(64) {
		out = append(out, tr.Uri)
	}
	return out
}

func TestQueueRemoveCommand(t *testing.T) {
	p := newQueueEditTestPlayer(t, []string{"spotify:track:1111111111111111111111", "spotify:track:2222222222222222222222", "spotify:track:3333333333333333333333"})

	handled, err := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandQueueRemove, QueueIndex: 1})
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	ids := visibleIDs(p)
	if len(ids) != 2 || ids[1] != "spotify:track:3333333333333333333333" {
		t.Fatalf("after remove, queue = %v", ids)
	}
}

func TestQueueRemoveOutOfRangeIsNoop(t *testing.T) {
	p := newQueueEditTestPlayer(t, []string{"spotify:track:1111111111111111111111"})

	if _, err := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandQueueRemove, QueueIndex: 5}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ids := visibleIDs(p); len(ids) != 1 {
		t.Fatalf("out-of-range remove must not mutate, queue = %v", ids)
	}
}

func TestQueueReorderCommand(t *testing.T) {
	p := newQueueEditTestPlayer(t, []string{"spotify:track:1111111111111111111111", "spotify:track:2222222222222222222222", "spotify:track:3333333333333333333333"})

	if _, err := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandQueueReorder, QueueIndex: 2, QueueTargetIndex: 0}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ids := visibleIDs(p)
	if ids[0] != "spotify:track:3333333333333333333333" || ids[2] != "spotify:track:2222222222222222222222" {
		t.Fatalf("after reorder queue = %v", ids)
	}
}

func TestQueueRemoveNilState(t *testing.T) {
	p := &AppPlayer{runtime: &Runtime{Log: noopLogger{}}}
	if _, err := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandQueueRemove}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueueJumpCommandPromotesTarget(t *testing.T) {
	p := newQueueEditTestPlayer(t, []string{"spotify:track:1111111111111111111111", "spotify:track:2222222222222222222222"})
	// The full load path needs a live fork player; assert the promotion and
	// state sync side, which is what the backend owns on the Run goroutine.
	p.player = nil

	if _, err := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandQueueJump, QueueIndex: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.state.player.Track == nil || p.state.player.Track.Uri != "spotify:track:2222222222222222222222" {
		t.Fatalf("jump must promote the target to current track, got %v", p.state.player.Track)
	}
	if ids := visibleIDs(p); len(ids) != 0 {
		t.Fatalf("promoted entry must leave the up-next view, got %v", ids)
	}
}
