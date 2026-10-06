package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

const searchMinQueryLength = 2

func (m model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.browse.search
	k := m.ui.keys
	if s.input.Focused() {
		switch {
		case keyMatches(msg, k.Tab):
			s.input.Blur()
			m.ui.activeTab = tabPlayer
			return m, tea.Batch(m.loadVisiblePlaylistCoversCmd(), m.kittyOverlayCmd())
		case keyMatches(msg, k.CloseModal):
			s.input.Blur()
			return m, nil
		case keyMatches(msg, k.Select):
			if item, ok := s.list.SelectedItem().(searchResultItem); ok {
				return m.selectSearchResult(item.result)
			}
			return m, nil
		case msg.Code == tea.KeyUp || keyMatches(msg, k.QueueUp), msg.Code == tea.KeyDown || keyMatches(msg, k.QueueDown):
			if msg.Code == tea.KeyUp || keyMatches(msg, k.QueueUp) {
				return m.moveSearchSelection(-1)
			}
			return m.moveSearchSelection(1)
		case msg.Code == tea.KeyPgUp, msg.Code == tea.KeyPgDown:
			perPage := s.list.Paginator.PerPage
			if perPage <= 0 {
				perPage = 10
			}
			if msg.Code == tea.KeyPgUp {
				return m.pageSearchSelection(-perPage)
			}
			return m.pageSearchSelection(perPage)
		}
		oldQuery := s.input.Value()
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		if query := strings.TrimSpace(s.input.Value()); query != oldQuery {
			next, searchCmd := m.setSearchQuery(query)
			return next, tea.Batch(cmd, searchCmd)
		}
		return m, cmd
	}

	switch {
	case keyMatches(msg, k.Filter):
		return m, s.input.Focus()
	case keyMatches(msg, k.CloseModal):
		s.input.Blur()
		return m, nil
	case keyMatches(msg, k.Refresh):
		if s.query != "" {
			s.requestID++
			s.offset = 0
			s.loading = true
			s.err = nil
			return m, m.searchCmd(s.requestID, s.query, 0)
		}
	case keyMatches(msg, k.Select):
		item, ok := s.list.SelectedItem().(searchResultItem)
		if ok {
			return m.selectSearchResult(item.result)
		}
	}
	return m.updateSearchList(msg)
}

func (m model) updateSearchList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyUp || keyMatches(msg, m.ui.keys.QueueUp) {
		return m.moveSearchSelection(-1)
	}
	if msg.Code == tea.KeyDown || keyMatches(msg, m.ui.keys.QueueDown) {
		return m.moveSearchSelection(1)
	}
	s := &m.browse.search
	previous := selectedSearchImageURL(s.list)
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	current := selectedSearchImageURL(s.list)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd(), m.kittyOverlayCmd()}
	if current != "" && current != previous {
		cmds = append(cmds, m.loadImageCmd(current, true))
	}
	return m, tea.Batch(cmds...)
}

func (m model) moveSearchSelection(delta int) (tea.Model, tea.Cmd) {
	s := &m.browse.search
	items := s.list.Items()
	if len(items) == 0 {
		return m, nil
	}
	index := max(s.list.Index()+delta, 0)
	if index >= len(items) {
		index = len(items) - 1
	}
	s.list.Select(index)
	return m, tea.Batch(
		m.loadImageCmd(selectedSearchImageURL(s.list), true),
		m.scheduleNavDebounceCmd(),
		m.kittyOverlayCmd(),
	)
}

func (m model) pageSearchSelection(delta int) (tea.Model, tea.Cmd) {
	next, cmd := m.moveSearchSelection(delta)
	moved := next.(model)
	moved, more := moved.loadMoreSearchIfNeeded()
	return moved, tea.Batch(cmd, more)
}

func (m model) setSearchQuery(query string) (model, tea.Cmd) {
	s := &m.browse.search
	s.query = query
	s.requestID++
	s.offset = 0
	s.hasMore = false
	s.err = nil
	if len([]rune(query)) < searchMinQueryLength {
		s.loading = false
		s.list.SetItems([]list.Item{})
		return m, m.kittyOverlayCmd()
	}
	s.loading = true
	return m, m.searchDebounceCmd(s.requestID, query)
}

func (m model) handleSearchDebounceMsg(msg searchDebounceMsg) (tea.Model, tea.Cmd) {
	s := &m.browse.search
	if msg.token != s.requestID || msg.query != s.query || msg.query == "" {
		return m, nil
	}
	return m, m.searchCmd(msg.token, msg.query, 0)
}

