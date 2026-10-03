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

// connectPutJob is a prebuilt connect-state PUT: snapshot built on Run, sent by the worker; carries no shared references.
type connectPutJob struct {
	reason   connectpb.PutStateReason
	inactive bool
	connId   string
	req      *connectpb.PutStateRequest
}

func (p *AppPlayer) flushConnectState() {
	if p == nil || !p.pendingConnectPut {
		return
	}
	reason := p.pendingConnectReason
	p.pendingConnectPut = false
	p.enqueueConnectPut(p.buildConnectPut(reason))
}

// Keep-latest: drain pending, insert the newest; a nil channel (tests) is a no-op.
func (p *AppPlayer) enqueueConnectPut(job connectPutJob) {
	if p == nil || p.connectPutJobs == nil {
		return
	}
	select {
	case <-p.connectPutJobs:
	default:
	}
	select {
	case p.connectPutJobs <- job:
	default:
	}
}

func (p *AppPlayer) runConnectPutWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.connectPutJobs:
			jobCtx, cancel := context.WithTimeout(p.ownerContext(), shuffleContextTimeout)
			err := p.sendConnectPut(jobCtx, job)
			cancel()
			if err != nil && ctx.Err() == nil {
				p.runtime.Log.WithError(err).WithField("reason", job.reason.String()).Error("failed put state (background)")
			}
		}
	}
}

func (p *AppPlayer) sendConnectPut(ctx context.Context, job connectPutJob) error {
	if job.inactive {
		return p.sess.Spclient().PutConnectStateInactive(ctx, job.connId, false)
	}
	return p.sess.Spclient().PutConnectState(ctx, job.connId, job.req)
}

func (p *AppPlayer) putConnectState(ctx context.Context, reason connectpb.PutStateReason) error {
	return p.sendConnectPut(ctx, p.buildConnectPut(reason))
}

func (p *AppPlayer) buildConnectPut(reason connectpb.PutStateReason) connectPutJob {
	if reason == connectpb.PutStateReason_BECAME_INACTIVE {
		return connectPutJob{reason: reason, inactive: true, connId: p.spotConnId}
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

	return connectPutJob{reason: reason, connId: p.spotConnId, req: putStateReq}
}
