package librespot

import (
	"context"
	"fmt"
	"strings"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	playerpb "github.com/elxgy/go-librespot/proto/spotify/player"
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
	case TUICommandPlayStation:
		stationCtx, err := resolveAutoplayContextWithRetry(ctx, func(resolveCtx context.Context) (*connectpb.Context, error) {
			return p.sess.Spclient().ContextResolveAutoplay(resolveCtx, &playerpb.AutoplayContextRequest{
				ContextUri: new(cmd.URI),
			})
		}, waitAutoplayResolveRetry)
		if err != nil {
			return true, fmt.Errorf("failed resolving station for %s: %w", cmd.URI, err)
		}
		p.state.setActive(true)
		golibrespot.SetPaused(p.state.player, false)
		p.state.player.Suppressions = &connectpb.Suppressions{}
		p.state.player.PlayOrigin = &connectpb.PlayOrigin{
			FeatureIdentifier: "go-librespot",
			FeatureVersion:    golibrespot.VersionNumberString(),
		}
		return true, p.loadAutoplayContext(ctx, stationCtx, true)
	case TUICommandPlayContextFromTrack:
		targetID := golibrespot.NormalizeSpotifyId(cmd.TrackID)
		if targetID == "" {
			return false, fmt.Errorf("empty track ID for play-from-track")
		}
		// Autoplay stations may not be resolvable again as ordinary contexts.
		// Their selected upcoming tracks are already in the active TrackList,
		// so seek that list directly before trying a Web API context resolve.
		if p.seekCurrentContextTrack(ctx, cmd.URI, targetID) {
			p.state.setActive(true)
			golibrespot.SetPaused(p.state.player, false)
			p.state.player.Suppressions = &connectpb.Suppressions{}
			p.state.player.PlayOrigin = &connectpb.PlayOrigin{
				FeatureIdentifier: "go-librespot",
				FeatureVersion:    golibrespot.VersionNumberString(),
			}
			p.bumpPrefetchGeneration()
			p.syncPlayerTrackState(p.state.tracks, nil)
			return true, p.loadCurrentTrackFromTransition(ctx, false, true, "play loaded context track")
		}
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
		skipTo := func(track *connectpb.ContextTrack) bool {
			return golibrespot.NormalizeSpotifyId(track.Uri) == targetID
		}
		p.suppressEmit = true
		err = p.loadContext(ctx, spotCtx, skipTo, false, true)
		p.suppressEmit = false
		if err != nil {
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
			p.syncPlayerTrackState(p.state.tracks, nil)
			p.emitPlaybackState()
		}
		return true, nil
	case TUICommandPlayTrack:
		spotCtx, err := singleTrackContext(cmd.URI)
		if err != nil {
			return true, err
		}
		p.state.setActive(true)
		golibrespot.SetPaused(p.state.player, false)
		p.state.player.Suppressions = &connectpb.Suppressions{}
		p.state.player.PlayOrigin = &connectpb.PlayOrigin{
			FeatureIdentifier: "go-librespot",
			FeatureVersion:    golibrespot.VersionNumberString(),
		}
		return true, p.loadContext(ctx, spotCtx, nil, false, true)
	case TUICommandPlayTracks:
		spotCtx, err := trackListContext(cmd.URI, cmd.URIs)
		if err != nil {
			return true, err
		}
		p.state.setActive(true)
		golibrespot.SetPaused(p.state.player, false)
		p.state.player.Suppressions = &connectpb.Suppressions{}
		p.state.player.PlayOrigin = &connectpb.PlayOrigin{
			FeatureIdentifier: "go-librespot",
			FeatureVersion:    golibrespot.VersionNumberString(),
		}
		return true, p.loadContext(ctx, spotCtx, nil, false, true)
	case TUICommandGetContextTracks:
		resultCh := cmd.ResultCh
		reqToken := cmd.ReqToken
		uri := cmd.URI
		go func() {
			bgCtx, cancel := context.WithTimeout(p.ownerContext(), contextTracksBgTimeout)
			defer cancel()

			spotCtx, err := p.sess.Spclient().ContextResolve(bgCtx, uri)
			if err != nil {
				if bgCtx.Err() == nil {
					p.runtime.Log.WithError(err).Error("failed resolving context for tracks")
				}
				if resultCh != nil {
					select {
					case resultCh <- ContextTracksResult{ReqToken: reqToken}:
					default:
					}
				}
				return
			}
			ctxTracks, err := tracks.NewTrackListFromContext(bgCtx, p.runtime.Log, p.sess.Spclient(), spotCtx, 0)
			if err != nil {
				if bgCtx.Err() == nil {
					p.runtime.Log.WithError(err).Error("failed creating track list for context tracks")
				}
				if resultCh != nil {
					select {
					case resultCh <- ContextTracksResult{ReqToken: reqToken}:
					default:
					}
				}
				return
			}
			allProvided := ctxTracks.AllTracks(bgCtx)

			result := make([]PlaybackStateQueueEntry, 0, len(allProvided))
			indexesByID := make(map[string][]int, len(allProvided))
			seenMissing := make(map[string]struct{}, len(allProvided))
			var missingURIs []string
			for _, t := range allProvided {
				if t == nil {
					continue
				}
				id := golibrespot.NormalizeSpotifyId(t.Uri)
				e := PlaybackStateQueueEntry{ID: id}
				if t.Metadata != nil {
					e.Name = metadataValue(t.Metadata, "title", "name", "track_name", "entity_name", "track_title")
					e.Artist = metadataValue(t.Metadata, "artist_name", "artist", "artists", "show_name", "album_artist_name")
					e.DurationMS = metadataDurationMS(t.Metadata)
				}
				if e.Artist == "" {
					e.Artist = "-"
				}
				if e.Name == "" {
					e.Name = "Unknown track"
					if _, ok := seenMissing[id]; !ok {
						seenMissing[id] = struct{}{}
						missingURIs = append(missingURIs, t.Uri)
					}
				}
				indexesByID[id] = append(indexesByID[id], len(result))
				result = append(result, e)
			}

			send := func() {
				if bgCtx.Err() == nil && resultCh != nil {
					select {
					case resultCh <- ContextTracksResult{ReqToken: reqToken, Entries: append([]PlaybackStateQueueEntry(nil), result...)}:
					default:
						p.runtime.Log.Warn("dropped context tracks result, no receiver")
					}
				}
			}
			sent := len(missingURIs) == 0
			if sent {
				send()
			}
			resolved := 0
			for start := 0; start < len(missingURIs); start += queueMetaBatchChunk {
				if bgCtx.Err() != nil {
					break
				}
				end := min(start+queueMetaBatchChunk, len(missingURIs))
				chunkCtx, chunkCancel := context.WithTimeout(bgCtx, metadataBatchTimeout)
				batchMeta, metaErr := p.sess.Spclient().ResolveTrackOrEpisodeMetadataBatch(chunkCtx, missingURIs[start:end])
				chunkCancel()
				if metaErr != nil {
					if bgCtx.Err() == nil {
						p.runtime.Log.WithError(metaErr).Warn("failed resolving track metadata batch for context tracks")
					}
					continue
				}
				chunkResolved := 0
				for uri, entry := range batchMeta {
					idxs, ok := indexesByID[golibrespot.NormalizeSpotifyId(uri)]
					if !ok || entry.Name == "" {
						continue
					}
					artist := entry.Artist
					if artist == "" {
						artist = "-"
					}
					for _, idx := range idxs {
						result[idx] = PlaybackStateQueueEntry{ID: result[idx].ID, Name: entry.Name, Artist: artist, DurationMS: entry.DurationMS, ImageURL: queueMetaImageURL(p, entry.AlbumCoverFileId)}
					}
					chunkResolved++
				}
				resolved += chunkResolved
				if chunkResolved > 0 {
					send()
					sent = true
				}
			}
			if !sent {
				send()
			}
			p.runtime.Log.WithField("uri", uri).WithField("resolved", resolved).WithField("tracks", len(result)).Debug("context tracks resolved")
		}()
		return true, nil
	default:
		return false, nil
	}
}

