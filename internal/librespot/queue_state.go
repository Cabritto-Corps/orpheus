package librespot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	golibrespot "github.com/devgianlu/go-librespot"
	extmetadatapb "github.com/devgianlu/go-librespot/proto/spotify/extendedmetadata"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
	"github.com/devgianlu/go-librespot/tracks"

	"orpheus/internal/cache"
)

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
	p.invalidateDerivedQueueCache()
}

func (p *AppPlayer) resetQueueMetaForContext(contextKey string) {
	p.queueMetaMu.Lock()
	if p.queueMetaCache == nil {
		p.queueMetaCache = cache.NewLRU[string, PlaybackStateQueueEntry](8192)
	} else {
		p.queueMetaCache.Clear()
	}
	p.queueMetaMu.Unlock()

	p.queueResolveMu.Lock()
	p.namePreloadContext = contextKey
	p.namePreloadToken++
	p.namePreloadDone = false
	p.queueResolveInFlight = false
	p.queueResolveMu.Unlock()

	p.bumpTrackStateVersion()
}

func (p *AppPlayer) resetPlayedTrackSet() {
	p.playedTrackMu.Lock()
	p.playedTrackURIs = make(map[string]struct{})
	p.playedTrackMu.Unlock()
}

func (p *AppPlayer) invalidateQueueDerivation(resetPlayed bool) {
	if resetPlayed {
		p.resetPlayedTrackSet()
	}
	p.invalidateDerivedQueueCache()
}

func (p *AppPlayer) markPlayedTrack(uri string) {
	id := normalizeSpotifyID(uri)
	if id == "" {
		return
	}
	p.playedTrackMu.Lock()
	if p.playedTrackURIs == nil {
		p.playedTrackURIs = make(map[string]struct{})
	}
	p.playedTrackURIs[id] = struct{}{}
	p.playedTrackMu.Unlock()
}

func (p *AppPlayer) seedPlayedTrackSetFromPlaybackWindow() {
	if p == nil || p.state == nil || p.state.player == nil {
		return
	}
	for _, t := range p.state.player.PrevTracks {
		if t == nil {
			continue
		}
		p.markPlayedTrack(t.Uri)
	}
	if p.state.player.Track != nil {
		p.markPlayedTrack(p.state.player.Track.Uri)
	}
}

func (p *AppPlayer) isPlayedTrack(uri string) bool {
	id := normalizeSpotifyID(uri)
	if id == "" {
		return false
	}
	p.playedTrackMu.RLock()
	_, ok := p.playedTrackURIs[id]
	p.playedTrackMu.RUnlock()
	return ok
}

func (p *AppPlayer) playedTrackCount() int {
	p.playedTrackMu.RLock()
	n := len(p.playedTrackURIs)
	p.playedTrackMu.RUnlock()
	return n
}

func (p *AppPlayer) bumpTrackStateVersion() {
	p.trackStateVersion.Add(1)
	p.invalidateDerivedQueueCache()
}

func (p *AppPlayer) invalidateDerivedQueueCache() {
	p.derivedQueueMu.Lock()
	p.derivedQueueValid = false
	p.derivedQueueEntries = nil
	p.derivedQueueKey = ""
	p.derivedQueueHasMore = false
	p.derivedQueueMu.Unlock()
}

func (p *AppPlayer) currentDerivedQueueKey(shuffle bool) string {
	return fmt.Sprintf("v:%d|shuffle:%t|played:%d", p.trackStateVersion.Load(), shuffle, p.playedTrackCount())
}

func (p *AppPlayer) getDerivedQueueCache(key string) ([]PlaybackStateQueueEntry, bool, bool) {
	p.derivedQueueMu.RLock()
	defer p.derivedQueueMu.RUnlock()
	if !p.derivedQueueValid || p.derivedQueueKey != key {
		return nil, false, false
	}
	out := append([]PlaybackStateQueueEntry(nil), p.derivedQueueEntries...)
	return out, p.derivedQueueHasMore, true
}

func (p *AppPlayer) setDerivedQueueCache(key string, entries []PlaybackStateQueueEntry, hasMore bool) {
	p.derivedQueueMu.Lock()
	p.derivedQueueKey = key
	p.derivedQueueEntries = append([]PlaybackStateQueueEntry(nil), entries...)
	p.derivedQueueHasMore = hasMore
	p.derivedQueueValid = true
	p.derivedQueueMu.Unlock()
}

func (p *AppPlayer) startQueueMetadataResolve() {
	p.queueResolveMu.Lock()
	if p.queueResolveInFlight {
		p.queueResolveMu.Unlock()
		return
	}
	p.queueResolveInFlight = true
	p.queueResolveMu.Unlock()
	go p.resolveQueueMetadataBatchAndEmit()
}

func (p *AppPlayer) shouldPreloadContextNames(contextKey string) (token uint64, ok bool) {
	contextKey = strings.TrimSpace(contextKey)
	if contextKey == "" {
		return 0, false
	}
	p.queueResolveMu.Lock()
	defer p.queueResolveMu.Unlock()
	if p.namePreloadContext != contextKey {
		p.namePreloadContext = contextKey
		p.namePreloadToken++
		p.namePreloadDone = false
		p.queueResolveInFlight = false
	}
	if p.namePreloadDone || p.queueResolveInFlight {
		return 0, false
	}
	p.queueResolveInFlight = true
	return p.namePreloadToken, true
}

