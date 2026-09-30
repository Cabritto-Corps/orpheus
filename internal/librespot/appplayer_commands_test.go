package librespot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elxgy/go-librespot/dealer"
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

// A modes-only set_options must merge into player options and push state.
func TestSetOptionsMergesModes(t *testing.T) {
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
