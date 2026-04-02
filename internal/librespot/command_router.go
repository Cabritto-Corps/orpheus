package librespot

import (
	"context"
	"fmt"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/tracks"

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
		p.suppressEmit = true
		if err := p.loadContext(ctx, spotCtx, skipTo, false, true); err != nil {
			p.suppressEmit = false
			return true, err
		}
		p.suppressEmit = false
		if p.state.tracks != nil {
			if p.state.tracks.CurrentTrack() != nil {
				currentID := golibrespot.NormalizeSpotifyId(p.state.tracks.CurrentTrack().Uri)
				if currentID != targetID {
					p.runtime.Log.Warnf("track %s not found in context, started from beginning", targetID)
				}
			}
			p.state.tracks.WrapPlaybackFromCurrent()
			p.syncPlayerTrackState(ctx, p.state.tracks, nil)
			p.emitPlaybackState()
		}
		return true, nil
	case TUICommandGetContextTracks:
		spotCtx, err := p.sess.Spclient().ContextResolve(ctx, cmd.URI)
		if err != nil {
			return true, fmt.Errorf("failed resolving context for tracks: %w", err)
		}
		ctxTracks, err := tracks.NewTrackListFromContext(ctx, p.runtime.Log, p.sess.Spclient(), spotCtx, 0)
		if err != nil {
			return true, fmt.Errorf("failed creating track list: %w", err)
		}
		allProvided := ctxTracks.AllTracks(ctx)

		trackURIs := make([]string, 0, len(allProvided))
		result := make([]PlaybackStateQueueEntry, 0, len(allProvided))
		for _, t := range allProvided {
			if t == nil {
				continue
			}
			id := golibrespot.NormalizeSpotifyId(t.Uri)
			trackURIs = append(trackURIs, t.Uri)
			result = append(result, PlaybackStateQueueEntry{ID: id, Name: "Unknown track", Artist: "-"})
		}

		if len(trackURIs) > 0 {
			metaCtx, metaCancel := context.WithTimeout(ctx, 15*time.Second)
			batchMeta, metaErr := p.sess.Spclient().ResolveTrackOrEpisodeMetadataBatch(metaCtx, trackURIs)
			metaCancel()
			if metaErr == nil {
				for uri, entry := range batchMeta {
					id := golibrespot.NormalizeSpotifyId(uri)
					for i := range result {
						if result[i].ID == id {
							result[i].Name = entry.Name
							artist := entry.Artist
							if artist == "" {
								artist = "-"
							}
							result[i].Artist = artist
							result[i].DurationMS = entry.DurationMS
							break
						}
					}
				}
			} else {
				p.runtime.Log.WithError(metaErr).Warn("failed resolving track metadata batch for context tracks")
			}
		}

		if cmd.ResultCh != nil {
			select {
			case cmd.ResultCh <- result:
			default:
				p.runtime.Log.Warn("dropped context tracks result, no receiver")
			}
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