func (p *AppPlayer) finishContextNamePreload(contextKey string, token uint64) {
	p.queueResolveMu.Lock()
	defer p.queueResolveMu.Unlock()
	if p.namePreloadContext == contextKey && p.namePreloadToken == token {
		p.namePreloadDone = true
	}
	p.queueResolveInFlight = false
}

func (p *AppPlayer) isContextNamePreloadDone(contextKey string) bool {
	p.queueResolveMu.Lock()
	defer p.queueResolveMu.Unlock()
	contextKey = strings.TrimSpace(contextKey)
	if contextKey == "" {
		return false
	}
	if p.namePreloadContext != contextKey {
		p.namePreloadContext = contextKey
		p.namePreloadToken++
		p.namePreloadDone = false
		p.queueResolveInFlight = false
		return false
	}
	return p.namePreloadDone
}

func (p *AppPlayer) preloadContextQueueMetadata(trackList *tracks.List, contextKey string) {
	token, ok := p.shouldPreloadContextNames(contextKey)
	if !ok || trackList == nil {
		return
	}
	go func(token uint64, contextKey string, list *tracks.List) {
		defer p.finishContextNamePreload(contextKey, token)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		all := list.AllTracks(ctx)
		if len(all) == 0 {
			return
		}
		seen := make(map[string]struct{}, len(all))
		toResolve := make([]string, 0, len(all))
		for _, t := range all {
			if t == nil {
				continue
			}
			id := normalizeSpotifyID(t.Uri)
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
				continue
			}
			toResolve = append(toResolve, t.Uri)
		}
		if len(toResolve) == 0 {
			p.runtime.EmitPlaybackState(p.BuildPlaybackStateUpdate())
			return
		}
		sem := make(chan struct{}, queueResolveConcurrency)
		var wg sync.WaitGroup
		var resolved atomic.Int32
		for _, uri := range toResolve {
			uri := uri
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				e, ok := p.resolveQueueEntry(ctx, uri)
				if !ok {
					return
				}
				p.setCachedQueueMeta(e.ID, e)
				resolved.Add(1)
			}()
		}
		wg.Wait()
		if resolved.Load() > 0 {
			p.runtime.EmitPlaybackState(p.BuildPlaybackStateUpdate())
		}
	}(token, strings.TrimSpace(contextKey), trackList)
}

const queueResolveBatchLimit = 32

func (p *AppPlayer) resolveQueueEntry(ctx context.Context, uri string) (e PlaybackStateQueueEntry, ok bool) {
	id := normalizeSpotifyID(uri)
	if id == "" {
		return PlaybackStateQueueEntry{ID: id}, false
	}
	e = PlaybackStateQueueEntry{ID: id}
	spotID, err := golibrespot.SpotifyIdFromUri(uri)
	if err != nil {
		return e, false
	}
	if spotID.Type() == golibrespot.SpotifyIdTypeTrack {
		var trackMeta metadatapb.Track
		if err := p.sess.Spclient().ExtendedMetadataSimple(ctx, *spotID, extmetadatapb.ExtensionKind_TRACK_V4, &trackMeta); err != nil {
			return e, false
		}
		if trackMeta.Name != nil {
			e.Name = *trackMeta.Name
		}
		if len(trackMeta.Artist) > 0 && trackMeta.Artist[0].Name != nil {
			e.Artist = *trackMeta.Artist[0].Name
		}
		if trackMeta.Duration != nil {
			e.DurationMS = int(*trackMeta.Duration)
		}
	} else if spotID.Type() == golibrespot.SpotifyIdTypeEpisode {
		var epMeta metadatapb.Episode
		if err := p.sess.Spclient().ExtendedMetadataSimple(ctx, *spotID, extmetadatapb.ExtensionKind_EPISODE_V4, &epMeta); err != nil {
			return e, false
		}
		if epMeta.Name != nil {
			e.Name = *epMeta.Name
		}
		if epMeta.Show != nil && epMeta.Show.Name != nil {
			e.Artist = *epMeta.Show.Name
		}
		if epMeta.Duration != nil {
			e.DurationMS = int(*epMeta.Duration)
		}
	}
	if e.Name == "" {
		e.Name = "Unknown track"
	}
	if e.Artist == "" {
		e.Artist = "-"
	}
	return e, true
}

const queueResolveConcurrency = 10

func (p *AppPlayer) resolveQueueMetadataBatchAndEmit() {
	defer func() {
		p.queueResolveMu.Lock()
		p.queueResolveInFlight = false
		p.queueResolveMu.Unlock()
	}()
	if p.state == nil || p.state.player == nil {
		return
	}
	var toResolve []string
	seen := make(map[string]struct{}, queueResolveBatchLimit)
	for _, t := range p.state.player.NextTracks {
		id := normalizeSpotifyID(t.Uri)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if p.getCachedQueueMeta(id) != nil {
			continue
		}
		toResolve = append(toResolve, t.Uri)
		if len(toResolve) >= queueResolveBatchLimit {
			break
		}
	}
	if len(toResolve) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sem := make(chan struct{}, queueResolveConcurrency)
	var wg sync.WaitGroup
	var resolved atomic.Int32
	for _, uri := range toResolve {
		uri := uri
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			e, ok := p.resolveQueueEntry(ctx, uri)
			if ok {
				p.setCachedQueueMeta(e.ID, e)
				resolved.Add(1)
			}
		}()
	}
	wg.Wait()
	if resolved.Load() > 0 {
		p.runtime.EmitPlaybackState(p.BuildPlaybackStateUpdate())
	}
}
