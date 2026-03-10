package librespot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/player"
	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
	playerpb "github.com/devgianlu/go-librespot/proto/spotify/player"
	"github.com/devgianlu/go-librespot/tracks"
	"google.golang.org/protobuf/proto"

	"orpheus/internal/playbackdomain"
)

func (p *AppPlayer) prefetchCandidateIDs(ctx context.Context) []golibrespot.SpotifyId {
	if p.state == nil || p.state.tracks == nil || p.state.player == nil {
		return nil
	}
	candidates := make([]golibrespot.SpotifyId, 0, transitionStreamCacheMax)
	seen := make(map[string]struct{}, transitionStreamCacheMax)
	repeatTrack := p.state.player.Options != nil && p.state.player.Options.RepeatingTrack
	appendCandidate := func(uri string) {
		id, err := golibrespot.SpotifyIdFromUri(strings.TrimSpace(uri))
		if err != nil {
			return
		}
		key := id.Uri()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, *id)
	}

	if repeatTrack && p.state.player.Track != nil {
		appendCandidate(p.state.player.Track.Uri)
	}
	if next := p.state.tracks.PeekNext(ctx); next != nil {
		appendCandidate(next.Uri)
	}
	for i := 0; i < len(p.state.player.NextTracks) && len(candidates) < transitionStreamCacheMax; i++ {
		appendCandidate(p.state.player.NextTracks[i].Uri)
	}
	if n := len(p.state.player.PrevTracks); n > 0 && len(candidates) < transitionStreamCacheMax {
		appendCandidate(p.state.player.PrevTracks[n-1].Uri)
	}
	return candidates
}

func (p *AppPlayer) clearSecondaryStream() {
	p.secondaryStream = nil
	if p.player != nil {
		func() {
			defer func() { _ = recover() }()
			p.player.SetSecondaryStream(nil)
		}()
	}
}

func (p *AppPlayer) resetPlaybackCaches(stopShuffleRefresh bool) {
	p.clearTransitionStreamCache()
	p.bumpPrefetchGeneration()
	p.clearSecondaryStream()
	if stopShuffleRefresh {
		p.shuffleRefreshPending = false
		stopTimer(p.shuffleRefreshTimer)
	}
}

func (p *AppPlayer) resetTrackTransitionPosition() {
	p.state.player.Timestamp = time.Now().UnixMilli()
	p.state.player.PositionAsOfTimestamp = 0
}

func (p *AppPlayer) currentPositionMs() int64 {
	if p.state == nil || p.state.player == nil {
		return 0
	}
	pos := p.state.trackPosition()
	if p.state.player.Duration > 0 && pos > p.state.player.Duration {
		return p.state.player.Duration
	}
	return pos
}

func (p *AppPlayer) loadCurrentTrackFromTransition(ctx context.Context, paused, drop bool, reason string) error {
	p.resetTrackTransitionPosition()
	if err := p.loadCurrentTrack(ctx, paused, drop); err != nil {
		return fmt.Errorf("failed loading current track (%s): %w", reason, err)
	}
	return nil
}

const (
	shuffleCacheRefreshDelay   = 2 * time.Second
	prefetchLeadTime           = 30 * time.Second
	prefetchImmediateThreshold = 10 * time.Second
)

func stopAndResetTimer(t *time.Timer, d time.Duration) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func stopTimer(t *time.Timer) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func (p *AppPlayer) scheduleShuffleCacheRefresh() {
	p.shuffleRefreshPending = true
	p.shuffleRefreshGen = p.prefetchGen.Load()
	p.runtime.Log.Debugf("scheduled shuffle cache refresh in %.1fs (gen=%d)", shuffleCacheRefreshDelay.Seconds(), p.shuffleRefreshGen)
	stopAndResetTimer(p.shuffleRefreshTimer, shuffleCacheRefreshDelay)
}

func (p *AppPlayer) handleShuffleCacheRefresh(ctx context.Context) {
	if !p.shuffleRefreshPending {
		return
	}
	p.shuffleRefreshPending = false
	if p.shuffleRefreshGen != p.prefetchGen.Load() {
		return
	}
	p.runtime.Log.Debug("running debounced shuffle cache refresh")
	p.prefetchNext(ctx)
}

func (p *AppPlayer) prefetchNext(ctx context.Context) {
	candidates := p.prefetchCandidateIDs(ctx)
	if len(candidates) == 0 {
		return
	}
	nextURI := candidates[0].Uri()
	gen := p.prefetchGen.Load()
	for i := range candidates {
		id := candidates[i]
		if p.primaryStream != nil && p.primaryStream.Is(id) {
			continue
		}
		if p.secondaryStream != nil && p.secondaryStream.Is(id) {
			continue
		}
		if p.hasTransitionCachedStream(id) {
			continue
		}
		if p.hasPrefetchPending(id) {
			continue
		}
		if !p.markPrefetchPending(id) {
			continue
		}
		select {
		case p.prefetchJobs <- prefetchJob{gen: gen, nextURI: nextURI, target: id}:
		default:
			p.clearPrefetchPending(id)
			return
		}
	}
}

