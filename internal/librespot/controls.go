package librespot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	playerpb "github.com/elxgy/go-librespot/proto/spotify/player"
	"github.com/elxgy/go-librespot/tracks"

	"orpheus/internal/playbackdomain"
)

// prefetchFanOutMax bounds how many streams one prefetch pass may warm.
// The transition cache holds 16, but warming all of them hammers spclient
// with metadata/storage requests for tracks the user will likely never
// reach before the next context change; the next few tracks cover skips.
const prefetchFanOutMax = 3

func (p *AppPlayer) prefetchCandidateIDs() []golibrespot.SpotifyId {
	if p.state == nil || p.state.tracks == nil || p.state.player == nil {
		return nil
	}
	candidates := make([]golibrespot.SpotifyId, 0, prefetchFanOutMax)
	seen := make(map[string]struct{}, prefetchFanOutMax)
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
	if next := p.state.tracks.PeekNextLoaded(); next != nil {
		appendCandidate(next.Uri)
	}
	for i := 0; i < len(p.state.player.NextTracks) && len(candidates) < prefetchFanOutMax; i++ {
		appendCandidate(p.state.player.NextTracks[i].Uri)
	}
	if n := len(p.state.player.PrevTracks); n > 0 && len(candidates) < prefetchFanOutMax {
		appendCandidate(p.state.player.PrevTracks[n-1].Uri)
	}
	return candidates
}

func (p *AppPlayer) clearSecondaryStream() {
	if p.secondaryStream != nil {
		if p.player != nil {
			p.player.SetSecondaryStream(nil)
		}
		closeStreamAsync(p.secondaryStream)
	}
	p.secondaryStream = nil
}

func closeStream(s *player.Stream) {
	if s == nil {
		return
	}
	_ = s.Close()
}

func closeStreamAsync(s *player.Stream) {
	if s == nil {
		return
	}
	go closeStream(s)
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
	p.setPlayerPositionAtNow(0)
}

func (p *AppPlayer) setPlayerPositionAtNow(position int64) {
	if p == nil || p.state == nil || p.state.player == nil {
		return
	}
	p.state.player.Timestamp = time.Now().UnixMilli()
	p.state.player.PositionAsOfTimestamp = position
}

func (p *AppPlayer) setPlayerTransportState(playing, buffering, paused bool) {
	if p == nil || p.state == nil || p.state.player == nil {
		return
	}
	p.state.player.IsPlaying = playing
	p.state.player.IsBuffering = buffering
	golibrespot.SetPaused(p.state.player, paused)
}

func (p *AppPlayer) currentPositionMs() int64 {
	if p.state == nil || p.state.player == nil {
		return 0
	}
	pos := golibrespot.TrackPosition(p.state.player, 0)
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
	endTransitionGuardInterval = 500 * time.Millisecond
	endTransitionGuardLeewayMs = int64(50)
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
	p.prefetchNext(ctx)
}

