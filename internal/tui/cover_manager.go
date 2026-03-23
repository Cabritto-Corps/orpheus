package tui

import (
	"log/slog"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/spotify"
)

type coverManager struct {
	imageRetryCount       map[string]int
	imageRetryToken       map[string]int
	resolveInFlight       map[string]struct{}
	queue                 []string
	queued                map[string]struct{}
	playerCoverFailStreak int
}

func newCoverManager() coverManager {
	return coverManager{
		imageRetryCount: make(map[string]int),
		imageRetryToken: make(map[string]int),
		resolveInFlight: make(map[string]struct{}),
		queued:          make(map[string]struct{}),
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
	c.queued[url] = struct{}{}
	c.queue = append(c.queue, url)
	return true
}

func (c *coverManager) popURL() (string, bool) {
	if len(c.queue) == 0 {
		return "", false
	}
	url := c.queue[0]
	c.queue = c.queue[1:]
	delete(c.queued, url)
	return url, true
}

func (c *coverManager) removeFromQueue(url string) bool {
	url = strings.TrimSpace(url)
	if _, ok := c.queued[url]; !ok {
		return false
	}
	for i, u := range c.queue {
		if strings.TrimSpace(u) == url {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			delete(c.queued, url)
			return true
		}
	}
	return false
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
	if !m.cover.queueResolve(kind, id) {
		return nil
	}
	cmd := m.resolveContextImageURLCmd(kind, id)
	if cmd == nil {
		m.cover.clearResolve(kind, id)
		return nil
	}
	return cmd
}

func (m *model) queueMissingLibraryImageResolvesCmd(limit int) tea.Cmd {
	if limit <= 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, limit)
	for _, item := range m.playlistList.Items() {
		if len(cmds) >= limit {
			break
		}
		pl, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(pl.summary.ImageURL) != "" {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindPlaylist, pl.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	for _, item := range m.albumList.Items() {
		if len(cmds) >= limit {
			break
		}
		al, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(al.summary.ImageURL) != "" {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindAlbum, al.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) queueResolvesForImageURLCmd(url string, limit int) tea.Cmd {
	url = strings.TrimSpace(url)
	if url == "" || limit <= 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, limit)
	for _, item := range m.playlistList.Items() {
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
	for _, item := range m.albumList.Items() {
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
	m.cover.enqueueURL(url)
}

func (m *model) drainCoverQueueCmd(limit int) tea.Cmd {
	if limit <= 0 {
		limit = coverQueueDrainBatch
	}
	cmds := make([]tea.Cmd, 0, limit)
	if m.status != nil {
		playerURL := strings.TrimSpace(m.status.AlbumImageURL)
		if playerURL != "" && m.cover.removeFromQueue(playerURL) && m.imgs.shouldQueueLoad(playerURL) {
			if cmd := m.loadImageCmd(playerURL, true); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	for len(m.cover.queue) > 0 && len(cmds) < limit {
		url, _ := m.cover.popURL()
		if !m.imgs.shouldQueueLoad(url) {
			continue
		}
		cmd := m.loadImageCmd(url, false)
		if cmd == nil {
			continue
		}
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *model) maybeFallbackFromKittyOnPlayerFailures(url string) {
	if m.imgs == nil || m.imgs.protocol != imageProtocolKitty {
		return
	}
	if m.status == nil || strings.TrimSpace(m.status.AlbumImageURL) == "" {
		return
	}
	if strings.TrimSpace(m.status.AlbumImageURL) != strings.TrimSpace(url) {
		return
	}
	if m.cover.playerCoverFailStreak < kittyProtocolFallbackFailures {
		return
	}
	m.imgs.protocol = imageProtocolNone
	m.cover.playerCoverFailStreak = 0
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
		items := m.playlistList.Items()
		prevIndex := m.playlistList.GlobalIndex()
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
			if m.playlistList.FilterState() == list.Unfiltered {
				m.playlistList.SetItems(items)
				if len(items) > 0 {
					m.playlistList.Select(clampInt(prevIndex, 0, len(items)-1))
				}
			}
		}
	case spotify.ContextKindAlbum:
		items := m.albumList.Items()
		prevIndex := m.albumList.GlobalIndex()
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
			if m.albumList.FilterState() == list.Unfiltered {
				m.albumList.SetItems(items)
				if len(items) > 0 {
					m.albumList.Select(clampInt(prevIndex, 0, len(items)-1))
				}
			}
		}
	}
	return updated
}