func (p *AppPlayer) schedulePrefetchNext() {
	if p.state.player.IsPaused || p.primaryStream == nil {
		p.prefetchTimer.Stop()
		return
	}
	if p.secondaryStream == nil {
		p.prefetchTimer.Reset(0)
		p.runtime.Log.Tracef("prefetch immediately (no secondary stream)")
		return
	}
	untilTrackEnd := time.Duration(p.primaryStream.Media.Duration()-int32(p.currentPositionMs())) * time.Millisecond
	untilTrackEnd -= prefetchLeadTime
	if untilTrackEnd < 0 {
		untilTrackEnd = 0
	}
	if untilTrackEnd < prefetchImmediateThreshold {
		p.prefetchTimer.Reset(0)
		p.runtime.Log.Tracef("prefetch as soon as possible")
	} else {
		p.prefetchTimer.Reset(untilTrackEnd)
		p.runtime.Log.Tracef("scheduling prefetch in %.0fs", untilTrackEnd.Seconds())
	}
}

func (p *AppPlayer) runPrefetchWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.prefetchJobs:
			jobCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			stream, err := p.player.NewStream(jobCtx, p.runtime.Client, job.target, p.runtime.Cfg.Bitrate, 0)
			cancel()
			p.prefetchDone <- prefetchResult{gen: job.gen, nextURI: job.nextURI, target: job.target, stream: stream, err: err}
		}
	}
}

func (p *AppPlayer) handlePrefetchResult(res prefetchResult) {
	p.clearPrefetchPending(res.target)
	if res.gen != p.prefetchGen.Load() {
		p.runtime.Log.WithField("uri", res.target.Uri()).Tracef("dropping stale prefetch result (res_gen=%d current_gen=%d)", res.gen, p.prefetchGen.Load())
		return
	}
	if res.err != nil {
		p.runtime.Log.WithError(res.err).WithField("uri", res.target.String()).Warnf("failed prefetching %s stream", res.target.Type())
		return
	}
	if p.primaryStream != nil && p.primaryStream.Is(res.target) {
		return
	}
	if p.secondaryStream != nil && p.secondaryStream.Is(res.target) {
		return
	}
	if p.hasTransitionCachedStream(res.target) {
		return
	}
	repeatTrack := p.state != nil &&
		p.state.player != nil &&
		p.state.player.Options != nil &&
		p.state.player.Options.RepeatingTrack
	currentID := ""
	if p.state != nil && p.state.player != nil && p.state.player.Track != nil {
		currentID = normalizeSpotifyID(p.state.player.Track.Uri)
	}
	targetID := normalizeSpotifyID(res.target.Uri())
	if repeatTrack && targetID != "" && targetID != currentID {
		p.runtime.Log.WithField("uri", res.target.Uri()).Trace("repeat-track mode: keeping prefetched target in transition cache, not secondary")
		p.putTransitionCachedStream(res.target, res.stream)
		return
	}
	if res.nextURI == res.target.Uri() && p.secondaryStream == nil {
		p.secondaryStream = res.stream
		func() {
			defer func() { _ = recover() }()
			p.player.SetSecondaryStream(res.stream.Source)
		}()
	} else {
		p.putTransitionCachedStream(res.target, res.stream)
	}
	p.runtime.Log.WithField("uri", res.target.Uri()).Infof("prefetched %s %s (duration: %dms)", res.target.Type(), strconv.QuoteToGraphic(res.stream.Media.Name()), res.stream.Media.Duration())
}

func (p *AppPlayer) syncPlayerTrackState(ctx context.Context, trackList *tracks.List, nextHint []*connectpb.ContextTrack) {
	if p.state == nil || p.state.player == nil || trackList == nil {
		return
	}
	p.state.player.Track = trackList.CurrentTrack()
	p.state.player.PrevTracks = trackList.PrevTracks()
	p.state.player.NextTracks = trackList.NextTracks(ctx, nextHint)
	p.state.player.Index = trackList.Index()
	p.bumpTrackStateVersion()
}

func (p *AppPlayer) logRepeatShuffleInvariant(source string) {
	if p.state == nil || p.state.player == nil || p.state.player.Options == nil {
		return
	}
	if p.state.player.Options.RepeatingTrack && p.state.player.Options.ShufflingContext {
		p.runtime.Log.WithField("source", source).Debug("repeat-track active while shuffle-context is enabled")
	}
}

func (p *AppPlayer) logEndOfTrackInvariant() {
	if p.state == nil || p.state.player == nil {
		return
	}
	trackPos := p.state.trackPosition()
	duration := int64(0)
	if p.primaryStream != nil {
		duration = int64(p.primaryStream.Media.Duration())
	}
	repeatTrack := p.state.player.Options != nil && p.state.player.Options.RepeatingTrack
	repeatContext := p.state.player.Options != nil && p.state.player.Options.RepeatingContext
	shuffleContext := p.state.player.Options != nil && p.state.player.Options.ShufflingContext
	p.runtime.Log.WithField("position_ms", trackPos).
		WithField("duration_ms", duration).
		WithField("repeat_track", repeatTrack).
		WithField("repeat_context", repeatContext).
		WithField("shuffle_context", shuffleContext).
		Debug("end-of-track transition state")
	if duration > 0 && trackPos+2000 < duration && !p.state.player.IsPaused {
		p.runtime.Log.WithField("position_ms", trackPos).
			WithField("duration_ms", duration).
			Warn("end-of-track event received before expected media end")
	}
}