func (p *AppPlayer) prefetchNext(ctx context.Context) {
	candidates := p.prefetchCandidateIDs()
	if len(candidates) == 0 {
		return
	}
	nextURI := candidates[0].Uri()
	gen := p.prefetchGen.Load()
	repeatTrack := p.state != nil && p.state.player != nil && p.state.player.Options != nil && p.state.player.Options.RepeatingTrack
	for i := range candidates {
		id := candidates[i]
		// In repeat-track mode the next track is the current one, so the
		// primary stream must stay a prefetch target; its result is routed
		// to the transition cache instead of the secondary slot.
		if p.primaryStream != nil && p.primaryStream.Is(id) && !repeatTrack {
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
		if !p.markPrefetchPending(id, gen) {
			continue
		}
		select {
		case p.prefetchJobs <- prefetchJob{gen: gen, nextURI: nextURI, target: id}:
		default:
			p.clearPrefetchPending(id, gen)
			return
		}
	}
}

// scheduleQueueTopUp arms the deferred queue-extension tick. The Run
// goroutine emits queue state from already-loaded pages only (B3); this
// schedules the single bounded extending fetch that keeps the full queue
// visible in the TUI and in connect state, without stalling emits.
func (p *AppPlayer) scheduleQueueTopUp() {
	if p == nil || p.queueTopUpInFlight {
		return
	}
	// A top-up's own emit would otherwise re-arm forever while the loaded
	// window stays at the cap: back-to-back bounded fetches that stall Run
	// and starve skip commands on slow networks (observed). One top-up per
	// triggering event; fresh events re-arm.
	if p.topUpSuppressArm {
		p.topUpSuppressArm = false
		return
	}
	p.queueTopUpInFlight = true
	stopAndResetTimer(p.queueTopUpTimer, queueTopUpDelay)
}

// topUpQueue performs the deferred extending fetch on the Run goroutine.
// The fetch mutates the tracks list (pagedList is single-goroutine by
// design), so it must run here; the timer keeps it out of the dealer-reply
// critical path and the bounded timeout caps the block.
func (p *AppPlayer) topUpQueue(ctx context.Context) {
	p.queueTopUpInFlight = false
	if p.state == nil || p.state.tracks == nil || p.primaryStream == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, queueTopUpTimeout)
	defer cancel()
	full := p.state.tracks.UpcomingTracks(ctx, queueOverrideMaxTracks)
	if ctx.Err() != nil || len(full) == 0 {
		return
	}
	p.syncPlayerTrackState(p.state.tracks, nil)
	p.topUpSuppressArm = true
	p.updateState()
	p.emitPlaybackState()
}

func (p *AppPlayer) schedulePrefetchNext() {
	if p.state.player.IsPaused || p.primaryStream == nil {
		stopTimer(p.prefetchTimer)
		return
	}
	if p.secondaryStream == nil {
		stopAndResetTimer(p.prefetchTimer, 0)
		p.runtime.Log.Tracef("prefetch immediately (no secondary stream)")
		return
	}
	untilTrackEnd := time.Duration(p.primaryStream.Media.Duration()-int32(p.currentPositionMs())) * time.Millisecond
	untilTrackEnd -= prefetchLeadTime
	if untilTrackEnd < 0 {
		untilTrackEnd = 0
	}
	if untilTrackEnd < prefetchImmediateThreshold {
		stopAndResetTimer(p.prefetchTimer, 0)
		p.runtime.Log.Tracef("prefetch as soon as possible")
	} else {
		stopAndResetTimer(p.prefetchTimer, untilTrackEnd)
		p.runtime.Log.Tracef("scheduling prefetch in %.0fs", untilTrackEnd.Seconds())
	}
}

func (p *AppPlayer) runPrefetchWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.prefetchJobs:
			jobCtx, cancel := context.WithTimeout(p.ownerContext(), prefetchJobTimeout)
			stream, err := p.player.NewStream(jobCtx, p.runtime.Client, job.target, p.runtime.Cfg.Bitrate, 0)
			cancel()
			if ctx.Err() != nil {
				closeStreamAsync(stream)
				return
			}
			select {
			case <-ctx.Done():
				closeStreamAsync(stream)
				return
			case p.prefetchDone <- prefetchResult{gen: job.gen, nextURI: job.nextURI, target: job.target, stream: stream, err: err}:
			}
		}
	}
}

func (p *AppPlayer) handlePrefetchResult(res prefetchResult) {
	p.clearPrefetchPending(res.target, res.gen)
	if res.gen != p.prefetchGen.Load() {
		p.runtime.Log.WithField("uri", res.target.Uri()).Tracef("dropping stale prefetch result (res_gen=%d current_gen=%d)", res.gen, p.prefetchGen.Load())
		closeStreamAsync(res.stream)
		return
	}
	if res.err != nil {
		p.runtime.Log.WithError(res.err).WithField("uri", res.target.String()).Warnf("failed prefetching %s stream", res.target.Type())
		return
	}
	if p.primaryStream != nil && p.primaryStream.Is(res.target) {
		closeStreamAsync(res.stream)
		return
	}
	if p.secondaryStream != nil && p.secondaryStream.Is(res.target) {
		closeStreamAsync(res.stream)
		return
	}
	if p.hasTransitionCachedStream(res.target) {
		closeStreamAsync(res.stream)
		return
	}
	repeatTrack := p.state != nil &&
		p.state.player != nil &&
		p.state.player.Options != nil &&
		p.state.player.Options.RepeatingTrack
	if repeatTrack {
		// The advance path clears the secondary slot but keeps the
		// transition cache, so a repeat-one loop stays gapless.
		p.runtime.Log.WithField("uri", res.target.Uri()).Trace("repeat-track mode: keeping prefetched stream in transition cache")
		p.putTransitionCachedStream(res.target, res.stream)
		return
	}
	if res.nextURI == res.target.Uri() && p.secondaryStream == nil {
		p.secondaryStream = res.stream
		p.player.SetSecondaryStream(res.stream.Source)
	} else {
		p.putTransitionCachedStream(res.target, res.stream)
	}
}

