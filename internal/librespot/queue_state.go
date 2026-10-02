package librespot

import (
	"context"
	"strings"

	golibrespot "github.com/elxgy/go-librespot"
	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/spclient"

	"orpheus/internal/cache"
)

func queueMetaImageURL(p *AppPlayer, coverFileId []byte) string {
	// Snapshot under lock: metadata-resolution goroutine vs Run-goroutine swap.
	prod := p.prodInfoSnapshot()
	if prod == nil || len(coverFileId) == 0 {
		return ""
	}
	if u := prod.ImageUrl(coverFileId); u != nil {
		return *u
	}
	return ""
}

func (p *AppPlayer) getCachedQueueMeta(id string) *PlaybackStateQueueEntry {
	p.queueMetaMu.RLock()
	defer p.queueMetaMu.RUnlock()
	if p.queueMetaCache == nil {
		return nil
	}
	if e, ok := p.queueMetaCache.Peek(id); ok {
		return &e
	}
	return nil
}

func (p *AppPlayer) setCachedQueueMeta(id string, e PlaybackStateQueueEntry) {
	p.queueMetaMu.Lock()
	defer p.queueMetaMu.Unlock()
	if p.queueMetaCache == nil {
		p.queueMetaCache = cache.NewLRU[string, PlaybackStateQueueEntry](8192)
	}
	p.queueMetaCache.Set(id, e)
}

func (p *AppPlayer) resetQueueMetaForContext() {
	p.queueMetaMu.Lock()
	defer p.queueMetaMu.Unlock()
	p.queueMetaCache = cache.NewLRU[string, PlaybackStateQueueEntry](8192)
}

// headImageWindow bounds how many upcoming tracks get cover images resolved
// ahead of playback. Context metadata carries names but no art, and the TUI
// prefetch only fires on entries carrying ImageURL — without this window
// every skip pays a cold cover fetch while the old art holds.
const headImageWindow = 8

// queueImageSweepWindow bounds the background sweep past the head: the full
// playing context eventually carries cover URLs so any skip lands warm, not
// just the next eight. Meta entries are small strings in an 8192-cap cache;
// the bound paces network, not memory.
const queueImageSweepWindow = 128

// queueMetaBatchChunk caps one extended-metadata request: a whole context in
// a single BatchedEntityRequest risks a megarequest timeout, so large
// sweeps go out as sequential chunks under the caller's deadline.
const queueMetaBatchChunk = 50

// headImageURIs selects the upcoming tracks whose covers still need
// resolution, in order, capped at n. Pure: callers pass an already-read
// slice so no tracks.List access happens off the Run goroutine.
func headImageURIs(upcoming []*connectpb.ProvidedTrack, missing func(id string) bool, n int) []string {
	if n <= 0 {
		return nil
	}
	var out []string
	for _, t := range upcoming {
		if len(out) >= n {
			break
		}
		if t == nil {
			continue
		}
		id := golibrespot.NormalizeSpotifyId(t.Uri)
		if id == "" || !missing(id) {
			continue
		}
		out = append(out, t.Uri)
	}
	return out
}

// queueHeadImageMissing reports whether a queue entry still needs its cover
// resolved before the prefetch can fire on it.
func (p *AppPlayer) queueHeadImageMissing(id string) bool {
	e := p.getCachedQueueMeta(id)
	return e == nil || strings.TrimSpace(e.ImageURL) == ""
}