func (m model) handleSearchResultsMsg(msg searchResultsMsg) (tea.Model, tea.Cmd) {
	s := &m.browse.search
	if msg.token != s.requestID || msg.query != s.query {
		return m, nil
	}
	s.loading = false
	if msg.err != nil {
		s.err = msg.err
		return m, nil
	}
	if msg.page == nil {
		s.err = nil
		return m, nil
	}
	s.err = nil
	s.hasMore = msg.page.HasMore
	s.offset = msg.page.NextOffset
	selectedIndex := s.list.Index()
	items := make([]list.Item, 0, len(s.list.Items())+len(msg.page.Items))
	if msg.offset > 0 {
		items = append(items, s.list.Items()...)
	}
	for _, result := range msg.page.Items {
		items = append(items, searchResultItem{result: result})
	}
	s.list.SetItems(items)
	imageURLs := make([]string, 0, len(msg.page.Items))
	for _, result := range msg.page.Items {
		if url := strings.TrimSpace(result.ImageURL); url != "" {
			imageURLs = append(imageURLs, url)
		}
	}
	if msg.offset == 0 && len(items) > 0 {
		s.list.Select(0)
	} else if len(items) > 0 {
		s.list.Select(min(max(selectedIndex, 0), len(items)-1))
	}
	return m, m.loadImagesBatchCmd(imageURLs)
}

func (m model) loadMoreSearchIfNeeded() (model, tea.Cmd) {
	s := m.browse.search
	items := s.list.Items()
	if m.ui.activeTab != tabSearch || s.loading || !s.hasMore || s.query == "" || len(items) == 0 {
		return m, nil
	}
	if s.list.Index() < len(items)-5 {
		return m, nil
	}
	m.browse.search.loading = true
	return m, m.searchCmd(s.requestID, s.query, s.offset)
}

func (m model) selectSearchResult(result spotify.SearchResultItem) (tea.Model, tea.Cmd) {
	if result.Kind == "artist" {
		return m.openArtistChoice(result)
	}
	if result.Kind == "album" {
		return m.selectAndPlayPlaylist(playlistItem{summary: spotify.PlaylistSummary{
			ID: result.ID, Name: result.Name, URI: result.URI,
			Kind: spotify.ContextKindAlbum, Owner: result.Owner,
			TrackCount: result.TrackCount, ImageURL: result.ImageURL,
		}})
	}
	next, cmd := m.playSingleTrack(result.URI, result.ImageURL, []librespot.PlaybackStateQueueEntry{
		{ID: result.ID, Name: result.Name, Artist: result.Owner, DurationMS: result.DurationMS, ImageURL: result.ImageURL},
	})
	played := next.(model)
	// Keep the search results available after playing a song. Arrows browse;
	// Enter plays another result. Album contexts and artist stations open Player.
	played.ui.activeTab = tabSearch
	return played, tea.Batch(cmd, played.kittyOverlayCmd())
}

func (m model) playArtistStation(result spotify.SearchResultItem) (tea.Model, tea.Cmd) {
	m.ui.activeTab = tabPlayer
	m.transport.playbackErr = nil
	m.freezeSessionTrack(m.transport.status)
	if m.transport.status != nil {
		m.transport.pendingContextFrom = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
		m.transport.pendingContextFromAt = time.Now()
	}
	m.transport.queue = nil
	m.transport.queueHasMore = false
	m.transport.stableQueueLen = 0
	if m.transport.status != nil {
		m.transport.status.ProgressMS = 0
		m.transport.status.DurationMS = 0
	}
	m.transport.interpolationSyncAt = time.Time{}
	m.transport.interpolationProgressMS = 0
	m.beginTransportTransition()
	cmd := librespot.TUICommand{Kind: librespot.TUICommandPlayStation, URI: result.URI}
	return m, tea.Batch(m.sendTUICommandOrRetry(cmd), m.loadImageCmd(result.ImageURL, true))
}

func (m model) playSingleTrack(uri, imageURL string, seeds []librespot.PlaybackStateQueueEntry) (tea.Model, tea.Cmd) {
	m.ui.activeTab = tabPlayer
	m.transport.playbackErr = nil
	m.freezeSessionTrack(m.transport.status)
	if m.transport.status != nil {
		m.transport.pendingContextFrom = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
		m.transport.pendingContextFromAt = time.Now()
		m.transport.status.ProgressMS = 0
		m.transport.status.DurationMS = 0
	}
	m.transport.queue = nil
	m.transport.queueHasMore = false
	m.transport.stableQueueLen = 0
	m.transport.interpolationSyncAt = time.Time{}
	m.transport.interpolationProgressMS = 0
	m.beginTransportTransition()
	cmd := librespot.TUICommand{Kind: librespot.TUICommandPlayTrack, URI: uri, Seed: seeds}
	return m, tea.Batch(m.sendTUICommandOrRetry(cmd), m.loadImageCmd(imageURL, true))
}

func selectedSearchImageURL(l list.Model) string {
	item, ok := l.SelectedItem().(searchResultItem)
	if !ok {
		return ""
	}
	return item.result.ImageURL
}
