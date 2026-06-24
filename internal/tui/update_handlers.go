package tui

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func (m model) handleWindowSizeMsg(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.ui.width = msg.Width
	m.ui.height = msg.Height

	m.ui.imgs.invalidateCovers()
	m.ui.cachedBodyLayoutValid = false

	layout := m.getBodyLayout()
	listInnerW := layout.rightW - 1
	listInnerH := layout.bodyH - 4

	m.browse.playlistList.SetSize(listInnerW, listInnerH)
	m.browse.albumList.SetSize(listInnerW, listInnerH)
	m.normalizeLibraryPagination()

	if m.ui.trackPopupOpen {
		modalW := min(m.ui.width-8, 60)
		popupBodyH := m.ui.height - headerH - tabBarH - 2
		popupInnerH := max(popupBodyH-4, 10)
		m.ui.trackPopupList.SetSize(modalW-2, popupInnerH-4)
		m.ui.trackPopupWidth = modalW - 4
	}

	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.maybeLoadMorePlaylistsCmd(m.browse.playlistList),
	)
}

func (m model) handleTickMsg() (tea.Model, tea.Cmd) {
	m.interpolatePlaybackProgress(uiTickInterval)
	inputCmd := m.pumpInputExecutor()
	var startupCoverCmd tea.Cmd
	if m.ui.startupCoverBoostTicks > 0 {
		m.ui.startupCoverBoostTicks--
		startupCoverCmd = m.drainCoverQueueCmd(coverQueueDrainBatch * 8)
	}

	m.ui.coverRefreshTick++
	m.ui.playerCoverRefreshTick++
	m.ui.libraryCoverRefreshTick++
	m.ui.libraryMetaRefreshTick++
	var coverCmd tea.Cmd
	var playerCoverCmd tea.Cmd
	var libraryCoverCmd tea.Cmd
	var metadataCmd tea.Cmd
	if m.ui.activeTab != tabPlayer && m.ui.coverRefreshTick >= coverRefreshEvery {
		m.ui.coverRefreshTick = 0
		coverCmd = m.loadVisiblePlaylistCoversCmd()
	}
	if m.ui.activeTab == tabPlayer && m.transport.status != nil {
		url := strings.TrimSpace(m.transport.status.AlbumImageURL)
		if url != "" && m.ui.imgs.shouldQueuePriorityLoad(url) {
			playerCoverCmd = m.loadImageCmd(url, true)
			m.ui.playerCoverRefreshTick = 0
		} else if m.ui.playerCoverRefreshTick >= playerCoverRefreshEvery {
			m.ui.playerCoverRefreshTick = 0
			playerCoverCmd = m.loadImageCmd(url, true)
		}
	}
	if m.ui.libraryCoverRefreshTick >= libraryCoverRefreshEvery {
		m.ui.libraryCoverRefreshTick = 0
		libraryCoverCmd = m.loadLibraryCoversCmd(libraryCoverRefreshBatch)
	}
	if m.ui.libraryMetaRefreshTick >= libraryMetaRefreshEvery {
		m.ui.libraryMetaRefreshTick = 0
		if m.hasMissingLibraryImageURLs() {
			metadataCmd = m.queueMissingLibraryImageResolvesCmd(libraryCoverRefreshBatch)
		}
	}

	if m.tuiCmdCh != nil {
		cmds := make([]tea.Cmd, 0, 6)
		cmds = append(cmds, m.tickCmd(), inputCmd)
		if startupCoverCmd != nil {
			cmds = append(cmds, startupCoverCmd)
		}
		if coverCmd != nil {
			cmds = append(cmds, coverCmd)
		}
		if playerCoverCmd != nil {
			cmds = append(cmds, playerCoverCmd)
		}
		if libraryCoverCmd != nil {
			cmds = append(cmds, libraryCoverCmd)
		}
		if metadataCmd != nil {
			cmds = append(cmds, metadataCmd)
		}
		return m, tea.Batch(cmds...)
	}
	if m.ui.activeTab != tabPlayer {
		cmds := make([]tea.Cmd, 0, 6)
		cmds = append(cmds, m.tickCmd(), inputCmd)
		if startupCoverCmd != nil {
			cmds = append(cmds, startupCoverCmd)
		}
		if coverCmd != nil {
			cmds = append(cmds, coverCmd)
		}
		if playerCoverCmd != nil {
			cmds = append(cmds, playerCoverCmd)
		}
		if libraryCoverCmd != nil {
			cmds = append(cmds, libraryCoverCmd)
		}
		if metadataCmd != nil {
			cmds = append(cmds, metadataCmd)
		}
		return m, tea.Batch(cmds...)
	}
	interval := m.ui.pollInterval
	if interval <= 0 {
		interval = uiTickInterval
	}
	if !m.ui.actionFastPollUntil.IsZero() && time.Now().Before(m.ui.actionFastPollUntil) {
		interval = uiTickInterval
	} else if m.transport.status == nil || !m.transport.status.Playing {
		interval = min(interval*2, idlePollBackoffMax)
	}
	if time.Since(m.ui.lastPollTime) < interval {
		cmds := make([]tea.Cmd, 0, 5)
		cmds = append(cmds, m.tickCmd(), inputCmd)
		if startupCoverCmd != nil {
			cmds = append(cmds, startupCoverCmd)
		}
		if playerCoverCmd != nil {
			cmds = append(cmds, playerCoverCmd)
		}
		if libraryCoverCmd != nil {
			cmds = append(cmds, libraryCoverCmd)
		}
		if metadataCmd != nil {
			cmds = append(cmds, metadataCmd)
		}
		return m, tea.Batch(cmds...)
	}
	m.ui.lastPollTime = time.Now()
	m.ui.pollTick++
	pollQueue := m.ui.pollTick%queuePollEvery == 0
	cmds := make([]tea.Cmd, 0, 6)
	cmds = append(cmds, m.pollCmd(pollQueue), m.tickCmd(), inputCmd)
	if startupCoverCmd != nil {
		cmds = append(cmds, startupCoverCmd)
	}
	if playerCoverCmd != nil {
		cmds = append(cmds, playerCoverCmd)
	}
	if libraryCoverCmd != nil {
		cmds = append(cmds, libraryCoverCmd)
	}
	if metadataCmd != nil {
		cmds = append(cmds, metadataCmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handlePlaylistsMsg(msg playlistsMsg) (tea.Model, tea.Cmd) {
	m.browse.playlistsLoading = false
	if msg.err != nil {
		m.browse.playlistsErr = msg.err
		slog.Error("fetch playlists failed", "error", msg.err)
		if spotify.IsTransientAPIError(msg.err) && !spotify.IsRateLimitError(msg.err) && m.browse.playlistsRetryCount < 2 {
			m.browse.playlistsRetryCount++
			m.browse.playlistsLoading = true
			return m, m.loadPlaylistsCmd(msg.offset, msg.limit)
		}
		return m, nil
	}
	m.browse.playlistsErr = nil
	m.browse.playlistsRetryCount = 0
	if msg.offset == 0 {
		m.browse.albumsForbidden = msg.albumsForbidden
	} else {
		m.browse.albumsForbidden = m.browse.albumsForbidden || msg.albumsForbidden
	}

	prevPlaylistIndex := m.browse.playlistList.GlobalIndex()
	prevAlbumIndex := m.browse.albumList.GlobalIndex()

	plItems := m.browse.playlistList.Items()
	alItems := m.browse.albumList.Items()
	if msg.offset == 0 {
		plItems = make([]list.Item, 0, len(msg.items)+1)
		plItems = append(plItems, playlistItem{summary: spotify.PlaylistSummary{
			ID:       "liked-songs",
			Name:     "Liked Songs",
			URI:      "spotify:collection",
			Kind:     spotify.ContextKindLikedSongs,
			Owner:    "You",
			ImageURL: likedSongsImageURL,
		}})
		alItems = make([]list.Item, 0, len(msg.items))
	} else {
		plItems = append([]list.Item(nil), plItems...)
		alItems = append([]list.Item(nil), alItems...)
	}

	seenPl := make(map[string]struct{}, len(plItems))
	for _, item := range plItems {
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		if pl.summary.Kind == spotify.ContextKindLikedSongs {
			continue
		}
		seenPl[pl.summary.ID] = struct{}{}
	}
	seenAl := make(map[string]struct{}, len(alItems))
	for _, item := range alItems {
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		seenAl[pl.summary.ID] = struct{}{}
	}

	missingImageURLs := 0
	for _, pl := range msg.items {
		item := playlistItem{summary: pl}
		if pl.Kind == spotify.ContextKindAlbum {
			if _, exists := seenAl[pl.ID]; !exists {
				alItems = append(alItems, item)
				seenAl[pl.ID] = struct{}{}
			}
		} else {
			if _, exists := seenPl[pl.ID]; !exists {
				plItems = append(plItems, item)
				seenPl[pl.ID] = struct{}{}
			}
		}
	}

	if msg.offset == 0 {
		for _, item := range plItems {
			pl, ok := item.(playlistItem)
			if ok && strings.TrimSpace(pl.summary.ImageURL) == "" {
				missingImageURLs++
			}
		}
		for _, item := range alItems {
			al, ok := item.(playlistItem)
			if ok && strings.TrimSpace(al.summary.ImageURL) == "" {
				missingImageURLs++
			}
		}
	}

	if m.browse.playlistList.FilterState() == list.Unfiltered {
		m.browse.playlistList.SetItems(plItems)
		if len(plItems) > 0 {
			idx := clampInt(prevPlaylistIndex, 0, len(plItems)-1)
			m.browse.playlistList.Select(idx)
		}
	}
	if m.browse.albumList.FilterState() == list.Unfiltered {
		m.browse.albumList.SetItems(alItems)
		if len(alItems) > 0 {
			idx := clampInt(prevAlbumIndex, 0, len(alItems)-1)
			m.browse.albumList.Select(idx)
		}
	}
	playlistPreviewURL := selectedImageURLFromList(m.browse.playlistList)
	if playlistPreviewURL == "" && len(plItems) > 0 {
		if first, ok := plItems[0].(playlistItem); ok {
			playlistPreviewURL = strings.TrimSpace(first.summary.ImageURL)
		}
	}
	albumPreviewURL := selectedImageURLFromList(m.browse.albumList)
	if albumPreviewURL == "" && len(alItems) > 0 {
		if first, ok := alItems[0].(playlistItem); ok {
			albumPreviewURL = strings.TrimSpace(first.summary.ImageURL)
		}
	}

	if len(msg.items) == 0 || !msg.hasMore {
		m.browse.playlistsExhausted = true
	}
	slog.Info("library items loaded", "playlists", len(plItems), "albums", len(alItems), "missing_image_urls", missingImageURLs)
	return m, tea.Batch(
		m.loadImageCmd(playlistPreviewURL, true),
		m.loadImageCmd(albumPreviewURL, true),
		m.loadLibraryCoversCmd(len(plItems)+len(alItems)),
		m.queueMissingLibraryImageResolvesCmd(missingImageURLs),
		m.maybeLoadMorePlaylistsCmd(m.browse.playlistList),
	)
}

func (m model) handleCurrentUserIDMsg(msg currentUserIDMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil && msg.userID != "" {
		m.browse.currentUserID = msg.userID
		if m.shouldLoadPlaylistItems() && m.browse.activePlaylistID != "" && !m.browse.activePlaylistItemLoading &&
			(m.browse.activePlaylistOwnerID == msg.userID || m.browse.activePlaylistCollaborative) {
			m.browse.activePlaylistItemHasMore = true
			m.browse.activePlaylistItemLoading = true
			m.browse.activePlaylistLoadToken++
			return m, m.loadPlaylistItemsCmd(m.browse.activePlaylistID, 0, m.browse.activePlaylistLoadToken)
		}
	}
	return m, m.loadPlaylistsCmd(0, playlistLoadBatchSize)
}

func (m model) handlePlaylistItemsMsg(msg playlistItemsMsg) (tea.Model, tea.Cmd) {
	if msg.playlistID == "" || msg.playlistID != m.browse.activePlaylistID || msg.token != m.browse.activePlaylistLoadToken {
		return m, nil
	}
	m.browse.activePlaylistItemLoading = false
	if msg.err != nil {
		m.browse.activePlaylistItemHasMore = false
		if !m.shouldLoadPlaylistItems() || spotify.IsForbidden(msg.err) {
			slog.Warn("optional playlist-track fetch skipped", "playlist_id", msg.playlistID, "error", msg.err)
			return m, nil
		}
		m.transport.playbackErr = msg.err
		slog.Error("fetch playlist items failed", "playlist_id", msg.playlistID, "error", msg.err)
		if spotify.IsTransientAPIError(msg.err) && !spotify.IsRateLimitError(msg.err) && m.browse.playlistItemRetryCount < 2 {
			m.browse.playlistItemRetryCount++
			m.browse.activePlaylistItemLoading = true
			return m, m.loadPlaylistItemsCmd(msg.playlistID, m.browse.activePlaylistItemNextOffset, m.browse.activePlaylistLoadToken)
		}
		return m, nil
	}
	m.browse.playlistItemRetryCount = 0
	seen := make(map[string]struct{}, len(m.browse.activePlaylistItemIDs)+len(msg.itemIDs))
	for _, trackID := range m.browse.activePlaylistItemIDs {
		if trackID == "" {
			continue
		}
		seen[trackID] = struct{}{}
	}
	for i, trackID := range msg.itemIDs {
		if trackID == "" {
			continue
		}
		if _, exists := seen[trackID]; exists {
			continue
		}
		seen[trackID] = struct{}{}
		m.browse.activePlaylistItemIDs = append(m.browse.activePlaylistItemIDs, trackID)
		if i < len(msg.itemInfos) {
			if info := msg.itemInfos[i]; info.Name != "" {
				m.browse.trackCache.Set(trackID, info)
			}
		}
	}
	m.browse.activePlaylistItemNextOffset = msg.nextOffset
	m.browse.activePlaylistItemHasMore = msg.hasMore
	cmds := make([]tea.Cmd, 0, 2)
	if cmd := m.maybeLoadMorePlaylistItemsCmd(playlistItemPreloadMax); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleNavDebounceMsg(msg navDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.ui.navToken {
		return m, nil
	}
	if m.ui.activeTab == tabPlayer {
		return m, nil
	}
	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.drainCoverQueueCmd(coverQueueDrainBatch),
		m.maybeLoadMorePlaylistsCmd(m.browse.playlistList),
	)
}

func (m model) handleImageLoadedMsg(msg imageLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.url == "" {
		return m, nil
	}
	if msg.err == nil {
		if m.shouldForceKittyRedrawForLoadedURL(msg.url) {
			m.ui.imgs.forceKittyRedraw()
		}
		if m.transport.status != nil && strings.TrimSpace(m.transport.status.AlbumImageURL) == strings.TrimSpace(msg.url) {
			m.ui.cover.playerCoverFailStreak = 0
		}
		m.ui.cover.clearRetry(msg.url)
		return m, nil
	}
	if m.transport.status != nil && strings.TrimSpace(m.transport.status.AlbumImageURL) == strings.TrimSpace(msg.url) {
		m.ui.cover.playerCoverFailStreak++
		m.maybeFallbackFromKittyOnPlayerFailures(msg.url)
	}

	attempt := m.ui.cover.imageRetryCount[msg.url] + 1
	if attempt > imageLoadRetryMax {
		m.ui.cover.clearRetry(msg.url)
		m.ui.imgs.markFailed(msg.url)
		slog.Warn("image load retries exhausted", "url", msg.url, "error", msg.err)
		if m.libraryHasImageURL(msg.url) {
			return m, m.queueResolvesForImageURLCmd(msg.url, libraryCoverRefreshBatch)
		}
		return m, nil
	}
	_, token := m.ui.cover.nextRetry(msg.url)
	return m, m.imageRetryCmd(msg.url, attempt, token)
}

func (m model) handleImageRetryMsg(msg imageRetryMsg) (tea.Model, tea.Cmd) {
	if msg.url == "" {
		return m, nil
	}
	if current := m.ui.cover.retryToken(msg.url); current != msg.token {
		return m, nil
	}
	if !m.needsImageURL(msg.url) {
		m.ui.cover.clearRetry(msg.url)
		return m, nil
	}
	return m, m.loadImageCmd(msg.url, false)
}

func (m model) handleCoverImageResolvedMsg(msg coverImageResolvedMsg) (tea.Model, tea.Cmd) {
	key := coverResolveKey(msg.kind, msg.id)
	delete(m.ui.cover.resolveInFlight, key)
	if msg.err != nil {
		slog.Warn("resolve context image URL failed", "kind", msg.kind, "id", msg.id, "error", msg.err)
		return m, nil
	}
	if strings.TrimSpace(msg.url) == "" {
		return m, nil
	}
	if !m.applyResolvedContextImageURL(msg.kind, msg.id, msg.url) {
		return m, nil
	}
	return m, m.loadImageCmd(msg.url, false)
}

func (m model) handleActionMsg(msg actionMsg) (tea.Model, tea.Cmd) {
	if m.isStaleStateFetchToken(msg.token) {
		return m, nil
	}
	m.transport.actionInFlight = false
	m.syncExecutorState()
	if msg.err != nil {
		m.transport.transition.Clear()
		m.syncExecutorState()
		m.transport.playbackErr = msg.err
		slog.Error("playback action failed", "error", msg.err)
		if msg.rollback != nil {
			m.transport.status = msg.rollback
		}
		return m, m.pumpInputExecutor()
	}
	m.transport.playbackErr = nil
	switch msg.action {
	case "play-from-browser":
		m.ui.activeTab = tabPlayer
	}
	cmds := []tea.Cmd{m.pollCmd(true)}
	if cmd := m.pumpInputExecutor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleVolDebounceMsg(msg volDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.transport.volDebounceToken || m.transport.volDebouncePending < 0 {
		return m, nil
	}
	target := m.transport.volDebouncePending
	if m.tuiCmdCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSetVolume, Volume: target}:
			m.transport.volDebouncePending = -1
			m.transport.volSentTarget = target
			m.transport.volSentAt = time.Now()
			m.ui.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
		default:
			m.transport.volDebouncePending = target
			m.transport.volDebounceToken++
			return m, m.volDebounceCmd(m.transport.volDebounceToken)
		}
		return m, m.pumpInputExecutor()
	}
	m.transport.volDebouncePending = -1
	m.transport.volSentTarget = target
	m.transport.volSentAt = time.Now()
	m.ui.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
	rollback := cloneStatus(m.transport.status)
	if m.transport.status != nil {
		m.transport.status.Volume = target
	}
	m.beginReconcileAction(0)
	v := target
	if m.service == nil {
		return m, nil
	}
	return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
		return m.service.SetVolume(ctx, m.deviceName, v)
	}, rollback)
}

