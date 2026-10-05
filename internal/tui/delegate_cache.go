package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The memo for the most expensive per-frame list-tab render (word-wrap +
// width passes per row). Delegates are rebuilt on every theme change, so a
// fresh empty cache can never serve stale rows — no epoch, no reset step.

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

type tabBarCacheKey struct {
	width  int
	active tab
}

// Output varies only by width and quantized fill cell, so one key covers a geometry.
type barCacheKey struct {
	width  int
	filled int
}

type dividerCacheKey struct {
	horizontal bool
	n          int
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
// cache rides behind a shared pointer written once by the event loop.
type cachedDelegate struct {
	list.DefaultDelegate
	cache *delegateCache
}

func newCachedPlaylistDelegate(s *themeStyles) cachedDelegate {
	c := &delegateCache{entries: make(map[delegateKey]string, 64)}
	return cachedDelegate{DefaultDelegate: newPlaylistDelegate(s), cache: c}
}

// trackRow right-aligns the duration at the row edge (the default delegate
// leaves the right half empty). Filtering falls back to the framework
// renderer, so no duration shows while filtering.
func (d cachedDelegate) trackRow(m list.Model, index int, name, artist string, durMS int) (string, bool) {
	if m.Width() <= 0 {
		return "", false
	}
	// The framework renderer shows no selection while the filter input is
	// active; the inline path below marks the cursor row the same as it
	// does once the filter is applied.
	if m.FilterState() == list.Filtering && (m.FilterValue() == "" || index != m.Index()) {
		return "", false
	}
	dur := ""
	if durMS > 0 {
		dur = fmtDuration(durMS)
	}
	avail := m.Width() - 2
	nameW := max(4, avail-lipgloss.Width(dur)-2)
	line := padCell(truncate(name, nameW), nameW) + dur
	key := delegateKey{
		width:    m.Width(),
		height:   d.Height(),
		selected: index == m.Index(),
		filter:   m.FilterValue(),
		filtered: m.FilterState() == list.FilterApplied,
		text:     name + "\x00" + artist + "\x00" + dur,
	}
	if s, hit := d.cache.get(key); hit {
		return s, true
	}
	var out string
	if index == m.Index() {
		out = d.Styles.SelectedTitle.Render(line) + "\n" +
			d.Styles.SelectedDesc.Render(artist)
	} else {
		out = d.Styles.NormalTitle.Render(line) + "\n" +
			d.Styles.NormalDesc.Render(artist)
	}
	d.cache.put(key, out)
	return out, true
}

// renderFilteringSelected marks the cursor row while the filter input is
// active: bubbles' default delegate renders no selection while typing, so
// the row would sit unmarked through the whole search session. Mirrors the
// delegate's own selected branch, including match highlighting.
func (d cachedDelegate) renderFilteringSelected(m list.Model, index int, title, desc string) (string, bool) {
	if m.FilterState() != list.Filtering || m.FilterValue() == "" || index != m.Index() {
		return "", false
	}
	textwidth := m.Width() - d.Styles.NormalTitle.GetPaddingLeft() - d.Styles.NormalTitle.GetPaddingRight()
	title = ansi.Truncate(title, textwidth, "…")
	if d.ShowDescription {
		var lines []string
		for i, line := range strings.Split(desc, "\n") {
			if i >= d.Height()-1 {
				break
			}
			lines = append(lines, ansi.Truncate(line, textwidth, "…"))
		}
		desc = strings.Join(lines, "\n")
	}
	unmatched := d.Styles.SelectedTitle.Inline(true)
	matched := unmatched.Inherit(d.Styles.FilterMatch)
	title = lipgloss.StyleRunes(title, m.MatchesForItem(index), matched, unmatched)
	var sb strings.Builder
	sb.WriteString(d.Styles.SelectedTitle.Render(title))
	sb.WriteString("\n")
	sb.WriteString(d.Styles.SelectedDesc.Render(desc))
	return sb.String(), true
}

func (d cachedDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	pi, ok := item.(playlistItem)
	if !ok {
		if ti, isTrack := item.(trackItem); isTrack {
			if line, ok := d.trackRow(m, index, ti.item.Name, ti.item.Artist, ti.item.DurationMS); ok {
				fmt.Fprint(w, line)
				return
			}
		}
		if si, isResult := item.(searchResultItem); isResult && si.result.Kind == "track" {
			if line, ok := d.trackRow(m, index, si.result.Name, si.result.Owner, si.result.DurationMS); ok {
				fmt.Fprint(w, line)
				return
			}
		}
		d.DefaultDelegate.Render(w, m, index, item)
		return
	}
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
	// The override render is deliberately not cached: the applied-filter
	// state must keep serving the delegate's own bytes, not a replica.
	if s, ok := d.renderFilteringSelected(m, index, title, desc); ok {
		fmt.Fprint(w, s)
		return
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
