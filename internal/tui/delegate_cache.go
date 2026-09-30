package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
)

// Render is the most expensive per-frame list-tab call (word-wrap + width
// passes per row) but pure in its key — memoized per delegate. Delegates are
// rebuilt on every theme change, so a fresh empty cache can never serve
// stale rows: no epoch, no registry, no reset step.

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

type placeholderCacheKey struct {
	cols, rows int
}

type tabBarCacheKey struct {
	width  int
	active tab
}

// stringCache memos constant-per-key view fragments. It lives on the theme
// bundle, so a theme change builds fresh cold caches and no reset step exists.
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

// cachedDelegate is stored by value in list.Model (copied freely), so the
// cache and the now-playing URI ride behind shared pointers written once by
// the event loop: every copy sees track moves with no flush.
type cachedDelegate struct {
	list.DefaultDelegate
	cache      *delegateCache
	glyph      string
	nowPlaying *string
}

func (d cachedDelegate) currentNowPlaying() string {
	if d.nowPlaying == nil {
		return ""
	}
	return *d.nowPlaying
}

func newCachedPlaylistDelegate(s *themeStyles, nowPlaying *string) cachedDelegate {
	c := &delegateCache{entries: make(map[delegateKey]string, 64)}
	return cachedDelegate{DefaultDelegate: newPlaylistDelegate(s), cache: c, glyph: s.themeNowPlayingGlyph(), nowPlaying: nowPlaying}
}

// trackRow right-aligns the duration at the row edge (the default delegate
// leaves the right half empty). Filtering falls back to the framework
// renderer, so no duration shows while filtering.
func (d cachedDelegate) trackRow(m list.Model, index int, item trackItem) (string, bool) {
	if m.FilterState() == list.Filtering || m.Width() <= 0 {
		return "", false
	}
	dur := ""
	if item.item.DurationMS > 0 {
		dur = fmtDuration(item.item.DurationMS)
	}
	avail := m.Width() - 2
	nameW := max(4, avail-lipgloss.Width(dur)-2)
	line := padCell(truncate(item.item.Name, nameW), nameW) + dur
	key := delegateKey{
		width:    m.Width(),
		height:   d.Height(),
		selected: index == m.Index(),
		filter:   m.FilterValue(),
		filtered: m.FilterState() == list.FilterApplied,
		text:     item.item.Name + "\x00" + item.item.Artist + "\x00" + dur,
	}
	if s, hit := d.cache.get(key); hit {
		return s, true
	}
	var out string
	if index == m.Index() {
		out = d.Styles.SelectedTitle.Render(line) + "\n" +
			d.Styles.SelectedDesc.Render(item.item.Artist)
	} else {
		out = d.Styles.NormalTitle.Render(line) + "\n" +
			d.Styles.NormalDesc.Render(item.item.Artist)
	}
	d.cache.put(key, out)
	return out, true
}

func (d cachedDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pi, ok := item.(playlistItem)
	if !ok {
		if ti, isTrack := item.(trackItem); isTrack {
			if line, ok := d.trackRow(m, index, ti); ok {
				fmt.Fprint(w, line)
				return
			}
		}
		d.DefaultDelegate.Render(w, m, index, item)
		return
	}
	// Stamp the now-playing flag before reading the title: the glyph
	// becomes part of Title(), which automatically invalidates the cache
	// key for that row when playback moves to another context.
	pi.nowPlaying = strings.TrimSpace(pi.summary.URI) == d.currentNowPlaying()
	pi.glyph = d.glyph
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
