package tui

import (
	stdlist "container/list"
	"log/slog"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"orpheus/internal/spotify"
)

type coverManager struct {
	imageRetryCount map[string]int
	imageRetryToken map[string]int
	resolveInFlight map[string]struct{}
	queue           *stdlist.List
	queued          map[string]*stdlist.Element
	// pinnedHead tracks the head-window URLs pinned against LRU eviction.
	// A map, not a scalar: coverManager travels by value, so scalar
	// writes would be silently dropped.
	pinnedHead map[string]struct{}
	// sweepPausedUntil parks background sweeps behind a server penalty
	// while priority (player-cover) loads keep flowing.
	sweepPausedUntil time.Time
	// Element pointers stay valid across pops and removes, so queued needs
	// no index bookkeeping that could desync.
	playerCoverFailStreak int
	kittyRecoveryStreak   int
	// kittyFellBack: kitty was disabled by the failure fallback, not merely
	// undetected — recovery must only re-enable a protocol that worked before.
	kittyFellBack bool
}

func newCoverManager() coverManager {
	return coverManager{
		imageRetryCount: make(map[string]int),
		imageRetryToken: make(map[string]int),
		resolveInFlight: make(map[string]struct{}),
		queue:           stdlist.New(),
		queued:          make(map[string]*stdlist.Element),
		pinnedHead:      make(map[string]struct{}),
	}
}

func (c *coverManager) clearRetry(url string) {
	delete(c.imageRetryCount, url)
	delete(c.imageRetryToken, url)
}

func (c *coverManager) nextRetry(url string) (attempt int, token int) {
	attempt = c.imageRetryCount[url] + 1
	c.imageRetryCount[url] = attempt
	c.imageRetryToken[url]++
	token = c.imageRetryToken[url]
	return attempt, token
}

func (c *coverManager) retryToken(url string) int {
	return c.imageRetryToken[url]
}

func (c *coverManager) queueResolve(kind, id string) bool {
	key := coverResolveKey(kind, id)
	if _, exists := c.resolveInFlight[key]; exists {
		return false
	}
	c.resolveInFlight[key] = struct{}{}
	return true
}

func (c *coverManager) clearResolve(kind, id string) {
	delete(c.resolveInFlight, coverResolveKey(kind, id))
}

func (c *coverManager) enqueueURL(url string) bool {
	url = strings.TrimSpace(url)
	if url == "" {
		return false
	}
	if _, exists := c.queued[url]; exists {
		return false
	}
	c.queued[url] = c.queue.PushBack(url)
	return true
}

func (c *coverManager) popURL() (string, bool) {
	el := c.queue.Front()
	if el == nil {
		return "", false
	}
	url := el.Value.(string)
	_ = c.queue.Remove(el)
	delete(c.queued, url)
	return url, true
}

// pruneExcept bounds the queue to the currently interesting set: fast scrolls
// must not strand hundreds of off-screen loads ahead of the visible ones.
func (c *coverManager) pruneExcept(keep map[string]struct{}) {
	if c.queue.Len() == 0 {
		return
	}
	for el := c.queue.Front(); el != nil; {
		next := el.Next()
		u := el.Value.(string)
		if _, ok := keep[u]; !ok {
			_ = c.queue.Remove(el)
			delete(c.queued, u)
		}
		el = next
	}
}

func (c *coverManager) removeFromQueue(url string) bool {
	url = strings.TrimSpace(url)
	el, ok := c.queued[url]
	if !ok {
		return false
	}
	_ = c.queue.Remove(el)
	delete(c.queued, url)
	return true
}

func coverResolveKey(kind, id string) string {
	return strings.TrimSpace(kind) + ":" + strings.TrimSpace(id)
}

func (m *model) queueCoverResolveCmd(kind, id string) tea.Cmd {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return nil
	}
	if !m.ui.cover.queueResolve(kind, id) {
		return nil
	}
	cmd := m.resolveContextImageURLCmd(kind, id)
	if cmd == nil {
		m.ui.cover.clearResolve(kind, id)
		return nil
	}
	return cmd
}

