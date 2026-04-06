package librespot

import (
	"context"
	"strings"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/tracks"

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
}

func (p *AppPlayer) resetQueueMetaForContext(contextKey string) {
	p.queueMetaMu.Lock()
	if p.queueMetaCache == nil {
		p.queueMetaCache = cache.NewLRU[string, PlaybackStateQueueEntry](8192)
	}
	p.queueMetaMu.Unlock()

	p.queueResolveMu.Lock()
	p.namePreloadContext = contextKey
	p.namePreloadToken++
	p.namePreloadDone = false
	p.queueResolveInFlight = false
	p.queueResolveMu.Unlock()
}

func (p *AppPlayer) claimContextNamePreload(contextKey string) (token uint64, ok bool) {
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

func (p *AppPlayer) checkNamePreloadStatus(contextKey string) bool {
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

func (p *AppPlayer) resolveContextQueueMetadata(ctx context.Context, trackList *tracks.List) {
	if trackList == nil {
		return
	}

	all := trackList.AllTracks(ctx)
	if len(all) == 0 {
		return
	}

	seen := make(map[string]struct{}, len(all))
	toResolve := make([]string, 0, len(all))
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
			continue
		}
		toResolve = append(toResolve, t.Uri)
	}

	if len(toResolve) == 0 {
		return
	}

	batch, err := p.sess.Spclient().ResolveTrackOrEpisodeMetadataBatch(ctx, toResolve)
	if err != nil {
		p.runtime.Log.WithError(err).Warn("batch metadata resolution failed")
		return
	}
	for uri, entry := range batch {
		id := golibrespot.NormalizeSpotifyId(uri)
		if id == "" {
			continue
		}
		e := PlaybackStateQueueEntry{ID: id, Name: entry.Name, Artist: entry.Artist, DurationMS: entry.DurationMS}
		if e.Artist == "" {
			e.Artist = "-"
		}
		p.setCachedQueueMeta(id, e)
	}
}