func (p *AppPlayer) syncPlayerTrackState(trackList *tracks.List, nextHint []*connectpb.ContextTrack) {
	if p.state == nil || p.state.player == nil || trackList == nil {
		return
	}
	p.state.player.Track = trackList.CurrentTrack()
	p.state.player.PrevTracks = trackList.PrevTracks()
	p.state.player.NextTracks = trackList.NextTracksLoaded(nextHint)
	p.state.player.Index = trackList.Index()
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
	trackPos := golibrespot.TrackPosition(p.state.player, 0)
	duration := int64(0)
	if p.primaryStream != nil {
		duration = int64(p.primaryStream.Media.Duration())
	}
	if duration > 0 && trackPos+2000 < duration && !p.state.player.IsPaused {
		p.runtime.Log.WithField("position_ms", trackPos).
			WithField("duration_ms", duration).
			Warn("end-of-track event received before expected media end")
	}
}

func (p *AppPlayer) runAdvanceNextTransition(source string, forceNext, dropTransition bool) {
	if p.advanceInFlight.Load() {
		p.runtime.Log.WithField("source", source).Debug("ignoring transition while another transition is in flight")
		return
	}
	p.advanceInFlight.Store(true)
	defer p.advanceInFlight.Store(false)
	transitionCtx, transitionCancel := context.WithTimeout(p.ownerContext(), trackTransitionTimeout)
	hasNextTrack, err := p.advanceNext(transitionCtx, forceNext, dropTransition)
	transitionCancel()
	if err != nil {
		if source == "end_guard" || source == "player_not_playing" {
			// Bounded retry: without a cap the 500ms end-of-track ticker
			// would retry a permanently failing advance forever, each
			// attempt able to block Run for a full transition timeout.
			p.endGuardFailures++
			if p.endGuardFailures >= endGuardMaxFailures {
				p.runtime.Log.WithError(err).WithField("source", source).
					Errorf("giving up end-of-track advance after %d failures", p.endGuardFailures)
				p.runtime.EmitPlaybackState(&PlaybackStateUpdate{Error: "playback stuck: failed to advance to the next track"})
				return
			}
		}
		p.runtime.Log.WithError(err).WithField("source", source).Error("failed advancing to next track")
		p.emitPlaybackState()
		return
	}
	if source == "end_guard" || source == "player_not_playing" {
		p.endGuardFailures = 0
	}
	if !hasNextTrack {
		p.emitPlaybackState()
	}
}

func (p *AppPlayer) maybeAdvanceOnTrackEndGuard() {
	if p == nil || p.state == nil || p.state.player == nil || p.primaryStream == nil {
		return
	}
	if p.state.player.IsPaused || !p.state.player.IsPlaying {
		return
	}
	duration := int64(p.primaryStream.Media.Duration())
	if duration <= 0 {
		return
	}
	position := p.currentPositionMs()
	if position+endTransitionGuardLeewayMs < duration {
		return
	}
	dropTransition := p.state.player.Options != nil && p.state.player.Options.RepeatingTrack
	p.runAdvanceNextTransition("end_guard", false, dropTransition)
}

