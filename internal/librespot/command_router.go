package librespot

import (
	"context"
	"fmt"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"

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
		golibrespot.SetPaused(p.state.player, false)
		p.state.player.Suppressions = &connectpb.Suppressions{}
		p.state.player.PlayOrigin = &connectpb.PlayOrigin{
			FeatureIdentifier: "go-librespot",
			FeatureVersion:    golibrespot.VersionNumberString(),
		}
		return true, p.loadContext(ctx, spotCtx, nil, false, true)
	case TUICommandPlayContextFromTrack:
		spotCtx, err := p.sess.Spclient().ContextResolve(ctx, cmd.URI)
		if err != nil {
			return true, fmt.Errorf("failed resolving context: %w", err)
		}
		p.state.setActive(true)
		golibrespot.SetPaused(p.state.player, false)
		p.state.player.Suppressions = &connectpb.Suppressions{}
		p.state.player.PlayOrigin = &connectpb.PlayOrigin{
			FeatureIdentifier: "go-librespot",
			FeatureVersion:    golibrespot.VersionNumberString(),
		}
		targetID := golibrespot.NormalizeSpotifyId(cmd.TrackID)
		if targetID == "" {
			return false, fmt.Errorf("empty track ID for play-from-track")
		}
		skipTo := func(track *connectpb.ContextTrack) bool {
			return golibrespot.NormalizeSpotifyId(track.Uri) == targetID
		}
		if err := p.loadContext(ctx, spotCtx, skipTo, false, true); err != nil {
			return true, err
		}
		if p.state.tracks != nil {
			if p.state.tracks.CurrentTrack() != nil {
				currentID := golibrespot.NormalizeSpotifyId(p.state.tracks.CurrentTrack().Uri)
				if currentID != targetID {
					p.runtime.Log.Warnf("track %s not found in context, started from beginning", targetID)
				}
			}
			p.state.tracks.WrapPlaybackFromCurrent()
			p.syncPlayerTrackState(ctx, p.state.tracks, nil)
		}
		return true, nil
	default:
		return false, nil
	}
}

func (p *AppPlayer) handleTUIPlaybackCommand(ctx context.Context, cmd TUICommand) (bool, error) {
	switch cmd.Kind {
	case TUICommandPause:
		return true, p.pause(ctx)
	case TUICommandResume:
		return true, p.play(ctx)
	case TUICommandSeek:
		return true, p.seek(ctx, cmd.Position)
	case TUICommandSkipNext:
		return true, p.skipNext(ctx, nil)
	case TUICommandSkipPrev:
		return true, p.skipPrev(ctx, true)
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