func (p *AppPlayer) resolveContextQueueMetadata(ctx context.Context, all []*connectpb.ProvidedTrack, headURIs []string) bool {
	if len(all) == 0 && len(headURIs) == 0 {
		return false
	}

	seen := make(map[string]struct{}, len(all))
	toResolve := make([]string, 0, len(all))
	changed := false
	for _, t := range all {
		if t == nil {
			continue
		}
		id := golibrespot.NormalizeSpotifyId(t.Uri)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		if p.getCachedQueueMeta(id) != nil {
			continue
		}
		e := PlaybackStateQueueEntry{ID: id}
		if t.Metadata != nil {
			e.Name = metadataValue(t.Metadata, "title", "name", "track_name", "entity_name", "track_title")
			e.Artist = metadataValue(t.Metadata, "artist_name", "artist", "artists", "show_name", "album_artist_name")
			e.DurationMS = metadataDurationMS(t.Metadata)
		}
		if e.Name != "" {
			if e.Artist == "" {
				e.Artist = "-"
			}
			p.setCachedQueueMeta(id, e)
			changed = true
			continue
		}
		toResolve = append(toResolve, t.Uri)
	}

	// Head images: named entries never enter the batch above, so the prefetch
	// would never see their covers. Union them explicitly. (Checked against
	// queued membership, not seen: seen holds every distinct id including
	// cached ones that were never queued.)
	queued := make(map[string]struct{}, len(toResolve)+len(headURIs))
	for _, uri := range toResolve {
		queued[golibrespot.NormalizeSpotifyId(uri)] = struct{}{}
	}
	for _, uri := range headURIs {
		id := golibrespot.NormalizeSpotifyId(uri)
		if id == "" {
			continue
		}
		if _, exists := queued[id]; exists {
			continue
		}
		queued[id] = struct{}{}
		if !p.queueHeadImageMissing(id) {
			continue
		}
		toResolve = append(toResolve, uri)
	}

	p.resolveQueueMetadataBatch(ctx, toResolve)
	return changed
}

// resolveQueueMetadataBatch resolves one metadata batch and merges names and
// cover images into the queue cache. Large sweeps go out chunked so one
// giant context cannot build a megarequest; chunks run sequentially under
// the caller's deadline and the first context error stops the rest.
// Network runs on the caller's goroutine — call from background workers,
// never Run. It reports whether the merge taught the cache anything new;
// callers signal Run to re-push only then, so a persistently unresolvable
// head cannot arm a re-resolve loop.
func (p *AppPlayer) resolveQueueMetadataBatch(ctx context.Context, uris []string) bool {
	if len(uris) == 0 {
		return false
	}
	if len(uris) > queueMetaBatchChunk {
		changed := false
		for start := 0; start < len(uris); start += queueMetaBatchChunk {
			end := min(start+queueMetaBatchChunk, len(uris))
			if ctx.Err() != nil {
				break
			}
			if p.resolveQueueMetadataChunk(ctx, uris[start:end]) {
				changed = true
			}
		}
		return changed
	}
	return p.resolveQueueMetadataChunk(ctx, uris)
}

func (p *AppPlayer) resolveQueueMetadataChunk(ctx context.Context, uris []string) bool {
	if len(uris) == 0 {
		return false
	}
	fetch := p.metaBatchFetch
	if fetch == nil {
		if p.sess == nil {
			return false
		}
		fetch = func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
			return p.sess.Spclient().ResolveTrackOrEpisodeMetadataBatch(ctx, uris)
		}
	}
	batch, err := fetch(ctx, uris)
	if err != nil {
		if ctx.Err() == nil && p.runtime != nil {
			p.runtime.Log.WithError(err).Warn("batch metadata resolution failed")
		}
		return false
	}
	return p.mergeQueueBatchResult(batch)
}

// mergeQueueBatchResult merges one metadata batch into the queue cache,
// filling names and cover images. Idempotent: re-resolving refreshes.
// It reports whether any entry is new or gained a name or cover image;
// unchanged re-resolves must not re-arm the delivery signal.
func (p *AppPlayer) mergeQueueBatchResult(batch map[string]spclient.ResolvedEntry) (changed bool) {
	for uri, entry := range batch {
		id := golibrespot.NormalizeSpotifyId(uri)
		if id == "" {
			continue
		}
		e := PlaybackStateQueueEntry{ID: id, Name: entry.Name, Artist: entry.Artist, DurationMS: entry.DurationMS}
		if e.Artist == "" {
			e.Artist = "-"
		}
		e.ImageURL = queueMetaImageURL(p, entry.AlbumCoverFileId)
		prev := p.getCachedQueueMeta(id)
		if prev == nil || prev.Name != e.Name || prev.ImageURL != e.ImageURL {
			changed = true
		}
		p.setCachedQueueMeta(id, e)
	}
	return changed
}

