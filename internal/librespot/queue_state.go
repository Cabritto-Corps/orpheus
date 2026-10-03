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

// Context metadata carries names but no art, and the TUI prefetch fires only on ImageURL entries.
const headImageWindow = 8

// The bound paces network, not memory: entries are small strings in an 8192-cap cache.
const queueImageSweepWindow = 128

// A whole context in one batch risks a megarequest timeout; large sweeps go out chunked.
const queueMetaBatchChunk = 50

// Callers pass an already-read slice: tracks.List must never be touched off the Run goroutine.
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

	// Named head entries never enter the batch above, so the prefetch would never see their
	// covers; union them. Membership traces toResolve, not seen (seen holds cached never-queued ids).
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

// Network runs on the caller's goroutine — call from background workers, never Run.
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

// Reports changed only when an entry is new or gained a name or cover image, so unchanged
// re-resolves cannot re-arm the delivery signal (a persistently unresolvable head would loop).
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

// Skips the head window outright — the head warm owns it; refetching it here would double every batch.
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

// Run-side cheap cache check, then one collapsing background batch; the single funnel for track loads.
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

// Cap-1 non-blocking signal: overlapping batches coalesce into one delivery; nil channel (tests) skips.
func (p *AppPlayer) signalQueueMetaUpdated() {
	if p == nil {
		return
	}
	select {
	case p.queueMetaUpdated <- struct{}{}:
	default:
	}
}

// Delivers a completed batch: re-emit so the TUI prefetch sees new covers, then re-warm heads it
// didn't cover — mid-flight skips leave new heads no other trigger would warm.
func (p *AppPlayer) handleQueueMetaUpdated() {
	if p == nil || p.state == nil || p.state.tracks == nil {
		return
	}
	p.emitPlaybackState()
	p.maybeWarmQueueHeadImages()
	p.maybeSweepQueueImages()
}
