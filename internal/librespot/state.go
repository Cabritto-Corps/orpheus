package librespot

import (
	"context"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/dealer"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/tracks"
)

type State struct {
	active      bool
	activeSince time.Time

	device *connectpb.DeviceInfo
	player *connectpb.PlayerState

	tracks  *tracks.List
	queueID uint64

	lastCommand           *dealer.RequestPayload
	lastTransferTimestamp int64
}

func (s *State) setActive(val bool) {
	if val {
		if s.active {
			return
		}
		s.active = true
		s.activeSince = time.Now()
	} else {
		s.active = false
		s.activeSince = time.Time{}
	}
}

func (s *State) reset() {
	s.active = false
	s.activeSince = time.Time{}
	s.player = golibrespot.NewPlayerState()
}

func (p *AppPlayer) initState() {
	cfg := p.runtime.Cfg
	p.state = &State{
		lastCommand: nil,
		device: golibrespot.DefaultDeviceInfo(golibrespot.DeviceInfoOpts{
			DeviceName:      cfg.DeviceName,
			DeviceId:        p.runtime.DeviceId,
			DeviceType:      p.runtime.DeviceType,
			ClientId:        golibrespot.ClientIdHex,
			VolumeSteps:     cfg.VolumeSteps,
			ZeroconfEnabled: cfg.ZeroconfEnabled,
		}),
	}
	p.state.reset()
}

func (p *AppPlayer) updateState() {
	p.scheduleConnectState(connectpb.PutStateReason_PLAYER_STATE_CHANGED)
}

func (p *AppPlayer) scheduleConnectState(reason connectpb.PutStateReason) {
	if p == nil {
		return
	}
	if p.connectStateTimer == nil {
		ctx, cancel := context.WithTimeout(p.ownerContext(), shuffleContextTimeout)
		defer cancel()
		if err := p.putConnectState(ctx, reason); err != nil {
			p.runtime.Log.WithError(err).Error("failed put state after update")
		}
		return
	}
	p.pendingConnectReason = reason
	p.pendingConnectPut = true
	stopAndResetTimer(p.connectStateTimer, connectStateDebounce)
}

func (p *AppPlayer) flushConnectState() {
	if p == nil || !p.pendingConnectPut {
		return
	}
	reason := p.pendingConnectReason
	p.pendingConnectPut = false
	ctx, cancel := context.WithTimeout(p.ownerContext(), shuffleContextTimeout)
	defer cancel()
	if err := p.putConnectState(ctx, reason); err != nil {
		p.runtime.Log.WithError(err).Error("failed put state after update")
	}
}

func (p *AppPlayer) putConnectState(ctx context.Context, reason connectpb.PutStateReason) error {
	if reason == connectpb.PutStateReason_BECAME_INACTIVE {
		return p.sess.Spclient().PutConnectStateInactive(ctx, p.spotConnId, false)
	}

	var hasBeenPlayingForMs uint64
	if p.state.active && !p.state.activeSince.IsZero() {
		if t := time.Since(p.state.activeSince); t > 0 {
			hasBeenPlayingForMs = uint64(t.Milliseconds())
		}
	}

	var lastCmdMsgId uint32
	var lastCmdSentBy string
	if p.state.lastCommand != nil {
		lastCmdMsgId = p.state.lastCommand.MessageId
		lastCmdSentBy = p.state.lastCommand.SentByDeviceId
	}

	putStateReq := golibrespot.BuildPutStateRequest(golibrespot.PutStateOpts{
		Device:                    p.state.device,
		PlayerState:               p.state.player,
		Active:                    p.state.active,
		ActiveSince:               p.state.activeSince,
		LastCommandMsgId:          lastCmdMsgId,
		LastCommandSentByDeviceId: lastCmdSentBy,
		HasBeenPlayingForMs:       hasBeenPlayingForMs,
	}, reason)

	return p.sess.Spclient().PutConnectState(ctx, p.spotConnId, putStateReq)
}
