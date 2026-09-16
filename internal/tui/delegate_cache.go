package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/list"
)

// DefaultDelegate.Render is the most expensive per-frame call on list tabs
// (word-wrap + grapheme width passes per visible row), yet its output is a
// pure function of (item text, width, selection, filter state, delegate
// height, styles). Rendering is memoized on that key; the theme's
// applyTheme clears the registry so style swaps never serve stale rows.

type delegateKey struct {
	width    int
	height   int
	selected bool
	filter   string
	filtered bool
	text     string
}

type delegateCache struct {
	mu      sync.Mutex
	entries map[delegateKey]string
}

func (c *delegateCache) get(k delegateKey) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries[k]
	return s, ok
}

func (c *delegateCache) put(k delegateKey, s string) {
	c.mu.Lock()
	if len(c.entries) >= 512 {
		c.entries = make(map[delegateKey]string, 64)
	}
	c.entries[k] = s
	c.mu.Unlock()
}

func (c *delegateCache) reset() {
	c.mu.Lock()
	c.entries = make(map[delegateKey]string, 64)
	c.mu.Unlock()
}

// delegateCaches is bounded: a theme rebuild creates fresh caches and the
// replaced objects are garbage, so the oldest registry slot is recycled.
var delegateCaches []*delegateCache

const maxDelegateCaches = 16

func registerDelegateCache(c *delegateCache) {
	if len(delegateCaches) >= maxDelegateCaches {
		delegateCaches = delegateCaches[1:]
	}
	delegateCaches = append(delegateCaches, c)
}

type placeholderCacheKey struct {
	cols, rows int
	epoch      uint64
}

var placeholderCache = newStringCache[placeholderCacheKey]()

type tabBarCacheKey struct {
	width  int
	active tab
	epoch  uint64
}

var (
	themeEpoch  uint64
	tabBarCache = newStringCache[tabBarCacheKey]()
)

// stringCache is a tiny memo for constant-per-key view fragments. Entries
// are only valid until the theme changes (resetStringCaches clears the
// whole registry).
type stringCache[K comparable] struct {
	mu      sync.Mutex
	entries map[K]string
}

func newStringCache[K comparable]() *stringCache[K] {
	return &stringCache[K]{entries: make(map[K]string, 8)}
}

func (c *stringCache[K]) get(k K) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.entries[k]
	return s, ok
}

func (c *stringCache[K]) put(k K, s string) {
	c.mu.Lock()
	if len(c.entries) >= 64 {
		c.entries = make(map[K]string, 8)
	}
	c.entries[k] = s
	c.mu.Unlock()
}

func (c *stringCache[K]) reset() {
	c.mu.Lock()
	c.entries = make(map[K]string, 8)
	c.mu.Unlock()
}

func resetStringCaches() {
	for _, c := range delegateCaches {
		c.reset()
	}
	tabBarCache.reset()
	placeholderCache.reset()
}

// cachedDelegate wraps list.DefaultDelegate. It is stored by value inside
// list.Model (which bubbletea copies freely), so the cache lives behind a
// pointer shared by every copy.
type cachedDelegate struct {
	list.DefaultDelegate
	cache *delegateCache
}

// nowPlayingContextURI identifies the playlist/album the player is currently
// drawing from; written only from the event loop (same goroutine View runs
// on), read inside the delegate render.
var nowPlayingContextURI string

func newCachedPlaylistDelegate() cachedDelegate {
	c := &delegateCache{entries: make(map[delegateKey]string, 64)}
	registerDelegateCache(c)
	return cachedDelegate{DefaultDelegate: newPlaylistDelegate(), cache: c}
}

func (d cachedDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pi, ok := item.(playlistItem)
	if !ok {
		d.DefaultDelegate.Render(w, m, index, item)
		return
	}
	// Stamp the now-playing flag before reading the title: the glyph
	// becomes part of Title(), which automatically invalidates the cache
	// key for that row when playback moves to another context.
	pi.nowPlaying = strings.TrimSpace(pi.summary.URI) == nowPlayingContextURI
	title := pi.Title()
	desc := pi.Description()
	key := delegateKey{
		width:    m.Width(),
		height:   d.Height(),
		selected: index == m.Index(),
		filter:   m.FilterValue(),
		filtered: m.FilterState() == list.Filtering || m.FilterState() == list.FilterApplied,
		text:     title + "\x00" + desc,
	}
	if s, hit := d.cache.get(key); hit {
		fmt.Fprint(w, s)
		return
	}

	var sb strings.Builder
	d.DefaultDelegate.Render(&sb, m, index, item)
	d.cache.put(key, sb.String())
	fmt.Fprint(w, sb.String())
}
