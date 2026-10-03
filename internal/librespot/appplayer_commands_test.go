package librespot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elxgy/go-librespot/dealer"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"google.golang.org/protobuf/proto"
)

// A cluster push without player state must evaluate nil-safely.
func TestHandleDealerMessageClusterWithoutPlayerState(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	payload, err := proto.Marshal(&connectpb.ClusterUpdate{
		Cluster: &connectpb.Cluster{ActiveDeviceId: "other-device"},
	})
	if err != nil {
		t.Fatalf("marshal cluster: %v", err)
	}
	p.state.setActive(true)
	if err := p.handleDealerMessage(context.Background(), dealer.Message{
		Uri:     "hm://connect-state/v1/cluster",
		Payload: payload,
	}); err != nil {
		t.Fatalf("cluster without player state: %v", err)
	}
}

// A transfer without session context is rejected before touching state.
func TestHandlePlayerCommandTransferWithoutSession(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	// Empty TransferState marshals to zero bytes (no-data return); options add content.
	payload, err := proto.Marshal(&connectpb.TransferState{
		Options: &connectpb.ContextPlayerOptions{},
	})
	if err != nil {
		t.Fatalf("marshal transfer state: %v", err)
	}
	var req dealer.RequestPayload
	req.Command.Endpoint = "transfer"
	req.Command.Data = payload
	err = p.handlePlayerCommand(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "session context") {
		t.Fatalf("transfer without session: got %v, want session context error", err)
	}
}

// A transfer without a playback block must fail cleanly, not deref.
func TestHandlePlayerCommandTransferWithoutPlayback(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	payload, err := proto.Marshal(&connectpb.TransferState{
		CurrentSession: &connectpb.Session{
			Context: &connectpb.Context{Uri: "spotify:playlist:test"},
		},
	})
	if err != nil {
		t.Fatalf("marshal transfer state: %v", err)
	}
	var req dealer.RequestPayload
	req.Command.Endpoint = "transfer"
	req.Command.Data = payload
	err = p.handlePlayerCommand(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "playback") {
		t.Fatalf("transfer without playback: got %v, want playback error", err)
	}
}

// Modes are opaque echo state: a modes-only set_options merges the map and
// pushes state so remotes round-trip, without changing playback behavior.
func TestSetOptionsModesAreOpaqueEcho(t *testing.T) {
	p, updates := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	var req dealer.RequestPayload
	req.Command.Endpoint = "set_options"
	req.Command.Modes = map[string]string{"jam": "on"}
	if err := p.handlePlayerCommand(context.Background(), req); err != nil {
		t.Fatalf("set_options with modes: %v", err)
	}
	if got := p.state.player.Options.Modes["jam"]; got != "on" {
		t.Fatalf("mode jam = %q, want on", got)
	}
	select {
	case <-updates:
	case <-time.After(time.Second):
		t.Fatal("mode change must push playback state")
	}
}

func TestHandlePlayerCommandUpdateContextWithoutContext(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	var req dealer.RequestPayload
	req.Command.Endpoint = "update_context"
	req.Command.Context = nil
	if err := p.handlePlayerCommand(context.Background(), req); err != nil {
		t.Fatalf("update_context without context: got %v, want nil", err)
	}
}

// Reachable with one keypress before anything loads.
func TestSkipPrevWithoutTrack(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	p.state.player.Track = nil
	err := p.handleTUICommand(context.Background(), TUICommand{Kind: TUICommandSkipPrev})
	if err == nil || !strings.Contains(err.Error(), "no current track") {
		t.Fatalf("skip prev without track: got %v, want no-current-track error", err)
	}
}

func TestLoadCurrentTrackWithoutTrack(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	p.state.player.Track = nil
	if err := p.loadCurrentTrack(context.Background(), false, true); err == nil || !strings.Contains(err.Error(), "no current track") {
		t.Fatalf("load without track: got %v, want no-current-track error", err)
	}
}

func TestShouldReconcilePushGatesTerminalError(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	p.state.player.ContextUri = "spotify:playlist:test"
	if !p.shouldReconcilePush() {
		t.Fatal("healthy context must allow the reconcile push")
	}
	p.terminalErrActive = true
	if p.shouldReconcilePush() {
		t.Fatal("terminal error must suppress the reconcile push")
	}
	p.terminalErrActive = false
	p.state.player.ContextUri = ""
	if p.shouldReconcilePush() {
		t.Fatal("empty context must suppress the reconcile push")
	}
}

func TestUnexpectedStopGiveUpArmsTerminalLatch(t *testing.T) {
	p, ch := newStopRecoveryTestPlayer(t)
	p.state.player.ContextUri = "spotify:playlist:test"
	p.stopRecoveryURI = stopRecoveryTestURI
	p.stopRecoveryFailures = endGuardMaxFailures

	p.handlePlayerEvent(&player.Event{Type: player.EventTypeStop})

	if !p.terminalErrActive {
		t.Fatal("give-up must latch the terminal error")
	}
	if p.shouldReconcilePush() {
		t.Fatal("reconcile must stay suppressed while the give-up error stands")
	}
	var sawError bool
	for {
		select {
		case u := <-ch:
			if u.Error != "" {
				sawError = true
			}
		default:
			goto done
		}
	}
done:
	if !sawError {
		t.Fatal("give-up must surface a user-facing error")
	}
}

func TestMaybeEmitAfterDrop(t *testing.T) {
	p, updates := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	p.state.player.ContextUri = "spotify:playlist:test"
	for range 10 {
		p.emitPlaybackState()
	}
	if p.runtime.DroppedPlaybackStateUpdates() == 0 {
		t.Fatal("expected dropped sends against an undrained channel")
	}
	for len(updates) > 0 {
		<-updates
	}
	p.maybeEmitAfterDrop()
	select {
	case u := <-updates:
		if !u.QueueIncluded {
			t.Fatal("drop recovery must re-emit a full snapshot with queue entries")
		}
	default:
		t.Fatal("expected a recovery push after dropped sends")
	}
	p.maybeEmitAfterDrop()
	select {
	case u := <-updates:
		t.Fatalf("recovery must fire once per drop episode, got %+v", u)
	default:
	}
}

func TestFlushConnectStateEnqueuesLatest(t *testing.T) {
	p, _ := newSkipTestPlayer(t, newPipeBackedPlayer(t), []string{"spotify:track:0000000000000000000000"})
	p.connectPutJobs = make(chan connectPutJob, 1)
	p.pendingConnectPut = true
	p.pendingConnectReason = connectpb.PutStateReason_PLAYER_STATE_CHANGED
	p.flushConnectState()
	p.pendingConnectPut = true
	p.pendingConnectReason = connectpb.PutStateReason_VOLUME_CHANGED
	p.flushConnectState()
	select {
	case job := <-p.connectPutJobs:
		if job.inactive || job.reason != connectpb.PutStateReason_VOLUME_CHANGED {
			t.Fatalf("expected the latest job, got %+v", job)
		}
	default:
		t.Fatal("expected one enqueued PUT job")
	}
	select {
	case job := <-p.connectPutJobs:
		t.Fatalf("superseded jobs must be dropped, got %+v", job)
	default:
	}
}
