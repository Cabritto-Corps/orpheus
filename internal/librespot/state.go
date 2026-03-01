package librespot

import (
	"context"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/dealer"
	"github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	"github.com/devgianlu/go-librespot/tracks"
)

type State struct {
	active       bool
	activeSince  time.Time
	device       *connectpb.DeviceInfo
	player       *connectpb.PlayerState
	tracks       *tracks.List
	queueID      uint64
	lastCommand  *dealer.RequestPayload
	lastTransferTimestamp int64
}

func (s *State) setPaused(val bool) {
	s.player.IsPaused = val
	if val {
		s.player.PlaybackSpeed = 0
	} else {
		s.player.PlaybackSpeed = 1
	}
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
	s.player = &connectpb.PlayerState{
		IsSystemInitiated: true,
		PlaybackSpeed:     1,
		PlayOrigin:        &connectpb.PlayOrigin{},
		Suppressions:      &connectpb.Suppressions{},
		Options:           &connectpb.ContextPlayerOptions{},
	}
}

func (s *State) trackPosition() int64 {
	if s.player.IsPaused || !s.player.IsPlaying {
		return s.player.PositionAsOfTimestamp
	}
	now := time.Now().UnixMilli()
	elapsed := now - s.player.Timestamp
	const maxReasonableElapsed = 10 * 60 * 1000
	if elapsed > maxReasonableElapsed || elapsed < 0 {
		return s.player.PositionAsOfTimestamp
	}
	calculated := s.player.PositionAsOfTimestamp + elapsed
	if calculated < 0 {
		return s.player.PositionAsOfTimestamp
	}
	return calculated
}

func (s *State) updateTimestamp() {
	now := time.Now()
	advancedTimeMillis := now.UnixMilli() - s.player.Timestamp
	advancedPositionMillis := int64(float64(advancedTimeMillis) * s.player.PlaybackSpeed)
	s.player.PositionAsOfTimestamp += advancedPositionMillis
	s.player.Timestamp = now.UnixMilli()
}

func (s *State) playOrigin() string {
	return s.player.PlayOrigin.FeatureIdentifier
}

func (p *AppPlayer) initState() {
	cfg := p.runtime.Cfg
	p.state = &State{
		lastCommand: nil,
		device: &connectpb.DeviceInfo{
			CanPlay:               true,
			Volume:                player.MaxStateVolume,
			Name:                  cfg.DeviceName,
			DeviceId:               p.runtime.DeviceId,
			DeviceType:             p.runtime.DeviceType,
			DeviceSoftwareVersion:  golibrespot.VersionString(),
			ClientId:               golibrespot.ClientIdHex,
			SpircVersion:           "3.2.6",
			Capabilities: &connectpb.Capabilities{
				CanBePlayer:                  true,
				RestrictToLocal:              false,
				GaiaEqConnectId:             true,
				SupportsLogout:               cfg.ZeroconfEnabled,
				IsObservable:                 true,
				VolumeSteps:                  int32(cfg.VolumeSteps),
				SupportedTypes:               []string{"audio/track", "audio/episode"},
				CommandAcks:                  true,
				SupportsRename:               false,
				Hidden:                       false,
				DisableVolume:                false,
				ConnectDisabled:              false,
				SupportsPlaylistV2:           true,
				IsControllable:               true,
				SupportsExternalEpisodes:     false,
				SupportsSetBackendMetadata:    true,
				SupportsTransferCommand:      true,
				SupportsCommandRequest:       true,
				IsVoiceEnabled:               false,
				NeedsFullPlayerState:         false,
				SupportsGzipPushes:           true,
				SupportsSetOptionsCommand:    true,
				SupportsHifi:                  nil,
				ConnectCapabilities:          "",
			},
		},
	}
	p.state.reset()
}

func (p *AppPlayer) updateState(ctx context.Context) {
	if err := p.putConnectState(ctx, connectpb.PutStateReason_PLAYER_STATE_CHANGED); err != nil {
		p.runtime.Log.WithError(err).Error("failed put state after update")
	}
}

func (p *AppPlayer) putConnectState(ctx context.Context, reason connectpb.PutStateReason) error {
	if reason == connectpb.PutStateReason_BECAME_INACTIVE {
		return p.sess.Spclient().PutConnectStateInactive(ctx, p.spotConnId, false)
	}
	putStateReq := &connectpb.PutStateRequest{
		ClientSideTimestamp: uint64(time.Now().UnixMilli()),
		MemberType:          connectpb.MemberType_CONNECT_STATE,
		PutStateReason:     reason,
	}
	if t := p.state.activeSince; !t.IsZero() {
		putStateReq.StartedPlayingAt = uint64(t.UnixMilli())
	}
	if t := p.player.HasBeenPlayingFor(); t > 0 {
		putStateReq.HasBeenPlayingForMs = uint64(t.Milliseconds())
	}
	putStateReq.IsActive = p.state.active
	putStateReq.Device = &connectpb.Device{
		DeviceInfo:  p.state.device,
		PlayerState: p.state.player,
	}
	if p.state.lastCommand != nil {
		putStateReq.LastCommandMessageId = p.state.lastCommand.MessageId
		putStateReq.LastCommandSentByDeviceId = p.state.lastCommand.SentByDeviceId
	}
	return p.sess.Spclient().PutConnectState(ctx, p.spotConnId, putStateReq)
}
