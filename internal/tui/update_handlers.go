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
	m.width = msg.Width
	m.height = msg.Height

	m.imgs.invalidateCovers()
	m.cachedBodyLayoutValid = false

	layout := m.getBodyLayout()
	listInnerW := layout.rightW - 1
	listInnerH := layout.bodyH - 4

	m.playlistList.SetSize(listInnerW, listInnerH)
	m.albumList.SetSize(listInnerW, listInnerH)
	m.normalizeLibraryPagination()

	if m.trackPopupOpen {
		modalW := min(m.width-8, 60)
		popupBodyH := m.height - headerH - tabBarH - 2
		popupInnerH := popupBodyH - 4
		if popupInnerH < 10 {
			popupInnerH = 10
		}
		m.trackPopupList.SetSize(modalW-2, popupInnerH-4)
	}

	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleTickMsg() (tea.Model, tea.Cmd) {
	m.interpolatePlaybackProgress(uiTickInterval)
	inputCmd := m.pumpInputExecutor()
	var startupCoverCmd tea.Cmd
	if m.startupCoverBoostTicks > 0 {
		m.startupCoverBoostTicks--
		startupCoverCmd = m.drainCoverQueueCmd(coverQueueDrainBatch * 8)
	}

	m.coverRefreshTick++
	m.playerCoverRefreshTick++
	m.libraryCoverRefreshTick++
	m.libraryMetaRefreshTick++
	var coverCmd tea.Cmd
	var playerCoverCmd tea.Cmd
	var libraryCoverCmd tea.Cmd
	var metadataCmd tea.Cmd
	if m.activeTab != tabPlayer && m.coverRefreshTick >= coverRefreshEvery {
		m.coverRefreshTick = 0
		coverCmd = m.loadVisiblePlaylistCoversCmd()
	}
	if m.activeTab == tabPlayer && m.status != nil {
		url := strings.TrimSpace(m.status.AlbumImageURL)
		if url != "" && m.imgs.shouldQueuePriorityLoad(url) {
			playerCoverCmd = m.loadImageCmd(url, true)
			m.playerCoverRefreshTick = 0
		} else if m.playerCoverRefreshTick >= playerCoverRefreshEvery {
			m.playerCoverRefreshTick = 0
			playerCoverCmd = m.loadImageCmd(url, true)
		}
	}
	if m.libraryCoverRefreshTick >= libraryCoverRefreshEvery {
		m.libraryCoverRefreshTick = 0
		libraryCoverCmd = m.loadLibraryCoversCmd(libraryCoverRefreshBatch)
	}
	if m.libraryMetaRefreshTick >= libraryMetaRefreshEvery {
		m.libraryMetaRefreshTick = 0
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
	if m.activeTab != tabPlayer {
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
	m.pollElapsed = 0
	m.pollTick++
	pollQueue := m.pollTick%queuePollEvery == 0
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

	prevPlaylistIndex := m.playlistList.GlobalIndex()
	prevAlbumIndex := m.albumList.GlobalIndex()

	plItems := m.playlistList.Items()
	alItems := m.albumList.Items()
	if msg.offset == 0 {
		plItems = make([]list.Item, 0, len(msg.items))
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

	if m.playlistList.FilterState() == list.Unfiltered {
		m.playlistList.SetItems(plItems)
		if len(plItems) > 0 {
			idx := clampInt(prevPlaylistIndex, 0, len(plItems)-1)
			m.playlistList.Select(idx)
		}
	}
	if m.albumList.FilterState() == list.Unfiltered {
		m.albumList.SetItems(alItems)
		if len(alItems) > 0 {
			idx := clampInt(prevAlbumIndex, 0, len(alItems)-1)
			m.albumList.Select(idx)
		}
	}
	playlistPreviewURL := selectedImageURLFromList(m.playlistList)
	if playlistPreviewURL == "" && len(plItems) > 0 {
		if first, ok := plItems[0].(playlistItem); ok {
			playlistPreviewURL = strings.TrimSpace(first.summary.ImageURL)
		}
	}
	albumPreviewURL := selectedImageURLFromList(m.albumList)
	if albumPreviewURL == "" && len(alItems) > 0 {
		if first, ok := alItems[0].(playlistItem); ok {
			albumPreviewURL = strings.TrimSpace(first.summary.ImageURL)
		}
	}

	if len(msg.items) == 0 || !msg.hasMore {
		m.playlistsExhausted = true
	}
	slog.Info("library items loaded", "playlists", len(plItems), "albums", len(alItems), "missing_image_urls", missingImageURLs)
	return m, tea.Batch(
		m.loadImageCmd(playlistPreviewURL, true),
		m.loadImageCmd(albumPreviewURL, true),
		m.loadLibraryCoversCmd(len(plItems)+len(alItems)),
		m.queueMissingLibraryImageResolvesCmd(missingImageURLs),
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleCurrentUserIDMsg(msg currentUserIDMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil && msg.userID != "" {
		m.currentUserID = msg.userID
		if m.shouldLoadPlaylistItems() && m.activePlaylistID != "" && !m.activePlaylistItemLoading &&
			(m.activePlaylistOwnerID == msg.userID || m.activePlaylistCollaborative) {
			m.activePlaylistItemHasMore = true
			m.activePlaylistItemLoading = true
			m.activePlaylistLoadToken++
			return m, m.loadPlaylistItemsCmd(m.activePlaylistID, 0, m.activePlaylistLoadToken)
		}
	}
	return m, m.loadPlaylistsCmd(0, playlistLoadBatchSize)
}

func (m model) handlePlaylistItemsMsg(msg playlistItemsMsg) (tea.Model, tea.Cmd) {
	if msg.playlistID == "" || msg.playlistID != m.activePlaylistID || msg.token != m.activePlaylistLoadToken {
		return m, nil
	}
	m.activePlaylistItemLoading = false
	if msg.err != nil {
		m.activePlaylistItemHasMore = false
		if !m.shouldLoadPlaylistItems() || spotify.IsForbidden(msg.err) {
			slog.Warn("optional playlist-track fetch skipped", "playlist_id", msg.playlistID, "error", msg.err)
			return m, nil
		}
		m.playbackErr = msg.err
		slog.Error("fetch playlist items failed", "playlist_id", msg.playlistID, "error", msg.err)
		if spotify.IsTransientAPIError(msg.err) && !spotify.IsRateLimitError(msg.err) && m.playlistItemRetryCount < 2 {
			m.playlistItemRetryCount++
			m.activePlaylistItemLoading = true
			return m, m.loadPlaylistItemsCmd(msg.playlistID, m.activePlaylistItemNextOffset, m.activePlaylistLoadToken)
		}
		return m, nil
	}
	m.playlistItemRetryCount = 0
	seen := make(map[string]struct{}, len(m.activePlaylistItemIDs)+len(msg.itemIDs))
	for _, trackID := range m.activePlaylistItemIDs {
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
		m.activePlaylistItemIDs = append(m.activePlaylistItemIDs, trackID)
		if i < len(msg.itemInfos) {
			if info := msg.itemInfos[i]; info.Name != "" {
				m.trackCache.Set(trackID, info)
			}
		}
	}
	m.activePlaylistItemNextOffset = msg.nextOffset
	m.activePlaylistItemHasMore = msg.hasMore
	cmds := make([]tea.Cmd, 0, 2)
	if cmd := m.maybeLoadMorePlaylistItemsCmd(playlistItemPreloadMax); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleNavDebounceMsg(msg navDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.navToken {
		return m, nil
	}
	if m.activeTab == tabPlayer {
		return m, nil
	}
	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.drainCoverQueueCmd(coverQueueDrainBatch),
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleImageLoadedMsg(msg imageLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.url == "" {
		return m, nil
	}
	if msg.err == nil {
		if m.shouldForceKittyRedrawForLoadedURL(msg.url) {
			m.imgs.forceKittyRedraw()
		}
		if m.status != nil && strings.TrimSpace(m.status.AlbumImageURL) == strings.TrimSpace(msg.url) {
			m.cover.playerCoverFailStreak = 0
		}
		m.cover.clearRetry(msg.url)
		return m, nil
	}
	if m.status != nil && strings.TrimSpace(m.status.AlbumImageURL) == strings.TrimSpace(msg.url) {
		m.cover.playerCoverFailStreak++
		m.maybeFallbackFromKittyOnPlayerFailures(msg.url)
	}

	attempt := m.cover.imageRetryCount[msg.url] + 1
	if attempt > imageLoadRetryMax {
		m.cover.clearRetry(msg.url)
		m.imgs.markFailed(msg.url)
		slog.Warn("image load retries exhausted", "url", msg.url, "error", msg.err)
		if m.libraryHasImageURL(msg.url) {
			return m, m.queueResolvesForImageURLCmd(msg.url, libraryCoverRefreshBatch)
		}
		return m, nil
	}
	_, token := m.cover.nextRetry(msg.url)
	return m, m.imageRetryCmd(msg.url, attempt, token)
}

func (m model) handleImageRetryMsg(msg imageRetryMsg) (tea.Model, tea.Cmd) {
	if msg.url == "" {
		return m, nil
	}
	if current := m.cover.retryToken(msg.url); current != msg.token {
		return m, nil
	}
	if !m.needsImageURL(msg.url) {
		m.cover.clearRetry(msg.url)
		return m, nil
	}
	return m, m.loadImageCmd(msg.url, false)
}

func (m model) handleCoverImageResolvedMsg(msg coverImageResolvedMsg) (tea.Model, tea.Cmd) {
	key := coverResolveKey(msg.kind, msg.id)
	delete(m.cover.resolveInFlight, key)
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
	m.actionInFlight = false
	m.syncExecutorState()
	if msg.err != nil {
		m.transportTransitionPending = false
		m.syncExecutorState()
		m.playbackErr = msg.err
		slog.Error("playback action failed", "error", msg.err)
		if msg.rollback != nil {
			m.status = msg.rollback
		}
		return m, m.pumpInputExecutor()
	}
	m.playbackErr = nil
	switch msg.action {
	case "play-from-browser":
		m.activeTab = tabPlayer
	}
	cmds := []tea.Cmd{m.pollCmd(true)}
	if cmd := m.pumpInputExecutor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleVolDebounceMsg(msg volDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.token != m.volDebounceToken || m.volDebouncePending < 0 {
		return m, nil
	}
	target := m.volDebouncePending
	if m.tuiCmdCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSetVolume, Volume: target}:
			m.volDebouncePending = -1
			m.volSentTarget = target
			m.volSentAt = time.Now()
			m.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
		default:
			m.volDebouncePending = target
			m.volDebounceToken++
			return m, m.volDebounceCmd(m.volDebounceToken)
		}
		return m, m.pumpInputExecutor()
	}
	m.volDebouncePending = -1
	m.volSentTarget = target
	m.volSentAt = time.Now()
	m.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
	rollback := cloneStatus(m.status)
	if m.status != nil {
		m.status.Volume = target
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
		return m, m.pumpInputExecutor()
	}
	rollback := cloneStatus(m.status)
	if m.status != nil {
		m.status.ProgressMS = target
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
	if m.trackPopupOpen {
		m.trackPopupList, cmd = m.trackPopupList.Update(msg)
		return m, cmd
	}
	switch m.activeTab {
	case tabPlaylists:
		m.playlistList, cmd = m.playlistList.Update(msg)
	case tabAlbums:
		m.albumList, cmd = m.albumList.Update(msg)
	}
	return m, cmd
}

func (m model) handleTrackPopupItemsMsg(msg trackPopupItemsMsg) (tea.Model, tea.Cmd) {
	if !m.trackPopupOpen {
		return m, nil
	}
	m.trackPopupItems = msg.items
	items := make([]list.Item, 0, len(msg.items))
	for _, qi := range msg.items {
		items = append(items, trackItem{item: qi})
	}
	m.trackPopupList.SetItems(items)
	return m, nil
}

func (m model) handleCoverImageURLsBatchResolvedMsg(msg coverImageURLsBatchResolvedMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, r := range msg.results {
		key := coverResolveKey(r.kind, r.id)
		delete(m.cover.resolveInFlight, key)
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
		m.imgs.finishLoad(r.url)
		if r.err != nil {
			m.imgs.markFailed(r.url)
			continue
		}
		m.imgs.clearFailed(r.url)
		if m.shouldForceKittyRedrawForLoadedURL(r.url) {
			m.imgs.forceKittyRedraw()
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