func (p *AppPlayer) handlePlayerEvent(ctx context.Context, ev *player.Event) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch ev.Type {
	case player.EventTypePlay:
		p.state.player.IsPlaying = true
		p.state.setPaused(false)
		p.state.player.IsBuffering = false
		p.updateState(ctx)
		p.sess.Events().OnPlayerPlay(p.primaryStream, p.state.player.ContextUri, p.state.player.Options.ShufflingContext, p.state.player.PlayOrigin, p.state.tracks.CurrentTrack(), p.state.trackPosition())
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypePlaying, Data: ApiEventDataPlaying{ContextUri: p.state.player.ContextUri, Uri: p.state.player.Track.Uri, Resume: false, PlayOrigin: p.state.playOrigin()}})
		p.emitPlaybackState()
	case player.EventTypeResume:
		p.state.player.IsPlaying = true
		p.state.setPaused(false)
		p.state.player.IsBuffering = false
		p.updateState(ctx)
		p.sess.Events().OnPlayerResume(p.primaryStream, p.state.trackPosition())
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypePlaying, Data: ApiEventDataPlaying{ContextUri: p.state.player.ContextUri, Uri: p.state.player.Track.Uri, Resume: true, PlayOrigin: p.state.playOrigin()}})
		p.emitPlaybackState()
	case player.EventTypePause:
		p.state.player.IsPlaying = true
		p.state.setPaused(true)
		p.state.player.IsBuffering = false
		p.updateState(ctx)
		p.sess.Events().OnPlayerPause(p.primaryStream, p.state.player.ContextUri, p.state.player.Options.ShufflingContext, p.state.player.PlayOrigin, p.state.tracks.CurrentTrack(), p.state.trackPosition())
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypePaused, Data: ApiEventDataPaused{ContextUri: p.state.player.ContextUri, Uri: p.state.player.Track.Uri, PlayOrigin: p.state.playOrigin()}})
		p.emitPlaybackState()
	case player.EventTypeNotPlaying:
		p.sess.Events().OnPlayerEnd(p.primaryStream, p.state.trackPosition())
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypeNotPlaying, Data: ApiEventDataNotPlaying{ContextUri: p.state.player.ContextUri, Uri: p.state.player.Track.Uri, PlayOrigin: p.state.playOrigin()}})
		p.logEndOfTrackInvariant()
		dropTransition := p.state != nil &&
			p.state.player != nil &&
			p.state.player.Options != nil &&
			p.state.player.Options.RepeatingTrack
		transitionCtx, transitionCancel := context.WithTimeout(context.Background(), 30*time.Second)
		hasNextTrack, err := p.advanceNext(transitionCtx, false, dropTransition)
		transitionCancel()
		if err != nil {
			p.runtime.Log.WithError(err).Error("failed advancing to next track")
		}
		if !hasNextTrack {
			p.runtime.Emit(&ApiEvent{Type: ApiEventTypeStopped, Data: ApiEventDataStopped{PlayOrigin: p.state.playOrigin()}})
		}
		p.emitPlaybackState()
	case player.EventTypeStop:
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypeStopped, Data: ApiEventDataStopped{PlayOrigin: p.state.playOrigin()}})
		p.emitPlaybackState()
	default:
		p.runtime.Log.WithField("event_type", ev.Type).Error("received unhandled player event")
	}
}

type skipToFunc func(*connectpb.ContextTrack) bool

