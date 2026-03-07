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

	leftW, _ := m.splitWidths()
	listInnerW := leftW - 3
	listInnerH := m.height - chromeH - 4

	m.playlistList.SetSize(listInnerW, listInnerH)
	m.albumList.SetSize(listInnerW, listInnerH)
	return m, tea.Batch(
		m.loadVisiblePlaylistCoversCmd(),
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleTickMsg() (tea.Model, tea.Cmd) {
	m.interpolatePlaybackProgress(uiTickInterval)
	inputCmd := m.pumpInputExecutor()

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
	if m.activeTab == tabPlayer && m.playerCoverRefreshTick >= playerCoverRefreshEvery {
		m.playerCoverRefreshTick = 0
		if m.status != nil {
			playerCoverCmd = m.loadImageCmd(m.status.AlbumImageURL)
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
		return m, tea.Batch(m.tickCmd(), inputCmd, coverCmd, playerCoverCmd, libraryCoverCmd, metadataCmd)
	}
	if m.activeTab != tabPlayer {
		return m, tea.Batch(m.tickCmd(), inputCmd, coverCmd, playerCoverCmd, libraryCoverCmd, metadataCmd)
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
		return m, tea.Batch(m.tickCmd(), inputCmd, playerCoverCmd, libraryCoverCmd, metadataCmd)
	}
	m.pollElapsed = 0
	m.pollTick++
	pollQueue := m.pollTick%queuePollEvery == 0
	return m, tea.Batch(m.pollCmd(pollQueue), m.tickCmd(), inputCmd, playerCoverCmd, libraryCoverCmd, metadataCmd)
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

	m.playlistList.SetItems(plItems)
	m.albumList.SetItems(alItems)
	if len(plItems) > 0 {
		idx := clampInt(prevPlaylistIndex, 0, len(plItems)-1)
		m.playlistList.Select(idx)
	}
	if len(alItems) > 0 {
		idx := clampInt(prevAlbumIndex, 0, len(alItems)-1)
		m.albumList.Select(idx)
	}

	if len(msg.items) == 0 || !msg.hasMore {
		m.playlistsExhausted = true
	}
	missingImageURLs := 0
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
	slog.Info("library items loaded", "playlists", len(plItems), "albums", len(alItems), "missing_image_urls", missingImageURLs)
	return m, tea.Batch(
		m.loadLibraryCoversCmd(0),
		m.loadVisiblePlaylistCoversCmd(),
		m.queueMissingLibraryImageResolvesCmd(libraryCoverRefreshBatch),
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
		m.maybeLoadMorePlaylistsCmd(m.playlistList),
	)
}

func (m model) handleImageLoadedMsg(msg imageLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.url == "" {
		return m, nil
	}
	if msg.err == nil {
		m.coverStats.Loaded++
		if m.status != nil && strings.TrimSpace(m.status.AlbumImageURL) == strings.TrimSpace(msg.url) {
			m.playerCoverFailStreak = 0
		}
		delete(m.imageRetryCount, msg.url)
		delete(m.imageRetryToken, msg.url)
		return m, nil
	}
	m.coverStats.Failed++
	if m.status != nil && strings.TrimSpace(m.status.AlbumImageURL) == strings.TrimSpace(msg.url) {
		m.playerCoverFailStreak++
		m.maybeFallbackFromKittyOnPlayerFailures(msg.url)
	}

	attempt := m.imageRetryCount[msg.url] + 1
	if attempt > imageLoadRetryMax {
		delete(m.imageRetryCount, msg.url)
		delete(m.imageRetryToken, msg.url)
		m.imgs.markFailed(msg.url)
		slog.Warn("image load retries exhausted", "url", msg.url, "error", msg.err)
		if m.libraryHasImageURL(msg.url) {
			return m, m.queueResolvesForImageURLCmd(msg.url, libraryCoverRefreshBatch)
		}
		return m, nil
	}
	m.imageRetryCount[msg.url] = attempt
	m.imageRetryToken[msg.url]++
	m.coverStats.Retried++
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

func (m model) handleCoverImageResolvedMsg(msg coverImageResolvedMsg) (tea.Model, tea.Cmd) {
	key := coverResolveKey(msg.kind, msg.id)
	delete(m.coverResolveInFlight, key)
	if msg.err != nil {
		m.coverStats.ResolveFailed++
		slog.Warn("resolve context image URL failed", "kind", msg.kind, "id", msg.id, "error", msg.err)
		return m, nil
	}
	if strings.TrimSpace(msg.url) == "" {
		return m, nil
	}
	if !m.applyResolvedContextImageURL(msg.kind, msg.id, msg.url) {
		return m, nil
	}
	m.coverStats.ResolveOK++
	return m, m.loadImageCmd(msg.url)
}

func (m model) handleActionMsg(msg actionMsg) (tea.Model, tea.Cmd) {
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
		if msg.reconcile {
			cmds := []tea.Cmd{m.pollCmd(true)}
			if cmd := m.pumpInputExecutor(); cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
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
	switch m.activeTab {
	case tabPlaylists:
		var cmd tea.Cmd
		m.playlistList, cmd = m.playlistList.Update(msg)
		return m, cmd
	case tabAlbums:
		var cmd tea.Cmd
		m.albumList, cmd = m.albumList.Update(msg)
		return m, cmd
	}
	return m, nil
}