func (p *AppPlayer) handlePlayerEvent(ev *player.Event) {
	if p.state.player.Options == nil {
		p.state.player.Options = &connectpb.ContextPlayerOptions{}
	}
	switch ev.Type {
	case player.EventTypePlay:
		p.state.player.IsPlaying = true
		golibrespot.SetPaused(p.state.player, false)
		p.state.player.IsBuffering = false
		p.updateState()
		var currentTrack *connectpb.ProvidedTrack
		if p.state.tracks != nil {
			currentTrack = p.state.tracks.CurrentTrack()
		}
		p.sess.Events().OnPlayerPlay(p.primaryStream, p.state.player.ContextUri, p.state.player.Options.ShufflingContext, p.state.player.PlayOrigin, currentTrack, golibrespot.TrackPosition(p.state.player, 0))
		p.emitPlaybackStateLight()
	case player.EventTypeResume:
		p.state.player.IsPlaying = true
		golibrespot.SetPaused(p.state.player, false)
		p.state.player.IsBuffering = false
		p.updateState()
		p.sess.Events().OnPlayerResume(p.primaryStream, golibrespot.TrackPosition(p.state.player, 0))
		p.emitPlaybackStateLight()
	case player.EventTypePause:
		p.state.player.IsPlaying = true
		golibrespot.SetPaused(p.state.player, true)
		p.state.player.IsBuffering = false
		p.updateState()
		var currentTrack *connectpb.ProvidedTrack
		if p.state.tracks != nil {
			currentTrack = p.state.tracks.CurrentTrack()
		}
		p.sess.Events().OnPlayerPause(p.primaryStream, p.state.player.ContextUri, p.state.player.Options.ShufflingContext, p.state.player.PlayOrigin, currentTrack, golibrespot.TrackPosition(p.state.player, 0))
		p.emitPlaybackStateLight()
	case player.EventTypeNotPlaying:
		if p.primaryStream != nil {
			duration := int64(p.primaryStream.Media.Duration())
			if duration > 0 && p.currentPositionMs()+endTransitionGuardLeewayMs < duration {
				break
			}
		}
		p.sess.Events().OnPlayerEnd(p.primaryStream, golibrespot.TrackPosition(p.state.player, 0))
		p.logEndOfTrackInvariant()
		dropTransition := p.state != nil &&
			p.state.player != nil &&
			p.state.player.Options != nil &&
			p.state.player.Options.RepeatingTrack
		p.runAdvanceNextTransition("player_not_playing", false, dropTransition)
	case player.EventTypeStop:
		p.emitPlaybackStateLight()
	default:
		p.runtime.Log.WithField("event_type", ev.Type).Error("received unhandled player event")
	}
}

type skipToFunc func(*connectpb.ContextTrack) bool