func (p *AppPlayer) loadContext(ctx context.Context, spotCtx *connectpb.Context, skipTo skipToFunc, paused, drop bool) error {
	ctxTracks, err := tracks.NewTrackListFromContext(ctx, p.runtime.Log, p.sess.Spclient(), spotCtx)
	if err != nil {
		return fmt.Errorf("failed creating track list: %w", err)
	}
	p.state.setPaused(paused)
	sessionId := make([]byte, 16)
	_, _ = rand.Read(sessionId)
	p.state.player.SessionId = base64.StdEncoding.EncodeToString(sessionId)
	p.state.player.ContextUri = spotCtx.Uri
	p.state.player.ContextUrl = spotCtx.Url
	p.state.player.Restrictions = spotCtx.Restrictions
	p.state.player.ContextRestrictions = spotCtx.Restrictions
	if spotCtx.Restrictions != nil {
		if len(spotCtx.Restrictions.DisallowTogglingShuffleReasons) > 0 {
			p.state.player.Options.ShufflingContext = false
		}
		if len(spotCtx.Restrictions.DisallowTogglingRepeatTrackReasons) > 0 {
			p.state.player.Options.RepeatingTrack = false
		}
		if len(spotCtx.Restrictions.DisallowTogglingRepeatContextReasons) > 0 {
			p.state.player.Options.RepeatingContext = false
		}
	}
	if p.state.player.ContextMetadata == nil {
		p.state.player.ContextMetadata = map[string]string{}
	}
	for k, v := range spotCtx.Metadata {
		p.state.player.ContextMetadata[k] = v
	}
	p.state.player.Timestamp = time.Now().UnixMilli()
	p.state.player.PositionAsOfTimestamp = 0
	if skipTo == nil {
		if err := ctxTracks.ToggleShuffle(ctx, p.state.player.Options.ShufflingContext); err != nil {
			return fmt.Errorf("failed shuffling context")
		}
		if err := ctxTracks.TrySeek(ctx, func(_ *connectpb.ContextTrack) bool { return true }); err != nil {
			return fmt.Errorf("failed seeking to track: %w", err)
		}
	} else {
		if err := ctxTracks.TrySeek(ctx, skipTo); err != nil {
			return fmt.Errorf("failed seeking to track: %w", err)
		}
		if err := ctxTracks.ToggleShuffle(ctx, p.state.player.Options.ShufflingContext); err != nil {
			return fmt.Errorf("failed shuffling context")
		}
	}
	p.state.tracks = ctxTracks
	p.invalidateQueueDerivation(true)
	p.resetQueueMetaForContext(strings.TrimSpace(spotCtx.Uri))
	p.resetPlaybackCaches(true)
	p.syncPlayerTrackState(ctx, ctxTracks, nil)
	p.preloadContextQueueMetadata(ctxTracks, spotCtx.Uri)
	if err := p.loadCurrentTrack(ctx, paused, drop); err != nil {
		return fmt.Errorf("failed loading current track (load context): %w", err)
	}
	return nil
}

func (p *AppPlayer) loadCurrentTrack(ctx context.Context, paused, drop bool) error {
	loadStarted := time.Now()
	if p.primaryStream != nil {
		p.sess.Events().OnPrimaryStreamUnload(p.primaryStream, p.currentPositionMs())
		p.primaryStream = nil
	}
	spotId, err := golibrespot.SpotifyIdFromUri(p.state.player.Track.Uri)
	if err != nil {
		return fmt.Errorf("failed parsing uri: %w", err)
	}
	if spotId.Type() != golibrespot.SpotifyIdTypeTrack && spotId.Type() != golibrespot.SpotifyIdTypeEpisode {
		return fmt.Errorf("unsupported spotify type: %s", spotId.Type())
	}
	trackPosition := p.state.trackPosition()
	p.state.updateTimestamp()
	if p.state.player.Duration > 0 && p.state.player.PositionAsOfTimestamp > p.state.player.Duration {
		p.state.player.PositionAsOfTimestamp = p.state.player.Duration
	}
	trackPosition = p.state.trackPosition()
	if p.state.player.Duration > 0 && trackPosition >= p.state.player.Duration {
		trackPosition = 0
		p.state.player.PositionAsOfTimestamp = 0
		p.state.player.Timestamp = time.Now().UnixMilli()
	}
	p.state.player.IsPlaying = true
	p.state.player.IsBuffering = true
	p.state.player.IsPaused = paused
	p.state.player.PlaybackSpeed = 0
	p.updateState(ctx)
	p.runtime.Emit(&ApiEvent{Type: ApiEventTypeWillPlay, Data: ApiEventDataWillPlay{ContextUri: p.state.player.ContextUri, Uri: spotId.Uri(), PlayOrigin: p.state.playOrigin()}})
	p.emitPlaybackState()
	var prefetched bool
	prefetchSource := "cold"
	if p.secondaryStream != nil && p.secondaryStream.Is(*spotId) {
		p.primaryStream = p.secondaryStream
		p.clearSecondaryStream()
		prefetched = true
		prefetchSource = "secondary"
	} else {
		if trackPosition == 0 {
			if cached := p.takeTransitionCachedStream(*spotId); cached != nil {
				p.primaryStream = cached
				prefetched = true
				prefetchSource = "cache"
			}
		}
		if p.primaryStream == nil {
			p.clearSecondaryStream()
			prefetched = false
			var err error
			var panicked interface{}
			func() {
				defer func() { panicked = recover() }()
				p.primaryStream, err = p.player.NewStream(ctx, p.runtime.Client, *spotId, p.runtime.Cfg.Bitrate, trackPosition)
			}()
			if panicked != nil {
				return fmt.Errorf("player unavailable (create stream): %v", panicked)
			}
			if err != nil {
				return fmt.Errorf("failed creating stream for %s: %w", spotId, err)
			}
		}
	}
	var setPrimaryErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				setPrimaryErr = fmt.Errorf("player unavailable: %v", r)
			}
		}()
		setPrimaryErr = p.player.SetPrimaryStream(p.primaryStream.Source, paused, drop)
	}()
	if setPrimaryErr != nil {
		return fmt.Errorf("failed setting stream for %s: %w", spotId, setPrimaryErr)
	}
	if err := p.player.SeekMs(trackPosition); err != nil {
		p.runtime.Log.WithError(err).WithField("position_ms", trackPosition).Warn("seek after load failed")
	}
	p.sess.Events().PostPrimaryStreamLoad(p.primaryStream, paused)
	p.runtime.Log.WithField("uri", spotId.Uri()).Infof("loaded %s %s (paused: %t, position: %dms, duration: %dms, prefetched: %t)", spotId.Type(), strconv.QuoteToGraphic(p.primaryStream.Media.Name()), paused, trackPosition, p.primaryStream.Media.Duration(), prefetched)
	p.runtime.Log.WithField("uri", spotId.Uri()).Debugf("track load latency=%s source=%s", time.Since(loadStarted), prefetchSource)
	p.state.updateTimestamp()
	p.state.player.PlaybackId = hex.EncodeToString(p.primaryStream.PlaybackId)
	p.state.player.Duration = int64(p.primaryStream.Media.Duration())
	if p.state.player.Duration > 0 && p.state.player.PositionAsOfTimestamp > p.state.player.Duration {
		p.state.player.PositionAsOfTimestamp = p.state.player.Duration
	}
	p.state.player.IsPlaying = true
	p.state.player.IsBuffering = false
	p.state.setPaused(paused)
	p.updateState(ctx)
	p.schedulePrefetchNext()
	p.runtime.Emit(&ApiEvent{Type: ApiEventTypeMetadata, Data: ApiEventDataMetadata(*p.newApiResponseStatusTrack(p.primaryStream.Media, trackPosition))})
	p.emitPlaybackState()
	return nil
}

