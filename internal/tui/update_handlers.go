package tui

import (
	"context"
	"log/slog"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func (m model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	m.imgs.invalidateCovers()

	leftW, _ := m.splitWidths()
	listInnerW := leftW - 3
	listInnerH := m.height - chromeH - 4

	m.playlistList.SetSize(listInnerW, listInnerH)
	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleTickMsg() (tea.Model, tea.Cmd) {
	m.interpolatePlaybackProgress(uiTickInterval)
	if m.tuiCmdCh != nil {
		return m, m.tickCmd()
	}
	if m.screen != screenPlayback {
		return m, m.tickCmd()
	}
	interval := m.pollInterval
	if interval <= 0 {
		interval = uiTickInterval
	}
	if !m.actionFastPollUntil.IsZero() && time.Now().Before(m.actionFastPollUntil) {
		interval = uiTickInterval
	} else if m.status == nil || !m.status.Playing {
		interval = min(interval*2, idlePollBackoffMax)
	}
	m.pollElapsed += uiTickInterval
	if m.pollElapsed < interval {
		return m, m.tickCmd()
	}
	m.pollElapsed = 0
	m.pollTick++
	pollQueue := m.pollTick%queuePollEvery == 0
	return m, tea.Batch(m.pollCmd(pollQueue), m.tickCmd())
}

func (m model) handlePlaylistsMsg(msg playlistsMsg) (tea.Model, tea.Cmd) {
	m.playlistsLoading = false
	if msg.err != nil {
		m.playlistsErr = msg.err
		slog.Error("fetch playlists failed", "error", msg.err)
		if spotify.IsTransientAPIError(msg.err) && !spotify.IsRateLimitError(msg.err) && m.playlistsRetryCount < 2 {
			m.playlistsRetryCount++
			m.playlistsLoading = true
			return m, m.loadPlaylistsCmd(msg.offset, msg.limit)
		}
		return m, nil
	}
	m.playlistsErr = nil
	m.playlistsRetryCount = 0
	if msg.offset == 0 {
		m.albumsForbidden = msg.albumsForbidden
	} else {
		m.albumsForbidden = m.albumsForbidden || msg.albumsForbidden
	}
	prevIndex := m.playlistList.GlobalIndex()
	items := m.playlistList.Items()
	if msg.offset == 0 {
		items = make([]list.Item, 0, len(msg.items))
	} else {
		items = append([]list.Item(nil), items...)
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		key := pl.summary.Kind + ":" + pl.summary.ID
		seen[key] = struct{}{}
	}
	for _, pl := range msg.items {
		key := pl.Kind + ":" + pl.ID
		if _, exists := seen[key]; exists {
			continue
		}
		items = append(items, playlistItem{summary: pl})
		seen[key] = struct{}{}
	}
	m.playlistList.SetItems(items)
	m.modalList.SetItems(items)
	if len(items) > 0 {
		idx := clampInt(prevIndex, 0, len(items)-1)
		m.playlistList.Select(idx)
	}
	if len(items) >= playlistLoadMax || len(msg.items) == 0 || !msg.hasMore {
		m.playlistsExhausted = true
	}
	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleCurrentUserIDMsg(msg currentUserIDMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil && msg.userID != "" {
		m.currentUserID = msg.userID
		if m.shouldLoadPlaylistTracks() && m.activePlaylistID != "" && !m.activePlaylistTrackLoading &&
			(m.activePlaylistOwnerID == msg.userID || m.activePlaylistCollaborative) {
			m.activePlaylistTrackHasMore = true
			m.activePlaylistTrackLoading = true
			m.activePlaylistLoadToken++
			return m, m.loadPlaylistTracksCmd(m.activePlaylistID, 0, m.activePlaylistLoadToken)
		}
	}
	return m, m.loadPlaylistsCmd(0, playlistLoadBatchSize)
}

func (m model) handlePlaylistTracksMsg(msg playlistTracksMsg) (tea.Model, tea.Cmd) {
	if msg.playlistID == "" || msg.playlistID != m.activePlaylistID || msg.token != m.activePlaylistLoadToken {
		return m, nil
	}
	m.activePlaylistTrackLoading = false
	if msg.err != nil {
		m.activePlaylistTrackHasMore = false
		if !m.shouldLoadPlaylistTracks() || spotify.IsForbidden(msg.err) {
			slog.Warn("optional playlist-track fetch skipped", "playlist_id", msg.playlistID, "error", msg.err)
			return m, nil
		}
		m.playbackErr = msg.err
		slog.Error("fetch playlist tracks failed", "playlist_id", msg.playlistID, "error", msg.err)
		if spotify.IsTransientAPIError(msg.err) && !spotify.IsRateLimitError(msg.err) && m.playlistTrackRetryCount < 2 {
			m.playlistTrackRetryCount++
			m.activePlaylistTrackLoading = true
			return m, m.loadPlaylistTracksCmd(msg.playlistID, m.activePlaylistTrackNextOffset, m.activePlaylistLoadToken)
		}
		return m, nil
	}
	m.playlistTrackRetryCount = 0
	seen := make(map[string]struct{}, len(m.activePlaylistTrackIDs)+len(msg.trackIDs))
	for _, trackID := range m.activePlaylistTrackIDs {
		if trackID == "" {
			continue
		}
		seen[trackID] = struct{}{}
	}
	for i, trackID := range msg.trackIDs {
		if trackID == "" {
			continue
		}
		if _, exists := seen[trackID]; exists {
			continue
		}
		seen[trackID] = struct{}{}
		m.activePlaylistTrackIDs = append(m.activePlaylistTrackIDs, trackID)
		if i < len(msg.trackInfos) {
			if info := msg.trackInfos[i]; info.Name != "" {
				m.trackCache[trackID] = info
			}
		}
	}
	m.activePlaylistTrackNextOffset = msg.nextOffset
	m.activePlaylistTrackHasMore = msg.hasMore
	cmds := make([]tea.Cmd, 0, 2)
	if cmd := m.maybeLoadMorePlaylistTracksCmd(playlistTrackPreloadMax); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleNavDebounceMsg(msg navDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.navToken {
		return m, nil
	}
	if m.modal {
		return m, m.maybeLoadMorePlaylistsCmd(m.modalList)
	}
	if m.screen != screenPlaylist {
		return m, nil
	}
	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleImageLoadedMsg(msg imageLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.url == "" {
		return m, nil
	}
	if msg.err == nil {
		delete(m.imageRetryCount, msg.url)
		delete(m.imageRetryToken, msg.url)
		return m, nil
	}

	attempt := m.imageRetryCount[msg.url] + 1
	if attempt > imageLoadRetryMax {
		delete(m.imageRetryCount, msg.url)
		delete(m.imageRetryToken, msg.url)
		slog.Warn("image load retries exhausted", "url", msg.url, "error", msg.err)
		return m, nil
	}
	m.imageRetryCount[msg.url] = attempt
	m.imageRetryToken[msg.url]++
	token := m.imageRetryToken[msg.url]
	return m, m.imageRetryCmd(msg.url, attempt, token)
}

func (m model) handleImageRetryMsg(msg imageRetryMsg) (tea.Model, tea.Cmd) {
	if msg.url == "" {
		return m, nil
	}
	if current := m.imageRetryToken[msg.url]; current != msg.token {
		return m, nil
	}
	if !m.needsImageURL(msg.url) {
		delete(m.imageRetryCount, msg.url)
		delete(m.imageRetryToken, msg.url)
		return m, nil
	}
	return m, m.loadImageCmd(msg.url)
}

func (m model) handleActionMsg(msg actionMsg) (tea.Model, tea.Cmd) {
	m.actionInFlight = false
	if msg.err != nil {
		m.playbackErr = msg.err
		slog.Error("playback action failed", "error", msg.err)
		if msg.rollback != nil {
			m.status = msg.rollback
		}
		if msg.reconcile {
			return m, m.pollCmd(true)
		}
		return m, nil
	}
	m.playbackErr = nil
	switch msg.action {
	case "play-from-browser":
		m.screen = screenPlayback
	case "play-from-modal":
		m.modal = false
		m.modalKind = modalKindNone
	}
	return m, m.pollCmd(true)
}

func (m model) handleVolDebounceMsg(msg volDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.volDebounceToken || m.volDebouncePending < 0 {
		return m, nil
	}
	target := m.volDebouncePending
	m.volDebouncePending = -1
	m.volSentTarget = target
	m.volSentAt = time.Now()
	m.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
	if m.tuiCmdCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSetVolume, Volume: target}:
		default:
		}
		return m, nil
	}
	rollback := cloneStatus(m.status)
	if m.status != nil {
		m.status.Volume = target
	}
	m.beginReconcileAction(0)
	v := target
	return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
		return m.service.SetVolume(ctx, m.deviceName, v)
	}, rollback)
}