func (p *AppPlayer) loadContext(ctx context.Context, spotCtx *connectpb.Context, skipTo skipToFunc, paused, drop bool) error {
	ctxTracks, err := tracks.NewTrackListFromContext(ctx, p.runtime.Log, p.sess.Spclient(), spotCtx, 0)
	if err != nil {
		return fmt.Errorf("failed creating track list: %w", err)
	}
	golibrespot.SetPaused(p.state.player, paused)
	sessionId := make([]byte, 16)
	if _, err := rand.Read(sessionId); err != nil {
		p.runtime.Log.WithError(err).Warn("failed generating session ID")
	}
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
	maps.Copy(p.state.player.ContextMetadata, spotCtx.Metadata)
	p.state.player.Timestamp = time.Now().UnixMilli()
	p.state.player.PositionAsOfTimestamp = 0
	if skipTo == nil {
		if err := ctxTracks.TrySeek(ctx, func(_ *connectpb.ContextTrack) bool { return true }); err != nil {
			return fmt.Errorf("failed seeking to track: %w", err)
		}
		if err := ctxTracks.ToggleShuffle(ctx, p.state.player.Options.ShufflingContext); err != nil {
			return fmt.Errorf("failed shuffling context")
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
	p.resetQueueMetaForContext()
	p.resetPlaybackCaches(true)
	p.syncPlayerTrackState(ctxTracks, nil)
	allTracks := ctxTracks.AllTracks(ctx)
	p.scheduleQueueTopUp()
	go func() {
		metaCtx, metaCancel := context.WithTimeout(p.ownerContext(), metadataBatchTimeout)
		defer metaCancel()
		p.resolveContextQueueMetadata(metaCtx, allTracks)
	}()
	if err := p.loadCurrentTrack(ctx, paused, drop); err != nil {
		if errors.Is(err, golibrespot.ErrMediaRestricted) || errors.Is(err, golibrespot.ErrNoSupportedFormats) {
			p.runtime.Log.WithError(err).Info("first track unplayable, skipping to next")
			if _, advErr := p.advanceNext(ctx, true, drop); advErr != nil {
				return fmt.Errorf("failed loading current track (load context): %w", advErr)
			}
			return nil
		}
		return fmt.Errorf("failed loading current track (load context): %w", err)
	}
	return nil
}

func (p *AppPlayer) loadCurrentTrack(ctx context.Context, paused, drop bool) error {
	// Defer closing the old primary until SetPrimaryStream replaces it in the
	// SwitchingAudioSource — closing first leaves a window where the output
	// goroutine reads from a closed cgo decoder.
	var oldStream *player.Stream
	var setPrimaryDone bool
	defer func() {
		if setPrimaryDone {
			closeStreamAsync(oldStream)
		} else if oldStream != nil && p.primaryStream == nil {
			p.primaryStream = oldStream
		}
	}()

	if p.primaryStream != nil {
		p.sess.Events().OnPrimaryStreamUnload(p.primaryStream, p.currentPositionMs())
		oldStream = p.primaryStream
		p.primaryStream = nil
	}
	spotId, err := golibrespot.SpotifyIdFromUri(p.state.player.Track.Uri)
	if err != nil {
		return fmt.Errorf("failed parsing uri: %w", err)
	}
	if spotId.Type() != golibrespot.SpotifyIdTypeTrack && spotId.Type() != golibrespot.SpotifyIdTypeEpisode {
		return fmt.Errorf("unsupported spotify type: %s", spotId.Type())
	}
	golibrespot.UpdateTimestamp(p.state.player, 0)
	if p.state.player.PositionAsOfTimestamp < 0 {
		p.state.player.PositionAsOfTimestamp = 0
	}
	trackPosition := golibrespot.TrackPosition(p.state.player, 0)
	p.setPlayerTransportState(true, true, paused)
	p.state.player.PlaybackSpeed = 0
	var prefetched bool
	if p.secondaryStream != nil && p.secondaryStream.Is(*spotId) {
		p.primaryStream = p.secondaryStream
		p.secondaryStream = nil
		p.player.SetSecondaryStream(nil)
		prefetched = true
	} else {
		if trackPosition == 0 {
			if cached := p.takeTransitionCachedStream(*spotId); cached != nil {
				p.primaryStream = cached
				prefetched = true
			}
		}
		if p.primaryStream == nil {
			p.clearSecondaryStream()
			prefetched = false
			var err error
			p.primaryStream, err = p.player.NewStream(ctx, p.runtime.Client, *spotId, p.runtime.Cfg.Bitrate, trackPosition)
			if err != nil {
				return fmt.Errorf("failed creating stream for %s: %w", spotId, err)
			}
		}
	}
	if err := p.player.SetPrimaryStream(p.primaryStream.Source, paused, drop); err != nil {
		failed := p.primaryStream
		p.primaryStream = oldStream
		oldStream = nil
		if failed != nil && failed != p.primaryStream {
			closeStreamAsync(failed)
		}
		return fmt.Errorf("failed setting stream for %s: %w", spotId, err)
	}
	setPrimaryDone = true
	if err := p.player.SeekMs(trackPosition); err != nil {
		p.runtime.Log.WithError(err).WithField("position_ms", trackPosition).Warn("seek after load failed")
	}
	p.sess.Events().PostPrimaryStreamLoad(p.primaryStream, paused)
	p.runtime.Log.WithField("uri", spotId.Uri()).Infof("loaded %s %s (paused: %t, position: %dms, duration: %dms, prefetched: %t)", spotId.Type(), strconv.QuoteToGraphic(p.primaryStream.Media.Name()), paused, trackPosition, p.primaryStream.Media.Duration(), prefetched)
	golibrespot.UpdateTimestamp(p.state.player, 0)
	p.state.player.PlaybackId = hex.EncodeToString(p.primaryStream.PlaybackId)
	p.state.player.Duration = int64(p.primaryStream.Media.Duration())
	if p.state.player.Duration > 0 && p.state.player.PositionAsOfTimestamp > p.state.player.Duration {
		p.state.player.PositionAsOfTimestamp = p.state.player.Duration
	}
	p.setPlayerTransportState(true, false, paused)
	p.updateState()
	p.schedulePrefetchNext()
	p.emitPlaybackState()
	return nil
}

func (p *AppPlayer) setOptions(ctx context.Context, repeatingContext *bool, repeatingTrack *bool, shufflingContext *bool) error {
	var scheduleQueueTopUp bool
	if p == nil || p.state == nil || p.state.player == nil || p.state.player.Options == nil {
		return nil
	}
	curr := playbackdomain.TraversalOptions{
		RepeatContext: p.state.player.Options.RepeatingContext,
		RepeatTrack:   p.state.player.Options.RepeatingTrack,
		Shuffle:       p.state.player.Options.ShufflingContext,
	}
	next := playbackdomain.ResolveOptions(curr, repeatingContext, repeatingTrack, shufflingContext)

	// Try the shuffle toggle first — it's the only operation that can fail.
	// If it fails, repeat options remain unchanged.
	if p.state.tracks != nil && next.Shuffle != curr.Shuffle {
		if err := p.state.tracks.ToggleShuffle(ctx, next.Shuffle); err != nil {
			p.runtime.Log.WithError(err).Errorf("failed toggling shuffle context (value: %t)", next.Shuffle)
			return err
		}
		p.state.player.Options.ShufflingContext = next.Shuffle
		p.resetPlaybackCaches(true)
		p.syncPlayerTrackState(p.state.tracks, nil)
		if next.Shuffle {
			p.scheduleShuffleCacheRefresh()
		}
		scheduleQueueTopUp = true
	}

	var requiresUpdate bool
	if next.RepeatContext != curr.RepeatContext {
		p.state.player.Options.RepeatingContext = next.RepeatContext
		requiresUpdate = true
	}
	if next.RepeatTrack != curr.RepeatTrack {
		p.state.player.Options.RepeatingTrack = next.RepeatTrack
		p.bumpPrefetchGeneration()
		if next.RepeatTrack {
			p.clearSecondaryStream()
		}
		requiresUpdate = true
	}
	if next.Shuffle != curr.Shuffle {
		requiresUpdate = true
	}
	if requiresUpdate {
		p.logRepeatShuffleInvariant("set_options")
		p.updateState()
		p.emitPlaybackState()
	}
	if scheduleQueueTopUp {
		p.scheduleQueueTopUp()
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
	p.syncPlayerTrackState(p.state.tracks, nil)
	p.updateState()
	p.schedulePrefetchNext()
	p.emitPlaybackState()
}

func (p *AppPlayer) queueRemove(index int) {
	if p.state == nil || p.state.tracks == nil {
		return
	}
	if !p.state.tracks.RemoveFromQueue(index) {
		p.runtime.Log.WithField("index", index).Warn("queue remove out of range")
		return
	}
	p.afterQueueEdit()
}

func (p *AppPlayer) queueReorder(from, to int) {
	if p.state == nil || p.state.tracks == nil {
		return
	}
	if !p.state.tracks.ReorderQueue(from, to) {
		p.runtime.Log.WithField("from", from).WithField("to", to).Warn("queue reorder out of range")
		return
	}
	p.afterQueueEdit()
}

// queueJump promotes the up-next entry at `index` to the current position and
// starts playing it; the target becomes queue[0] (the "playing" head).
func (p *AppPlayer) queueJump(ctx context.Context, index int) {
	if p.state == nil || p.state.tracks == nil {
		return
	}
	if !p.state.tracks.GoToQueueEntry(index) {
		p.runtime.Log.WithField("index", index).Warn("queue jump out of range")
		return
	}
	p.syncPlayerTrackState(p.state.tracks, nil)
	p.updateState()
	p.emitPlaybackState()
	if p.player == nil {
		return
	}
	if err := p.loadCurrentTrackFromTransition(ctx, false, true, "queue jump"); err != nil {
		p.runtime.Log.WithError(err).Error("failed loading queue-jump target")
	}
}

// afterQueueEdit refreshes everything a queue mutation affects: track state,
// connect-state, prefetch targets and the pushed TUI queue.
func (p *AppPlayer) afterQueueEdit() {
	p.syncPlayerTrackState(p.state.tracks, nil)
	p.updateState()
	p.schedulePrefetchNext()
	p.emitPlaybackState()
}

func (p *AppPlayer) setQueue(ctx context.Context, prev []*connectpb.ContextTrack, next []*connectpb.ContextTrack) {
	if p.state.tracks == nil {
		p.runtime.Log.Warnf("cannot set queue without a context")
		return
	}
	p.state.tracks.SetQueue(prev, next)
	p.syncPlayerTrackState(p.state.tracks, next)
	p.updateState()
	p.schedulePrefetchNext()
	p.emitPlaybackState()
}

func (p *AppPlayer) play(ctx context.Context) error {
	if p.primaryStream == nil {
		return fmt.Errorf("no primary stream")
	}
	seekPos := golibrespot.TrackPosition(p.state.player, 0)
	seekPos = max(0, min(seekPos, int64(p.primaryStream.Media.Duration())))
	if err := p.player.SeekMs(seekPos); err != nil {
		return fmt.Errorf("failed seeking before play: %w", err)
	}
	if err := p.player.Play(); err != nil {
		return fmt.Errorf("failed starting playback: %w", err)
	}
	streamPos := p.currentPositionMs()
	p.setPlayerPositionAtNow(streamPos)
	p.setPlayerTransportState(true, false, false)
	p.updateState()
	p.schedulePrefetchNext()
	p.emitPlaybackStateLight()
	return nil
}

func (p *AppPlayer) pause(ctx context.Context) error {
	if p.primaryStream == nil {
		return fmt.Errorf("no primary stream")
	}
	streamPos := p.currentPositionMs()
	if err := p.player.Pause(); err != nil {
		return fmt.Errorf("failed pausing playback: %w", err)
	}
	p.setPlayerPositionAtNow(streamPos)
	p.setPlayerTransportState(true, false, true)
	p.updateState()
	p.emitPlaybackStateLight()
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
	if err := p.player.SeekMs(position); err != nil {
		return err
	}
	p.setPlayerPositionAtNow(position)
	p.updateState()
	p.schedulePrefetchNext()
	p.sess.Events().OnPlayerSeek(p.primaryStream, oldPosition, position)
	p.emitPlaybackStateLight()
	return nil
}

func (p *AppPlayer) skipPrev(ctx context.Context, allowSeeking bool) error {
	if allowSeeking && p.currentPositionMs() > 3000 {
		return p.seek(ctx, 0)
	}
	p.sess.Events().OnPlayerSkipBackward(p.primaryStream, p.currentPositionMs())
	if p.state.tracks != nil {
		p.state.tracks.GoPrev()
		p.syncPlayerTrackState(p.state.tracks, nil)
	}
	if err := p.loadCurrentTrackFromTransition(ctx, p.state.player.IsPaused, true, "skip prev"); err != nil {
		return err
	}
	return nil
}

func (p *AppPlayer) skipNext(ctx context.Context, track *connectpb.ContextTrack) error {
	p.sess.Events().OnPlayerSkipForward(p.primaryStream, p.currentPositionMs(), track != nil)
	if track != nil {
		contextSpotType := golibrespot.InferSpotifyIdTypeFromContextUri(p.state.player.ContextUri)
		if err := p.state.tracks.TrySeek(ctx, tracks.ContextTrackComparator(contextSpotType, track)); err != nil {
			return err
		}
		p.bumpPrefetchGeneration()
		p.syncPlayerTrackState(p.state.tracks, nil)
		if err := p.loadCurrentTrackFromTransition(ctx, p.state.player.IsPaused, true, "skip next"); err != nil {
			return err
		}
		return nil
	}
	if _, err := p.advanceNext(ctx, true, true); err != nil {
		return fmt.Errorf("failed skipping to next track: %w", err)
	}
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
		p.syncPlayerTrackState(p.state.tracks, nil)
	}
	if !forceNext && p.state.player.Options != nil && p.state.player.Options.RepeatingTrack && !selection.trackChanged && selection.hasNextTrack {
		p.clearSecondaryStream()
	}
	p.state.player.IsPaused = !selection.hasNextTrack
}

func (p *AppPlayer) currentTrackID() string {
	if p.state == nil || p.state.player == nil || p.state.player.Track == nil {
		return ""
	}
	return golibrespot.NormalizeSpotifyId(p.state.player.Track.Uri)
}

func (p *AppPlayer) logAdvanceInvariants(forceNext bool, selection advanceNextSelection, beforeTrackID string) {
	if p.state == nil || p.state.player == nil {
		return
	}
	afterTrackID := p.currentTrackID()
	repeatTrack := p.state.player.Options != nil && p.state.player.Options.RepeatingTrack
	repeatContext := p.state.player.Options != nil && p.state.player.Options.RepeatingContext
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
}

func (p *AppPlayer) advanceNext(ctx context.Context, forceNext, drop bool) (bool, error) {
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
		spotCtx, err := p.sess.Spclient().ContextResolveAutoplay(ctx, &playerpb.AutoplayContextRequest{
			ContextUri:     new(p.state.player.ContextUri),
			RecentTrackUri: prevTrackUris,
		})
		if err != nil {
			p.runtime.Log.WithError(err).Warnf("failed resolving station for %s", p.state.player.ContextUri)
			return false, nil
		}
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

	maxRetries := 10
	for attempt := range maxRetries {
		if err := p.loadCurrentTrackFromTransition(ctx, !hasNextTrack, drop, "advance next"); err != nil {
			if errors.Is(err, golibrespot.ErrMediaRestricted) || errors.Is(err, golibrespot.ErrNoSupportedFormats) {
				p.runtime.Log.WithError(err).Infof("skipping unplayable media (attempt %d/%d): %s", attempt+1, maxRetries, uri)
				selection = p.selectAdvanceNextTarget(ctx, true)
				if !selection.hasNextTrack {
					p.runtime.Log.Warnf("no more tracks after skipping unplayable media")
					p.state.player.IsPlaying = false
					p.state.player.IsPaused = false
					p.state.player.IsBuffering = false
					return false, nil
				}
				p.applyAdvanceNextSelection(ctx, selection, true)
				hasNextTrack = selection.hasNextTrack
				if p.state.player.Track != nil {
					uri = p.state.player.Track.Uri
				}
				continue
			}
			return false, fmt.Errorf("failed loading current track (advance to %s): %w", uri, err)
		}
		return hasNextTrack, nil
	}
	p.runtime.Log.Warnf("gave up advancing after %d unplayable tracks", maxRetries)
	p.state.player.IsPlaying = false
	p.state.player.IsPaused = false
	p.state.player.IsBuffering = false
	return false, nil
}

func (p *AppPlayer) apiVolume() uint32 {
	return uint32(math.Round(float64(p.state.device.Volume*p.runtime.Cfg.VolumeSteps) / player.MaxStateVolume))
}

func (p *AppPlayer) updateVolume(newVal uint32) {
	if newVal > player.MaxStateVolume {
		newVal = player.MaxStateVolume
	}
	p.player.SetVolume(newVal)
	p.runtime.State.LastVolume = &newVal
	if err := p.runtime.State.Write(); err != nil {
		p.runtime.Log.WithError(err).Error("failed writing state after volume change")
	}
	// Latest-value semantics: drain any pending report (e.g. from the audio
	// backend) so a fresh value can never be lost to the cap-1 buffer.
	select {
	case <-p.volumeUpdate:
	default:
	}
	select {
	case p.volumeUpdate <- float32(newVal) / player.MaxStateVolume:
	default:
	}
}

func (p *AppPlayer) volumeUpdated(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, shuffleContextTimeout)
	defer cancel()
	if err := p.putConnectState(ctx, connectpb.PutStateReason_VOLUME_CHANGED); err != nil {
		p.runtime.Log.WithError(err).Error("failed put state after volume change")
	}
	p.emitPlaybackStateLight()
}

func (p *AppPlayer) stopPlayback(ctx context.Context) error {
	p.player.Stop()
	closeStream(p.primaryStream)
	p.primaryStream = nil
	p.resetPlaybackCaches(true)
	p.state.reset()
	if err := p.putConnectState(ctx, connectpb.PutStateReason_BECAME_INACTIVE); err != nil {
		return fmt.Errorf("failed inactive state put: %w", err)
	}
	p.emitPlaybackState()
	return nil
}