func (p *AppPlayer) setOptions(ctx context.Context, repeatingContext *bool, repeatingTrack *bool, shufflingContext *bool) error {
	if p == nil || p.state == nil || p.state.player == nil || p.state.player.Options == nil {
		return nil
	}
	curr := playbackdomain.TraversalOptions{
		RepeatContext: p.state.player.Options.RepeatingContext,
		RepeatTrack:   p.state.player.Options.RepeatingTrack,
		Shuffle:       p.state.player.Options.ShufflingContext,
	}
	next := playbackdomain.ResolveOptions(curr, repeatingContext, repeatingTrack, shufflingContext)
	var requiresUpdate bool
	if next.RepeatContext != curr.RepeatContext {
		p.state.player.Options.RepeatingContext = next.RepeatContext
		p.invalidateQueueDerivation(false)
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypeRepeatContext, Data: ApiEventDataRepeatContext{Value: next.RepeatContext}})
		requiresUpdate = true
	}
	if next.RepeatTrack != curr.RepeatTrack {
		p.state.player.Options.RepeatingTrack = next.RepeatTrack
		p.bumpPrefetchGeneration()
		if next.RepeatTrack {
			p.clearSecondaryStream()
		}
		p.invalidateDerivedQueueCache()
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypeRepeatTrack, Data: ApiEventDataRepeatTrack{Value: next.RepeatTrack}})
		requiresUpdate = true
	}
	if p.state.tracks == nil && next.Shuffle != curr.Shuffle {
		p.runtime.Log.WithField("value", next.Shuffle).Warn("ignoring shuffle toggle without active context")
	}
	if p.state.tracks != nil && next.Shuffle != curr.Shuffle {
		if err := p.state.tracks.ToggleShuffle(ctx, next.Shuffle); err != nil {
			p.runtime.Log.WithError(err).Errorf("failed toggling shuffle context (value: %t)", next.Shuffle)
			return err
		}
		p.state.player.Options.ShufflingContext = next.Shuffle
		p.invalidateQueueDerivation(true)
		if next.Shuffle {
			p.seedPlayedTrackSetFromPlaybackWindow()
		}
		p.resetPlaybackCaches(true)
		p.syncPlayerTrackState(ctx, p.state.tracks, nil)
		if next.Shuffle {
			p.scheduleShuffleCacheRefresh()
		}
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypeShuffleContext, Data: ApiEventDataShuffleContext{Value: next.Shuffle}})
		requiresUpdate = true
	}
	if requiresUpdate {
		p.logRepeatShuffleInvariant("set_options")
		p.updateState(ctx)
		p.emitPlaybackState()
	}
	return nil
}

func (p *AppPlayer) addToQueue(ctx context.Context, track *connectpb.ContextTrack) {
	if p.state.tracks == nil {
		p.runtime.Log.Warnf("cannot add to queue without a context")
		return
	}
	if track.Uid == "" {
		p.state.queueID++
		track.Uid = fmt.Sprintf("q%d", p.state.queueID)
	}
	p.state.tracks.AddToQueue(track)
	p.state.player.PrevTracks = p.state.tracks.PrevTracks()
	p.state.player.NextTracks = p.state.tracks.NextTracks(ctx, nil)
	p.updateState(ctx)
	p.schedulePrefetchNext()
	p.emitPlaybackState()
}