func (m model) handleSeekDebounceMsg(msg seekDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.transport.seekDebounceToken || m.transport.seekDebouncePending < 0 {
		return m, nil
	}
	target := m.clampSeekTarget(m.transport.seekDebouncePending)
	m.transport.seekDebouncePending = -1
	m.transport.seekSentTarget = target
	m.transport.seekSentAt = time.Now()
	if m.transport.status != nil {
		m.transport.status.ProgressMS = target
		m.resetInterpolationBaseline()
	}
	if m.tuiCmdCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSeek, Position: int64(target)}:
		default:
			m.transport.seekDebouncePending = target
			m.transport.seekDebounceToken++
			return m, m.seekDebounceCmd(m.transport.seekDebounceToken)
		}
		return m, m.pumpInputExecutor()
	}
	rollback := cloneStatus(m.transport.status)
	if m.transport.status != nil {
		m.transport.status.ProgressMS = target
		m.resetInterpolationBaseline()
	}
	m.beginReconcileAction(0)
	p := target
	if m.service == nil {
		return m, nil
	}
	return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
		return m.service.Seek(ctx, m.deviceName, p)
	}, rollback)
}

func (m model) handleFilterMatchesMsg(msg list.FilterMatchesMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.ui.trackPopupOpen {
		m.ui.trackPopupList, cmd = m.ui.trackPopupList.Update(msg)
		return m, cmd
	}
	switch m.ui.activeTab {
	case tabPlaylists:
		m.browse.playlistList, cmd = m.browse.playlistList.Update(msg)
	case tabAlbums:
		m.browse.albumList, cmd = m.browse.albumList.Update(msg)
	}
	return m, cmd
}