func (m *model) queueMissingLibraryImageResolvesCmd(limit int) tea.Cmd {
	if limit <= 0 || m.ui.cover.sweepPaused() {
		return nil
	}
	items := make([]struct{ Kind, ID string }, 0, limit)
	for _, item := range m.browse.playlistList.Items() {
		if len(items) >= limit {
			break
		}
		pl, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(pl.summary.ImageURL) != "" {
			continue
		}
		if !m.ui.cover.queueResolve(spotify.ContextKindPlaylist, pl.summary.ID) {
			continue
		}
		items = append(items, struct{ Kind, ID string }{Kind: spotify.ContextKindPlaylist, ID: pl.summary.ID})
	}
	for _, item := range m.browse.albumList.Items() {
		if len(items) >= limit {
			break
		}
		al, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(al.summary.ImageURL) != "" {
			continue
		}
		if !m.ui.cover.queueResolve(spotify.ContextKindAlbum, al.summary.ID) {
			continue
		}
		items = append(items, struct{ Kind, ID string }{Kind: spotify.ContextKindAlbum, ID: al.summary.ID})
	}
	cmd := m.resolveContextImageURLsBatchCmd(items)
	if cmd == nil {
		// No loader: unmark the in-flight keys so they can be retried later.
		for _, item := range items {
			m.ui.cover.clearResolve(item.Kind, item.ID)
		}
		return nil
	}
	return cmd
}