func (p *AppPlayer) setQueue(ctx context.Context, prev []*connectpb.ContextTrack, next []*connectpb.ContextTrack) {
	if p.state.tracks == nil {
		p.runtime.Log.Warnf("cannot set queue without a context")
		return
	}
	p.state.tracks.SetQueue(prev, next)
	p.state.player.PrevTracks = p.state.tracks.PrevTracks()
	p.state.player.NextTracks = p.state.tracks.NextTracks(ctx, next)
	p.updateState(ctx)
	p.schedulePrefetchNext()
	p.emitPlaybackState()
}

func (p *AppPlayer) play(ctx context.Context) error {
	if p.primaryStream == nil {
		return fmt.Errorf("no primary stream")
	}
	seekPos := p.state.trackPosition()
	seekPos = max(0, min(seekPos, int64(p.primaryStream.Media.Duration())))
	if err := p.player.SeekMs(seekPos); err != nil {
		return fmt.Errorf("failed seeking before play: %w", err)
	}
	if err := p.player.Play(); err != nil {
		return fmt.Errorf("failed starting playback: %w", err)
	}
	streamPos := p.currentPositionMs()
	p.runtime.Log.Debugf("resume track at %dms", streamPos)
	p.state.player.Timestamp = time.Now().UnixMilli()
	p.state.player.PositionAsOfTimestamp = streamPos
	p.state.setPaused(false)
	p.updateState(ctx)
	p.schedulePrefetchNext()
	p.emitPlaybackState()
	return nil
}

func (p *AppPlayer) pause(ctx context.Context) error {
	if p.primaryStream == nil {
		return fmt.Errorf("no primary stream")
	}
	streamPos := p.currentPositionMs()
	p.runtime.Log.Debugf("pause track at %dms", streamPos)
	if err := p.player.Pause(); err != nil {
		return fmt.Errorf("failed pausing playback: %w", err)
	}
	p.state.player.Timestamp = time.Now().UnixMilli()
	p.state.player.PositionAsOfTimestamp = streamPos
	p.state.setPaused(true)
	p.updateState(ctx)
	p.schedulePrefetchNext()
	p.emitPlaybackState()
	return nil
}

func (p *AppPlayer) seek(ctx context.Context, position int64) error {
	if p.primaryStream == nil {
		return fmt.Errorf("no primary stream")
	}
	requestedPosition := position
	oldPosition := p.currentPositionMs()
	duration := int64(p.primaryStream.Media.Duration())
	position = max(0, min(position, duration))
	if position != requestedPosition {
		p.runtime.Log.WithField("requested_ms", requestedPosition).
			WithField("bounded_ms", position).
			WithField("duration_ms", duration).
			Warn("seek target clamped to valid range")
	}
	if position == duration {
		p.runtime.Log.WithField("repeat_track", p.state.player.Options != nil && p.state.player.Options.RepeatingTrack).
			WithField("repeat_context", p.state.player.Options != nil && p.state.player.Options.RepeatingContext).
			WithField("shuffle_context", p.state.player.Options != nil && p.state.player.Options.ShufflingContext).
			Debug("seek landed at track end")
	}
	p.runtime.Log.Debugf("seek track to %dms", position)
	if err := p.player.SeekMs(position); err != nil {
		return err
	}
	p.state.player.Timestamp = time.Now().UnixMilli()
	p.state.player.PositionAsOfTimestamp = position
	p.updateState(ctx)
	p.schedulePrefetchNext()
	p.sess.Events().OnPlayerSeek(p.primaryStream, oldPosition, position)
	p.runtime.Emit(&ApiEvent{Type: ApiEventTypeSeek, Data: ApiEventDataSeek{ContextUri: p.state.player.ContextUri, Uri: p.state.player.Track.Uri, Position: int(position), Duration: int(p.primaryStream.Media.Duration()), PlayOrigin: p.state.playOrigin()}})
	p.emitPlaybackState()
	return nil
}

func (p *AppPlayer) skipPrev(ctx context.Context, allowSeeking bool) error {
	started := time.Now()
	if allowSeeking && p.currentPositionMs() > 3000 {
		return p.seek(ctx, 0)
	}
	p.sess.Events().OnPlayerSkipBackward(p.primaryStream, p.currentPositionMs())
	if p.state.tracks != nil {
		p.runtime.Log.Debug("skip previous track")
		p.state.tracks.GoPrev()
		p.syncPlayerTrackState(ctx, p.state.tracks, nil)
	}
	if err := p.loadCurrentTrackFromTransition(ctx, p.state.player.IsPaused, true, "skip prev"); err != nil {
		return err
	}
	p.runtime.Log.Debugf("skip prev transition completed in %s", time.Since(started))
	p.emitPlaybackState()
	return nil
}