func (m model) handleSeekDebounceMsg(msg seekDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.seekDebounceToken || m.seekDebouncePending < 0 {
		return m, nil
	}
	target := m.clampSeekTarget(m.seekDebouncePending)
	m.seekDebouncePending = -1
	m.seekSentTarget = target
	m.seekSentAt = time.Now()
	if m.status != nil {
		m.status.ProgressMS = target
	}
	if m.tuiCmdCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSeek, Position: int64(target)}:
		default:
			m.seekDebouncePending = target
			m.seekDebounceToken++
			return m, m.seekDebounceCmd(m.seekDebounceToken)
		}
		return m, nil
	}
	rollback := cloneStatus(m.status)
	if m.status != nil {
		m.status.ProgressMS = target
	}
	m.beginReconcileAction(0)
	p := target
	return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
		return m.service.Seek(ctx, m.deviceName, p)
	}, rollback)
}

func (m model) handleFilterMatchesMsg(msg list.FilterMatchesMsg) (tea.Model, tea.Cmd) {
	if m.modal {
		var cmd tea.Cmd
		m.modalList, cmd = m.modalList.Update(msg)
		return m, cmd
	}
	if m.screen == screenPlaylist {
		var cmd tea.Cmd
		m.playlistList, cmd = m.playlistList.Update(msg)
		return m, cmd
	}
	return m, nil
}