func (m *model) queueResolvesForImageURLCmd(url string, limit int) tea.Cmd {
	url = strings.TrimSpace(url)
	if url == "" || limit <= 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, limit)
	for _, item := range m.browse.playlistList.Items() {
		if len(cmds) >= limit {
			break
		}
		pl, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(pl.summary.ImageURL) != url {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindPlaylist, pl.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	for _, item := range m.browse.albumList.Items() {
		if len(cmds) >= limit {
			break
		}
		al, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(al.summary.ImageURL) != url {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindAlbum, al.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) enqueueCoverURL(url string) {
	m.ui.cover.enqueueURL(url)
}

func (m *model) drainCoverQueueCmd(limit int) tea.Cmd {
	if limit <= 0 {
		limit = coverQueueDrainBatch
	}
	urls := make([]string, 0, limit)
	if m.transport.status != nil {
		playerURL := strings.TrimSpace(m.transport.status.AlbumImageURL)
		if playerURL != "" && m.ui.cover.removeFromQueue(playerURL) && m.ui.imgs.shouldQueueLoad(playerURL) {
			urls = append(urls, playerURL)
		}
	}
	for m.ui.cover.queue.Len() > 0 && len(urls) < limit {
		url, _ := m.ui.cover.popURL()
		if !m.ui.imgs.shouldQueueLoad(url) {
			continue
		}
		urls = append(urls, url)
	}
	return m.loadImagesBatchCmd(urls)
}

// sweepPaused reports a server-penalty park: background sweeps stop
// enqueueing until the wait elapses, while priority loads keep flowing.
func (c *coverManager) sweepPaused() bool {
	return time.Now().Before(c.sweepPausedUntil)
}

// pauseSweep parks background sweeps for d: hammering a penalizing server
// is exactly what the non-transient 429 policy forbids.
func (c *coverManager) pauseSweep(d time.Duration) {
	if d <= 0 {
		return
	}
	if until := time.Now().Add(d); until.After(c.sweepPausedUntil) {
		c.sweepPausedUntil = until
	}
}

// promoteURL moves an already-queued URL to the drain head so a rapid skip
// re-prioritizes the new head ahead of stale sweep entries.
func (c *coverManager) promoteURL(url string) {
	url = strings.TrimSpace(url)
	if el, ok := c.queued[url]; ok {
		c.queue.MoveToFront(el)
	}
}

// sweepQueueCoversCmd warms the whole pushed queue head-first so any skip
// swaps from cache, not just the next track. The cover queue dedupes
// (queued map), shouldQueueLoad gates dead URLs behind the fail cooldown,
// and the chained drain in handleImagesBatchLoadedMsg runs the sweep to
// completion across ticks. Head starvation is impossible: the player cover
// loads direct (never via this queue) and head URLs promote to front.
func (m *model) sweepQueueCoversCmd() tea.Cmd {
	if m.ui.imgs == nil || m.ui.cover.sweepPaused() {
		return nil
	}
	q := m.visibleQueue()
	if len(q) == 0 {
		return nil
	}
	current := ""
	if m.transport.status != nil {
		current = strings.TrimSpace(m.transport.status.AlbumImageURL)
	}
	enqueued := 0
	for _, item := range q {
		if enqueued >= queueCoverSweepBatch {
			break
		}
		url := strings.TrimSpace(item.ImageURL)
		if url == "" || url == current {
			continue
		}
		if !m.ui.imgs.shouldQueueLoad(url) {
			continue
		}
		if m.ui.cover.enqueueURL(url) {
			enqueued++
		}
	}
	// Newest head first: rapid skips re-prioritize ahead of stale entries.
	for i := min(len(q), queueHeadPinWindow) - 1; i >= 0; i-- {
		m.ui.cover.promoteURL(q[i].ImageURL)
	}
	return m.drainCoverQueueCmd(coverQueueDrainBatch)
}

// pinQueueHeadCovers pins the current cover plus the head window against
// LRU eviction: a full-context sweep must never push out the art the next
// skip needs. Stale pins release as the window moves.
func (m *model) pinQueueHeadCovers() {
	if m.ui.imgs == nil {
		return
	}
	want := make(map[string]struct{}, queueHeadPinWindow+1)
	if m.transport.status != nil {
		if url := strings.TrimSpace(m.transport.status.AlbumImageURL); url != "" {
			want[url] = struct{}{}
		}
	}
	q := m.visibleQueue()
	for i := range min(len(q), queueHeadPinWindow) {
		if url := strings.TrimSpace(q[i].ImageURL); url != "" {
			want[url] = struct{}{}
		}
	}
	for url := range m.ui.cover.pinnedHead {
		if _, ok := want[url]; !ok {
			m.ui.imgs.unpinURL(url)
			delete(m.ui.cover.pinnedHead, url)
		}
	}
	for url := range want {
		if _, ok := m.ui.cover.pinnedHead[url]; !ok {
			m.ui.imgs.pinURL(url)
			m.ui.cover.pinnedHead[url] = struct{}{}
		}
	}
}

func (m *model) maybeRecoverKittyProtocol() {
	if m.ui.imgs == nil || m.ui.imgs.protocolForRender() == imageProtocolKitty {
		return
	}
	if !m.ui.cover.kittyFellBack {
		return
	}
	// After a healthy streak, give kitty another chance instead of staying
	// in half-block mode for the whole session.
	m.ui.cover.kittyRecoveryStreak++
	if m.ui.cover.kittyRecoveryStreak >= kittyProtocolRecoveryStreak {
		m.ui.imgs.setProtocol(imageProtocolKitty)
		m.ui.cover.kittyRecoveryStreak = 0
		m.ui.cover.kittyFellBack = false
		slog.Info("re-enabling kitty image protocol after recovery streak")
	}
}

func (m *model) maybeFallbackFromKittyOnPlayerFailures(url string) {
	if m.ui.imgs == nil || m.ui.imgs.protocolForRender() != imageProtocolKitty {
		return
	}
	if m.transport.status == nil || strings.TrimSpace(m.transport.status.AlbumImageURL) == "" {
		return
	}
	if strings.TrimSpace(m.transport.status.AlbumImageURL) != strings.TrimSpace(url) {
		return
	}
	if m.ui.cover.playerCoverFailStreak < kittyProtocolFallbackFailures {
		return
	}
	m.ui.imgs.setProtocol(imageProtocolNone)
	m.ui.cover.playerCoverFailStreak = 0
	m.ui.cover.kittyRecoveryStreak = 0
	m.ui.cover.kittyFellBack = true
	slog.Warn("disabling kitty image protocol after repeated player cover failures", "url", url)
}

func (m *model) applyResolvedContextImageURL(kind, id, imageURL string) bool {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	imageURL = strings.TrimSpace(imageURL)
	if kind == "" || id == "" || imageURL == "" {
		return false
	}
	updated := false
	switch kind {
	case spotify.ContextKindPlaylist:
		items := m.browse.playlistList.Items()
		prevIndex := m.browse.playlistList.GlobalIndex()
		for i, item := range items {
			pl, ok := item.(playlistItem)
			if !ok || pl.summary.ID != id {
				continue
			}
			if strings.TrimSpace(pl.summary.ImageURL) == imageURL {
				return false
			}
			pl.summary.ImageURL = imageURL
			items[i] = pl
			updated = true
			break
		}
		if updated {
			if m.browse.playlistList.FilterState() == list.Unfiltered {
				m.browse.playlistList.SetItems(items)
				if len(items) > 0 {
					m.browse.playlistList.Select(min(max(prevIndex, 0), len(items)-1))
				}
			}
		}
	case spotify.ContextKindAlbum:
		items := m.browse.albumList.Items()
		prevIndex := m.browse.albumList.GlobalIndex()
		for i, item := range items {
			al, ok := item.(playlistItem)
			if !ok || al.summary.ID != id {
				continue
			}
			if strings.TrimSpace(al.summary.ImageURL) == imageURL {
				return false
			}
			al.summary.ImageURL = imageURL
			items[i] = al
			updated = true
			break
		}
		if updated {
			if m.browse.albumList.FilterState() == list.Unfiltered {
				m.browse.albumList.SetItems(items)
				if len(items) > 0 {
					m.browse.albumList.Select(min(max(prevIndex, 0), len(items)-1))
				}
			}
		}
	}
	return updated
}
