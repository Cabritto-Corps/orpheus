package librespot

import (
	"context"
	"fmt"

	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
)

func (p *AppPlayer) handleTUIContextCommand(ctx context.Context, cmd TUICommand) (bool, error) {
	switch cmd.Kind {
	case TUICommandPlayContext:
		spotCtx, err := p.sess.Spclient().ContextResolve(ctx, cmd.URI)
		if err != nil {
			return true, fmt.Errorf("failed resolving context: %w", err)
		}
		p.state.setActive(true)
		p.state.setPaused(false)
		p.state.player.Suppressions = &connectpb.Suppressions{}
		p.state.player.PlayOrigin = &connectpb.PlayOrigin{
			FeatureIdentifier: "go-librespot",
			FeatureVersion:    golibrespot.VersionNumberString(),
		}
		return true, p.loadContext(ctx, spotCtx, nil, false, true)
	default:
		return false, nil
	}
}

func (p *AppPlayer) handleTUIPlaybackCommand(ctx context.Context, cmd TUICommand) (bool, error) {
	switch cmd.Kind {
	case TUICommandPause:
		_ = p.pause(ctx)
		return true, nil
	case TUICommandResume:
		_ = p.play(ctx)
		return true, nil
	case TUICommandSeek:
		_ = p.seek(ctx, cmd.Position)
		return true, nil
	case TUICommandSkipNext:
		_ = p.skipNext(ctx, nil)
		return true, nil
	case TUICommandSkipPrev:
		_ = p.skipPrev(ctx, true)
		return true, nil
	case TUICommandSetVolume:
		vol := uint32(cmd.Volume) * player.MaxStateVolume / p.runtime.Cfg.VolumeSteps
		if vol > player.MaxStateVolume {
			vol = player.MaxStateVolume
		}
		p.updateVolume(vol)
		return true, nil
	case TUICommandShuffle:
		if p.state.tracks == nil {
			return true, nil
		}
		if p.state.player.Options.ShufflingContext {
			if err := p.state.tracks.ToggleShuffle(ctx, false); err != nil {
				return true, fmt.Errorf("toggle shuffle off: %w", err)
			}
		}
		if err := p.state.tracks.ToggleShuffle(ctx, true); err != nil {
			return true, fmt.Errorf("toggle shuffle on: %w", err)
		}
		p.state.player.Options.ShufflingContext = true
		p.resetPlaybackCaches(true)
		p.syncPlayerTrackState(ctx, p.state.tracks, nil)
		p.scheduleShuffleCacheRefresh()
		p.updateState(ctx)
		p.emitPlaybackState()
		return true, nil
	case TUICommandCycleRepeat:
		if p.state == nil || p.state.player == nil || p.state.player.Options == nil {
			return true, nil
		}
		repeatContext := false
		repeatTrack := false
		if p.state.player.Options.RepeatingTrack {
			repeatContext = false
			repeatTrack = false
		} else if p.state.player.Options.RepeatingContext {
			repeatContext = false
			repeatTrack = true
		} else {
			repeatContext = true
			repeatTrack = false
		}
		p.setOptions(ctx, &repeatContext, &repeatTrack, nil)
		return true, nil
	default:
		return false, nil
	}
}
