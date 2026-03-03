package librespot

import (
	"context"
	"fmt"

	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"

	"orpheus/internal/playbackdomain"
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
		if p.state == nil || p.state.player == nil || p.state.player.Options == nil {
			return true, nil
		}
		target := !p.state.player.Options.ShufflingContext
		return true, p.setOptions(ctx, nil, nil, &target)
	case TUICommandCycleRepeat:
		if p.state == nil || p.state.player == nil || p.state.player.Options == nil {
			return true, nil
		}
		curr := playbackdomain.TraversalOptions{
			RepeatContext: p.state.player.Options.RepeatingContext,
			RepeatTrack:   p.state.player.Options.RepeatingTrack,
			Shuffle:       p.state.player.Options.ShufflingContext,
		}
		next := playbackdomain.NextRepeatTraversalOptions(curr)
		return true, p.setOptions(ctx, &next.RepeatContext, &next.RepeatTrack, nil)
	default:
		return false, nil
	}
}
