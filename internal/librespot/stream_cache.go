package librespot

import (
	golibrespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/player"
)

const transitionStreamCacheMax = 4

func streamCacheKey(id golibrespot.SpotifyId) string {
	return id.Uri()
}

func (p *AppPlayer) hasTransitionCachedStream(id golibrespot.SpotifyId) bool {
	p.transitionStreamMu.Lock()
	defer p.transitionStreamMu.Unlock()
	_, ok := p.transitionStreamCache[streamCacheKey(id)]
	return ok
}

func (p *AppPlayer) takeTransitionCachedStream(id golibrespot.SpotifyId) *player.Stream {
	key := streamCacheKey(id)
	p.transitionStreamMu.Lock()
	defer p.transitionStreamMu.Unlock()
	if p.transitionStreamCache == nil {
		return nil
	}
	stream, ok := p.transitionStreamCache[key]
	if !ok {
		return nil
	}
	delete(p.transitionStreamCache, key)
	for i := range p.transitionStreamOrder {
		if p.transitionStreamOrder[i] == key {
			p.transitionStreamOrder = append(p.transitionStreamOrder[:i], p.transitionStreamOrder[i+1:]...)
			break
		}
	}
	return stream
}

func (p *AppPlayer) putTransitionCachedStream(id golibrespot.SpotifyId, stream *player.Stream) bool {
	if stream == nil {
		return false
	}
	key := streamCacheKey(id)
	p.transitionStreamMu.Lock()
	defer p.transitionStreamMu.Unlock()
	if p.transitionStreamCache == nil {
		p.transitionStreamCache = make(map[string]*player.Stream, transitionStreamCacheMax)
	}
	if _, exists := p.transitionStreamCache[key]; exists {
		return false
	}
	if len(p.transitionStreamOrder) >= transitionStreamCacheMax {
		evict := p.transitionStreamOrder[0]
		p.transitionStreamOrder = p.transitionStreamOrder[1:]
		delete(p.transitionStreamCache, evict)
	}
	p.transitionStreamOrder = append(p.transitionStreamOrder, key)
	p.transitionStreamCache[key] = stream
	return true
}

func (p *AppPlayer) clearTransitionStreamCache() {
	p.transitionStreamMu.Lock()
	p.transitionStreamCache = make(map[string]*player.Stream, transitionStreamCacheMax)
	p.transitionStreamOrder = nil
	p.prefetchPending = make(map[string]struct{}, transitionStreamCacheMax)
	p.transitionStreamMu.Unlock()
}

func (p *AppPlayer) bumpPrefetchGeneration() uint64 {
	next := p.prefetchGen.Add(1)
	p.transitionStreamMu.Lock()
	p.prefetchPending = make(map[string]struct{}, transitionStreamCacheMax)
	p.transitionStreamMu.Unlock()
	return next
}

func (p *AppPlayer) hasPrefetchPending(id golibrespot.SpotifyId) bool {
	key := streamCacheKey(id)
	p.transitionStreamMu.Lock()
	defer p.transitionStreamMu.Unlock()
	_, ok := p.prefetchPending[key]
	return ok
}

func (p *AppPlayer) markPrefetchPending(id golibrespot.SpotifyId) bool {
	key := streamCacheKey(id)
	p.transitionStreamMu.Lock()
	defer p.transitionStreamMu.Unlock()
	if p.prefetchPending == nil {
		p.prefetchPending = make(map[string]struct{}, transitionStreamCacheMax)
	}
	if _, exists := p.prefetchPending[key]; exists {
		return false
	}
	p.prefetchPending[key] = struct{}{}
	return true
}

func (p *AppPlayer) clearPrefetchPending(id golibrespot.SpotifyId) {
	key := streamCacheKey(id)
	p.transitionStreamMu.Lock()
	delete(p.prefetchPending, key)
	p.transitionStreamMu.Unlock()
}