func (p *AppPlayer) seekCurrentContextTrack(ctx context.Context, contextURI, trackID string) bool {
	if p == nil || p.state == nil || p.state.player == nil || p.state.tracks == nil ||
		strings.TrimSpace(p.state.player.ContextUri) != strings.TrimSpace(contextURI) {
		return false
	}
	targetID := golibrespot.NormalizeSpotifyId(trackID)
	if targetID == "" {
		return false
	}
	typ := golibrespot.InferSpotifyIdTypeFromContextUri(contextURI)
	target := &connectpb.ContextTrack{Uri: "spotify:track:" + targetID}
	return p.state.tracks.Seek(ctx, tracks.ContextTrackComparator(typ, target)) == nil
}

func singleTrackContext(uri string) (*connectpb.Context, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, fmt.Errorf("empty track URI")
	}
	id, err := golibrespot.SpotifyIdFromUri(uri)
	if err != nil || id == nil || id.Type() != golibrespot.SpotifyIdTypeTrack {
		return nil, fmt.Errorf("invalid Spotify track URI %q", uri)
	}
	trackURI := id.Uri()
	return &connectpb.Context{
		Uri: trackURI,
		Pages: []*connectpb.ContextPage{{
			Tracks: []*connectpb.ContextTrack{{Uri: trackURI}},
		}},
	}, nil
}

