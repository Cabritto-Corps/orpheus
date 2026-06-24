package librespot

import (
	"sync"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
)

const transitionStreamCacheMax = 16

func streamCacheKey(id golibrespot.SpotifyId) string {
	return id.Uri()
}

type transitionCache struct {
	mu      sync.Mutex
	streams map[string]*player.Stream
	order   []string
	pending map[string]struct{}
}

func newTransitionCache() *transitionCache {
	return &transitionCache{
		streams: make(map[string]*player.Stream, transitionStreamCacheMax),
		pending: make(map[string]struct{}, transitionStreamCacheMax),
	}
}

func (c *transitionCache) Has(id golibrespot.SpotifyId) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.streams[streamCacheKey(id)]
	return ok
}

func (c *transitionCache) Take(id golibrespot.SpotifyId) *player.Stream {
	key := streamCacheKey(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams == nil {
		return nil
	}
	stream, ok := c.streams[key]
	if !ok {
		return nil
	}
	delete(c.streams, key)
	for i := range c.order {
		if c.order[i] == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	return stream
}

func (c *transitionCache) Put(id golibrespot.SpotifyId, stream *player.Stream) bool {
	if stream == nil {
		return false
	}
	key := streamCacheKey(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams == nil {
		c.streams = make(map[string]*player.Stream, transitionStreamCacheMax)
	}
	if _, exists := c.streams[key]; exists {
		return false
	}
	if len(c.order) >= transitionStreamCacheMax {
		evict := c.order[0]
		c.order = c.order[1:]
		if old := c.streams[evict]; old != nil {
			closeStream(old)
		}
		delete(c.streams, evict)
	}
	c.order = append(c.order, key)
	c.streams[key] = stream
	return true
}

func (c *transitionCache) Clear() {
	c.mu.Lock()
	for _, s := range c.streams {
		closeStream(s)
	}
	c.streams = make(map[string]*player.Stream, transitionStreamCacheMax)
	c.order = nil
	c.pending = make(map[string]struct{}, transitionStreamCacheMax)
	c.mu.Unlock()
}

func (c *transitionCache) HasPending(id golibrespot.SpotifyId) bool {
	key := streamCacheKey(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pending[key]
	return ok
}

func (c *transitionCache) MarkPending(id golibrespot.SpotifyId) bool {
	key := streamCacheKey(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = make(map[string]struct{}, transitionStreamCacheMax)
	}
	if _, exists := c.pending[key]; exists {
		return false
	}
	c.pending[key] = struct{}{}
	return true
}

func (c *transitionCache) ClearPending(id golibrespot.SpotifyId) {
	key := streamCacheKey(id)
	c.mu.Lock()
	delete(c.pending, key)
	c.mu.Unlock()
}

func (c *transitionCache) ResetPending() {
	c.mu.Lock()
	c.pending = make(map[string]struct{}, transitionStreamCacheMax)
	c.mu.Unlock()
}

func (p *AppPlayer) hasTransitionCachedStream(id golibrespot.SpotifyId) bool {
	return p.transitionCache.Has(id)
}

func (p *AppPlayer) takeTransitionCachedStream(id golibrespot.SpotifyId) *player.Stream {
	return p.transitionCache.Take(id)
}

func (p *AppPlayer) putTransitionCachedStream(id golibrespot.SpotifyId, stream *player.Stream) bool {
	return p.transitionCache.Put(id, stream)
}

func (p *AppPlayer) clearTransitionStreamCache() {
	p.transitionCache.Clear()
}

func (p *AppPlayer) bumpPrefetchGeneration() uint64 {
	next := p.prefetchGen.Add(1)
	p.transitionCache.ResetPending()
	return next
}

func (p *AppPlayer) hasPrefetchPending(id golibrespot.SpotifyId) bool {
	return p.transitionCache.HasPending(id)
}

func (p *AppPlayer) markPrefetchPending(id golibrespot.SpotifyId) bool {
	return p.transitionCache.MarkPending(id)
}

func (p *AppPlayer) clearPrefetchPending(id golibrespot.SpotifyId) {
	p.transitionCache.ClearPending(id)
}