// maybeSweepQueueImages resolves cover URLs for the playing context past
// the head window, so a far skip lands as warm as the next one. It skips
// the head outright (the head warm owns it — refetching it here would
// double every batch), with its own collapse flag alongside. The missing()
// check keeps it quiet once the context is imaged.
func (p *AppPlayer) maybeSweepQueueImages() {
	if p == nil || p.state == nil || p.state.tracks == nil {
		return
	}
	if !p.queueSweepWarmInFlight.CompareAndSwap(false, true) {
		return
	}
	upcoming := p.state.tracks.UpcomingTracksLoaded(headImageWindow + queueImageSweepWindow)
	if len(upcoming) > headImageWindow {
		upcoming = upcoming[headImageWindow:]
	} else {
		upcoming = nil
	}
	uris := headImageURIs(upcoming, p.queueHeadImageMissing, queueImageSweepWindow)
	if len(uris) == 0 {
		p.queueSweepWarmInFlight.Store(false)
		return
	}
	go func() {
		defer p.queueSweepWarmInFlight.Store(false)
		metaCtx, metaCancel := context.WithTimeout(p.ownerContext(), metadataBatchTimeout)
		defer metaCancel()
		if p.resolveQueueMetadataBatch(metaCtx, uris) {
			p.signalQueueMetaUpdated()
		}
	}()
}

// maybeWarmQueueHeadImages keeps the coming covers ahead of the next skip.
// Run-side: cheap cache check, then one collapsing background batch. Called
// after a track loads, the single funnel for initial loads, skips,
// auto-advances, and recovery reloads.
func (p *AppPlayer) maybeWarmQueueHeadImages() {
	if p == nil || p.state == nil || p.state.tracks == nil {
		return
	}
	if !p.queueHeadWarmInFlight.CompareAndSwap(false, true) {
		return
	}
	uris := headImageURIs(p.state.tracks.UpcomingTracksLoaded(headImageWindow), p.queueHeadImageMissing, headImageWindow)
	if len(uris) == 0 {
		p.queueHeadWarmInFlight.Store(false)
		return
	}
	go func() {
		defer p.queueHeadWarmInFlight.Store(false)
		metaCtx, metaCancel := context.WithTimeout(p.ownerContext(), metadataBatchTimeout)
		defer metaCancel()
		if p.resolveQueueMetadataBatch(metaCtx, uris) {
			p.signalQueueMetaUpdated()
		}
	}()
}

// signalQueueMetaUpdated tells Run a background metadata batch taught the
// queue cache something new. Cap-1 and non-blocking: overlapping batches
// coalesce into one delivery, and a bare test player (nil channel) skips.
func (p *AppPlayer) signalQueueMetaUpdated() {
	if p == nil {
		return
	}
	select {
	case p.queueMetaUpdated <- struct{}{}:
	default:
	}
}

// handleQueueMetaUpdated delivers a completed background metadata batch:
// re-emit with queue so the TUI learns the new cover URLs (its prefetch
// only fires on entries carrying ImageURL), then re-warm any heads the
// batch didn't cover — skips taken while the batch was in flight leave new
// heads no other trigger would warm before the next track load.
func (p *AppPlayer) handleQueueMetaUpdated() {
	if p == nil || p.state == nil || p.state.tracks == nil {
		return
	}
	p.emitPlaybackState()
	p.maybeWarmQueueHeadImages()
	p.maybeSweepQueueImages()
}