func (p *AppPlayer) skipNext(ctx context.Context, track *connectpb.ContextTrack) error {
	started := time.Now()
	p.sess.Events().OnPlayerSkipForward(p.primaryStream, p.currentPositionMs(), track != nil)
	if track != nil {
		contextSpotType := golibrespot.InferSpotifyIdTypeFromContextUri(p.state.player.ContextUri)
		if err := p.state.tracks.TrySeek(ctx, tracks.ContextTrackComparator(contextSpotType, track)); err != nil {
			return err
		}
		p.syncPlayerTrackState(ctx, p.state.tracks, nil)
		if err := p.loadCurrentTrackFromTransition(ctx, p.state.player.IsPaused, true, "skip next"); err != nil {
			return err
		}
		p.runtime.Log.Debugf("skip next transition completed in %s", time.Since(started))
		p.emitPlaybackState()
		return nil
	}
	hasNextTrack, err := p.advanceNext(ctx, true, true)
	if err != nil {
		return fmt.Errorf("failed skipping to next track: %w", err)
	}
	if !hasNextTrack {
		p.runtime.Emit(&ApiEvent{Type: ApiEventTypeStopped, Data: ApiEventDataStopped{PlayOrigin: p.state.playOrigin()}})
	}
	p.runtime.Log.Debugf("skip next transition completed in %s", time.Since(started))
	p.emitPlaybackState()
	return nil
}

type advanceNextSelection struct {
	hasNextTrack   bool
	trackChanged   bool
	wrappedContext bool
}

func (p *AppPlayer) selectAdvanceNextTarget(ctx context.Context, forceNext bool) advanceNextSelection {
	var selection advanceNextSelection
	if p.state == nil || p.state.player == nil || p.state.tracks == nil {
		return selection
	}
	repeatingTrack := p.state.player.Options != nil && p.state.player.Options.RepeatingTrack
	if !forceNext && repeatingTrack {
		selection.hasNextTrack = true
		return selection
	}
	if p.state.player.Track != nil && p.state.player.Track.Uri != "" {
		p.markPlayedTrack(p.state.player.Track.Uri)
	}
	selection.hasNextTrack = p.state.tracks.GoNext(ctx)
	selection.trackChanged = true
	if selection.hasNextTrack {
		return selection
	}
	selection.wrappedContext = p.state.tracks.GoStart(ctx)
	selection.hasNextTrack = selection.wrappedContext
	repeatingContext := p.state.player.Options != nil && p.state.player.Options.RepeatingContext
	if !repeatingContext {
		selection.hasNextTrack = false
		selection.wrappedContext = false
	}
	return selection
}

func (p *AppPlayer) applyAdvanceNextSelection(ctx context.Context, selection advanceNextSelection, forceNext bool) {
	if p.state == nil || p.state.player == nil {
		return
	}
	if selection.trackChanged && p.state.tracks != nil {
		p.syncPlayerTrackState(ctx, p.state.tracks, nil)
	}
	if !forceNext && p.state.player.Options != nil && p.state.player.Options.RepeatingTrack && !selection.trackChanged && selection.hasNextTrack {
		p.clearSecondaryStream()
	}
	p.state.player.IsPaused = !selection.hasNextTrack
	if selection.wrappedContext && p.state.player.Options != nil && p.state.player.Options.RepeatingContext {
		p.invalidateQueueDerivation(true)
	}
}

func (p *AppPlayer) currentTrackID() string {
	if p.state == nil || p.state.player == nil || p.state.player.Track == nil {
		return ""
	}
	return normalizeSpotifyID(p.state.player.Track.Uri)
}

func (p *AppPlayer) logAdvanceInvariants(forceNext bool, selection advanceNextSelection, beforeTrackID string) {
	if p.state == nil || p.state.player == nil {
		return
	}
	afterTrackID := p.currentTrackID()
	repeatTrack := p.state.player.Options != nil && p.state.player.Options.RepeatingTrack
	repeatContext := p.state.player.Options != nil && p.state.player.Options.RepeatingContext
	shuffleContext := p.state.player.Options != nil && p.state.player.Options.ShufflingContext
	if !forceNext && repeatTrack && beforeTrackID != "" && afterTrackID != "" && beforeTrackID != afterTrackID {
		p.runtime.Log.WithField("before", beforeTrackID).WithField("after", afterTrackID).Warn("repeat-track invariant violated: current track changed on auto-advance")
	}
	if selection.wrappedContext && !repeatContext {
		p.runtime.Log.Warn("repeat-context invariant violated: wrapped context while repeat context is disabled")
	}
	if selection.hasNextTrack && afterTrackID == "" {
		p.runtime.Log.Warn("transition invariant violated: next track was selected but current track is empty")
	}
	if !selection.hasNextTrack && !forceNext && repeatTrack {
		p.runtime.Log.Warn("repeat-track invariant violated: no next track selected during auto-advance")
	}
	if selection.wrappedContext {
		p.runtime.Log.WithField("shuffle_context", shuffleContext).Debug("repeat context wrapped to start")
	}
}