func (m model) handleTrackPopupItemsMsg(msg trackPopupItemsMsg) (tea.Model, tea.Cmd) {
	if !m.ui.trackPopupOpen {
		return m, nil
	}
	m.ui.trackPopupItems = msg.items
	maxTitleW := max(m.ui.trackPopupWidth-6, 10)
	items := make([]list.Item, 0, len(msg.items))
	for _, qi := range msg.items {
		qi.Name = truncate(qi.Name, maxTitleW)
		items = append(items, trackItem{item: qi})
	}
	m.ui.trackPopupList.SetItems(items)
	return m, nil
}

func (m model) handleCoverImageURLsBatchResolvedMsg(msg coverImageURLsBatchResolvedMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, r := range msg.results {
		key := coverResolveKey(r.kind, r.id)
		delete(m.ui.cover.resolveInFlight, key)
		if r.err != nil || strings.TrimSpace(r.url) == "" {
			continue
		}
		if !m.applyResolvedContextImageURL(r.kind, r.id, r.url) {
			continue
		}
		cmds = append(cmds, m.loadImageCmd(r.url, false))
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleImagesBatchLoadedMsg(msg imagesBatchLoadedMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, r := range msg.results {
		m.ui.imgs.finishLoad(r.url)
		if r.err != nil {
			m.ui.imgs.markFailed(r.url)
			continue
		}
		m.ui.imgs.clearFailed(r.url)
		if m.shouldForceKittyRedrawForLoadedURL(r.url) {
			m.ui.imgs.forceKittyRedraw()
		}
	}
	if cmd := m.drainCoverQueueCmd(coverQueueDrainBatch); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}