func trackListContext(contextURI string, uris []string) (*connectpb.Context, error) {
	contextURI = strings.TrimSpace(contextURI)
	if len(uris) == 0 {
		return nil, fmt.Errorf("empty track list")
	}
	tracks := make([]*connectpb.ContextTrack, 0, len(uris))
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		id, err := golibrespot.SpotifyIdFromUri(uri)
		if err != nil || id == nil || id.Type() != golibrespot.SpotifyIdTypeTrack {
			return nil, fmt.Errorf("invalid Spotify track URI %q", uri)
		}
		trackURI := id.Uri()
		tracks = append(tracks, &connectpb.ContextTrack{Uri: trackURI})
	}
	if contextURI == "" {
		contextURI = tracks[0].Uri
	}
	return &connectpb.Context{
		Uri: contextURI,
		Pages: []*connectpb.ContextPage{{
			Tracks: tracks,
		}},
	}, nil
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
		vol := min(uint32(cmd.Volume)*player.MaxStateVolume/p.runtime.Cfg.VolumeSteps, player.MaxStateVolume)
		p.updateVolume(vol)
		return true, nil
	case TUICommandShuffle:
		if p.state == nil || p.state.player == nil || p.state.player.Options == nil {
			if p.runtime != nil {
				p.runtime.Log.Warn("shuffle ignored: no active playback state")
			}
			return true, nil
		}
		target := !p.state.player.Options.ShufflingContext
		return true, p.setOptions(ctx, nil, nil, &target, nil)
	case TUICommandCycleRepeat:
		if p.state == nil || p.state.player == nil || p.state.player.Options == nil {
			if p.runtime != nil {
				p.runtime.Log.Warn("repeat cycle ignored: no active playback state")
			}
			return true, nil
		}
		curr := playbackdomain.TraversalOptions{
			RepeatContext: p.state.player.Options.RepeatingContext,
			RepeatTrack:   p.state.player.Options.RepeatingTrack,
			Shuffle:       p.state.player.Options.ShufflingContext,
		}
		next := playbackdomain.NextRepeatTraversalOptions(curr)
		return true, p.setOptions(ctx, &next.RepeatContext, &next.RepeatTrack, nil, nil)
	case TUICommandQueueRemove:
		return true, p.queueRemove(cmd.QueueIndex)
	case TUICommandQueueReorder:
		return true, p.queueReorder(cmd.QueueIndex, cmd.QueueTargetIndex)
	case TUICommandQueueJump:
		return true, p.queueJump(ctx, cmd.QueueIndex)
	default:
		return false, nil
	}
}