func (p *AppPlayer) advanceNext(ctx context.Context, forceNext, drop bool) (bool, error) {
	started := time.Now()
	beforeTrackID := p.currentTrackID()
	selection := p.selectAdvanceNextTarget(ctx, forceNext)
	p.applyAdvanceNextSelection(ctx, selection, forceNext)
	uri := ""
	if p.state != nil && p.state.player != nil && p.state.player.Track != nil {
		uri = p.state.player.Track.Uri
	}
	hasNextTrack := selection.hasNextTrack
	if !hasNextTrack && !p.runtime.Cfg.DisableAutoplay && !strings.HasPrefix(p.state.player.ContextUri, "spotify:station:") {
		p.state.player.Suppressions = &connectpb.Suppressions{}
		var prevTrackUris []string
		if p.state.tracks != nil {
			for _, track := range p.state.tracks.AllTracks(ctx) {
				prevTrackUris = append(prevTrackUris, track.Uri)
			}
		}
		if len(prevTrackUris) == 0 {
			p.runtime.Log.Warnf("cannot resolve autoplay station because there are no previous tracks in context %s", p.state.player.ContextUri)
			return false, nil
		}
		p.runtime.Log.Debugf("resolving autoplay station for %d tracks", len(prevTrackUris))
		spotCtx, err := p.sess.Spclient().ContextResolveAutoplay(ctx, &playerpb.AutoplayContextRequest{
			ContextUri:     proto.String(p.state.player.ContextUri),
			RecentTrackUri: prevTrackUris,
		})
		if err != nil {
			p.runtime.Log.WithError(err).Warnf("failed resolving station for %s", p.state.player.ContextUri)
			return false, nil
		}
		p.runtime.Log.Debugf("resolved autoplay station: %s", spotCtx.Uri)
		if err := p.loadContext(ctx, spotCtx, func(_ *connectpb.ContextTrack) bool { return true }, false, drop); err != nil {
			p.runtime.Log.WithError(err).Warnf("failed loading station for %s", p.state.player.ContextUri)
			return false, nil
		}
		return true, nil
	}
	if !hasNextTrack {
		p.state.player.IsPlaying = false
		p.state.player.IsPaused = false
		p.state.player.IsBuffering = false
	}
	p.logAdvanceInvariants(forceNext, selection, beforeTrackID)
	if err := p.loadCurrentTrackFromTransition(ctx, !hasNextTrack, drop, "advance next"); errors.Is(err, golibrespot.ErrMediaRestricted) || errors.Is(err, golibrespot.ErrNoSupportedFormats) {
		p.runtime.Log.WithError(err).Infof("skipping unplayable media: %s", uri)
		if forceNext {
			return false, err
		}
		return p.advanceNext(ctx, true, drop)
	} else if err != nil {
		return false, fmt.Errorf("failed loading current track (advance to %s): %w", uri, err)
	}
	if selection.wrappedContext {
		p.runtime.Log.Debug("repeat context wrapped to start; rebuilt played-track cycle for queue visibility")
	}
	p.runtime.Log.Debugf("advance next transition completed in %s (force=%t repeatTrack=%t)", time.Since(started), forceNext, p.state.player.Options.RepeatingTrack)
	return hasNextTrack, nil
}

func (p *AppPlayer) apiVolume() uint32 {
	return uint32(math.Round(float64(p.state.device.Volume*p.runtime.Cfg.VolumeSteps) / player.MaxStateVolume))
}

func (p *AppPlayer) updateVolume(newVal uint32) {
	if newVal > player.MaxStateVolume {
		newVal = player.MaxStateVolume
	} else if newVal < 0 {
		newVal = 0
	}
	p.runtime.Log.Debugf("update volume requested to %d/%d", newVal, player.MaxStateVolume)
	p.player.SetVolume(newVal)
	p.runtime.State.LastVolume = &newVal
	if err := p.runtime.State.Write(); err != nil {
		p.runtime.Log.WithError(err).Error("failed writing state after volume change")
	}
	select {
	case <-p.volumeUpdate:
	default:
	}
	p.volumeUpdate <- float32(newVal) / player.MaxStateVolume
}

func (p *AppPlayer) volumeUpdated(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.putConnectState(ctx, connectpb.PutStateReason_VOLUME_CHANGED); err != nil {
		p.runtime.Log.WithError(err).Error("failed put state after volume change")
	}
	p.runtime.Emit(&ApiEvent{Type: ApiEventTypeVolume, Data: ApiEventDataVolume{Value: p.apiVolume(), Max: p.runtime.Cfg.VolumeSteps}})
	p.emitPlaybackState()
}

func (p *AppPlayer) stopPlayback(ctx context.Context) error {
	p.player.Stop()
	p.primaryStream = nil
	p.resetPlaybackCaches(true)
	p.state.reset()
	if err := p.putConnectState(ctx, connectpb.PutStateReason_BECAME_INACTIVE); err != nil {
		return fmt.Errorf("failed inactive state put: %w", err)
	}
	p.schedulePrefetchNext()
	if p.runtime.Cfg.ZeroconfEnabled {
		p.logout <- p
	}
	p.runtime.Emit(&ApiEvent{Type: ApiEventTypeInactive})
	p.emitPlaybackState()
	return nil
}
