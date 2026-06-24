package tui

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/spotify"
)

func (m model) shouldEnsureAlbumImageLoad(prev, next *spotify.PlaybackStatus) bool {
	if shouldQueueAlbumImageLoad(prev, next) {
		return true
	}
	if next == nil {
		return false
	}
	url := strings.TrimSpace(next.AlbumImageURL)
	if url == "" {
		return false
	}
	return m.ui.imgs != nil && m.ui.imgs.shouldQueuePriorityLoad(url)
}

func (m model) needsImageURL(url string) bool {
	if url == "" {
		return false
	}
	if m.transport.status != nil && m.transport.status.AlbumImageURL == url {
		return true
	}
	if sel, ok := m.selectedPlaylist(); ok && sel.summary.ImageURL == url {
		return true
	}
	if sel, ok := m.selectedAlbum(); ok && sel.summary.ImageURL == url {
		return true
	}
	for _, pl := range m.visiblePlaylistItems() {
		if pl.summary.ImageURL == url {
			return true
		}
	}
	for _, pl := range m.visibleAlbumItems() {
		if pl.summary.ImageURL == url {
			return true
		}
	}
	return m.libraryHasImageURL(url)
}

func (m model) shouldForceKittyRedrawForLoadedURL(url string) bool {
	if m.ui.imgs == nil || m.ui.imgs.protocol != imageProtocolKitty {
		return false
	}
	target := strings.TrimSpace(url)
	if target == "" {
		return false
	}
	switch m.ui.activeTab {
	case tabPlayer:
		return m.transport.status != nil && strings.TrimSpace(m.transport.status.AlbumImageURL) == target
	case tabPlaylists:
		return strings.TrimSpace(selectedImageURLFromList(m.browse.playlistList)) == target
	case tabAlbums:
		return strings.TrimSpace(selectedImageURLFromList(m.browse.albumList)) == target
	default:
		return false
	}
}

func (m model) libraryHasImageURL(url string) bool {
	if url == "" {
		return false
	}
	for _, item := range m.browse.playlistList.Items() {
		pl, ok := item.(playlistItem)
		if ok && pl.summary.ImageURL == url {
			return true
		}
	}
	for _, item := range m.browse.albumList.Items() {
		al, ok := item.(playlistItem)
		if ok && al.summary.ImageURL == url {
			return true
		}
	}
	return false
}

func (m model) hasMissingLibraryImageURLs() bool {
	for _, item := range m.browse.playlistList.Items() {
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		if strings.TrimSpace(pl.summary.ImageURL) == "" {
			return true
		}
	}
	for _, item := range m.browse.albumList.Items() {
		al, ok := item.(playlistItem)
		if !ok {
			continue
		}
		if strings.TrimSpace(al.summary.ImageURL) == "" {
			return true
		}
	}
	return false
}

func (m model) isStaleStateFetchToken(token uint64) bool {
	return token > 0 && token != m.ui.stateFetchToken
}

func (m *model) acceptPlaybackStateSeq(seq uint64) bool {
	if seq == 0 {
		return true
	}
	if seq <= m.ui.lastPlaybackStateSeq {
		return false
	}
	m.ui.lastPlaybackStateSeq = seq
	return true
}

func (m *model) clearQueueOnTrackBoundary(prevTrackID, incomingTrackID string, queueFetched bool) {
	if queueFetched {
		return
	}
	if prevTrackID == "" || incomingTrackID == "" || incomingTrackID == prevTrackID {
		return
	}
	m.transport.queue = nil
	m.transport.queueHasMore = false
	m.transport.stableQueueLen = 0
}

func (m *model) applyFetchedStatusAndQueue(prevTrackID string, status *spotify.PlaybackStatus, queue []spotify.QueueItem, queueFetched bool, queueHasMore bool, observedVol int) {
	m.applyStatusSettleOverrides(status, observedVol)
	incomingTrack := ""
	if status != nil {
		incomingTrack = golibrespot.NormalizeSpotifyId(status.TrackID)
		if prevTrackID != "" && incomingTrack != "" && incomingTrack != prevTrackID {
			m.transport.seekDebouncePending = -1
			m.transport.seekSentTarget = -1
		}
	}
	m.transport.status = status
	m.maybeClearTransportTransition(m.transport.status)
	m.clearQueueOnTrackBoundary(prevTrackID, incomingTrack, queueFetched)
	if queueFetched && m.shouldApplyIncomingQueue(incomingTrack) {
		m.applyMergedQueue(queue, queueHasMore, true, true)
	}
}

func (m model) handlePlaybackStateMsg(msg playbackStateMsg) (tea.Model, tea.Cmd) {
	if !m.acceptPlaybackStateSeq(msg.seq) {
		return m, nil
	}
	prevStatus := m.transport.status
	prevQueueHead := queueHeadTrackID(m.transport.queue)
	inVolSettle := m.transport.volDebouncePending >= 0 ||
		(m.transport.volSentTarget >= 0 && time.Since(m.transport.volSentAt) < volSettleWindow)
	if inVolSettle && msg.status != nil && prevStatus != nil {
		msg.status.Volume = prevStatus.Volume
	}
	if inVolSettle && msg.status != nil && m.transport.volSentTarget >= 0 {
		msg.status.Volume = m.transport.volSentTarget
	}
	if m.transport.volSentTarget >= 0 && time.Since(m.transport.volSentAt) >= volSettleWindow {
		m.transport.volSentTarget = -1
	}
	incomingProgress := -1
	if msg.status != nil {
		incomingProgress = msg.status.ProgressMS
	}
	if m.shouldApplySeekSettle(msg.status) {
		msg.status.ProgressMS = m.clampSeekTarget(m.seekSettleProgress())
	}
	m.clearSeekSettleTarget(incomingProgress)
	prevTrackID := ""
	if prevStatus != nil {
		prevTrackID = golibrespot.NormalizeSpotifyId(prevStatus.TrackID)
	}
	nextTrackID := ""
	if msg.status != nil {
		nextTrackID = golibrespot.NormalizeSpotifyId(msg.status.TrackID)
	}
	if prevTrackID != "" && nextTrackID != "" && nextTrackID != prevTrackID {
		m.transport.seekDebouncePending = -1
		m.transport.seekSentTarget = -1
	}
	prevShuffleState := false
	if prevStatus != nil {
		prevShuffleState = prevStatus.ShuffleState
	}
	shuffleChanged := msg.status != nil && msg.status.ShuffleState != prevShuffleState
	if shuffleChanged {
		m.transport.queue = nil
		m.transport.queueHasMore = false
		m.transport.stableQueueLen = 0
	}
	if m.shouldApplyIncomingQueue(nextTrackID) {
		m.applyMergedQueue(msg.queue, msg.queueHasMore, true, true)
	}
	m.transport.status = mergeStatusFromPrevious(prevStatus, m.transport.queue, msg.status, m.browse.trackCache)
	if m.transport.status != nil {
		m.smoothApplyProgress(m.transport.status.ProgressMS)
	}
	m.advancePlayerCoverEpochIfNeeded(prevStatus, m.transport.status, prevQueueHead, queueHeadTrackID(m.transport.queue))
	m.maybeClearTransportTransition(m.transport.status)
	m.transport.playbackErr = nil
	m.fireOnSongChange(prevStatus, m.transport.status)
	cmds := []tea.Cmd{}
	if m.shouldEnsureAlbumImageLoad(prevStatus, m.transport.status) {
		cmds = append(cmds, m.loadImageCmd(m.transport.status.AlbumImageURL, true))
	}
	if cmd := m.consumeTransportRecoveryCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.pumpInputExecutor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handlePollMsg(msg pollMsg) (tea.Model, tea.Cmd) {
	if m.isStaleStateFetchToken(msg.token) {
		return m, nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, spotify.ErrNoActiveTrack) {
			m.transport.status = nil
			m.transport.interpolationSyncAt = time.Time{}
			m.transport.interpolationProgressMS = 0
			m.transport.queue = nil
			m.transport.queueHasMore = false
			m.transport.stableQueueLen = 0
			m.transport.playbackErr = nil
		} else {
			m.transport.playbackErr = msg.err
			slog.Error("poll status failed", "error", msg.err)
		}
		m.transport.transition.Clear()
		m.syncExecutorState()
		return m, m.pumpInputExecutor()
	}
	m.ui.lastPollTime = time.Now()
	prevStatus := m.transport.status
	prevQueueHead := queueHeadTrackID(m.transport.queue)
	prevTrackID := ""
	if m.transport.status != nil {
		prevTrackID = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
	}
	incomingVol := -1
	if msg.status != nil {
		incomingVol = msg.status.Volume
	}
	m.applyFetchedStatusAndQueue(prevTrackID, msg.status, msg.queue, msg.queueFetched, false, incomingVol)
	m.transport.playbackErr = nil
	if msg.queueErr != nil {
		m.transport.playbackErr = msg.queueErr
		slog.Error("fetch queue failed", "error", msg.queueErr)
	}
	m.transport.status = mergeStatusFromPrevious(prevStatus, m.transport.queue, m.transport.status, m.browse.trackCache)
	if m.transport.status != nil {
		m.smoothApplyProgress(m.transport.status.ProgressMS)
	}
	m.advancePlayerCoverEpochIfNeeded(prevStatus, m.transport.status, prevQueueHead, queueHeadTrackID(m.transport.queue))
	m.fireOnSongChange(prevStatus, m.transport.status)

	cmds := make([]tea.Cmd, 0, 3)
	if m.shouldEnsureAlbumImageLoad(prevStatus, m.transport.status) {
		cmds = append(cmds, m.loadImageCmd(m.transport.status.AlbumImageURL, true))
	}
	if msg.queueFetched {
		if cmd := m.maybeLoadMorePlaylistItemsCmd(playlistItemPreloadMax); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if cmd := m.consumeTransportRecoveryCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.pumpInputExecutor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleActionReconcileMsg(msg actionReconcileMsg) (tea.Model, tea.Cmd) {
	if m.isStaleStateFetchToken(msg.token) {
		return m, nil
	}
	m.transport.actionInFlight = false
	m.syncExecutorState()
	if msg.err != nil {
		m.transport.transition.Clear()
		m.syncExecutorState()
		m.transport.playbackErr = msg.err
		m.transport.seekSentTarget = -1
		if m.transport.volDebouncePending < 0 {
			m.transport.volSentTarget = -1
		}
		if msg.rollback != nil {
			m.transport.status = msg.rollback
			slog.Error("playback action failed", "error", msg.err)
		} else {
			slog.Info("reconcile failed", "error", msg.err)
		}
		return m, m.pumpInputExecutor()
	}
	if msg.status == nil {
		return m, m.reconcileCmd()
	}
	m.transport.playbackErr = nil
	m.ui.lastPollTime = time.Now()
	prevStatus := m.transport.status
	prevQueueHead := queueHeadTrackID(m.transport.queue)
	prevTrackID := ""
	if m.transport.status != nil {
		prevTrackID = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
	}
	reconciledVol := -1
	if msg.status != nil {
		reconciledVol = msg.status.Volume
	}
	m.applyFetchedStatusAndQueue(prevTrackID, msg.status, msg.queue, msg.queueFetched, false, reconciledVol)
	m.transport.status = mergeStatusFromPrevious(prevStatus, m.transport.queue, m.transport.status, m.browse.trackCache)
	if m.transport.status != nil {
		m.smoothApplyProgress(m.transport.status.ProgressMS)
	}
	m.advancePlayerCoverEpochIfNeeded(prevStatus, m.transport.status, prevQueueHead, queueHeadTrackID(m.transport.queue))
	cmds := make([]tea.Cmd, 0, 3)
	if m.shouldEnsureAlbumImageLoad(prevStatus, m.transport.status) {
		cmds = append(cmds, m.loadImageCmd(m.transport.status.AlbumImageURL, true))
	}
	if msg.queueFetched {
		if cmd := m.maybeLoadMorePlaylistItemsCmd(playlistItemPreloadMax); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if cmd := m.consumeTransportRecoveryCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.pumpInputExecutor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *model) scheduleNavDebounceCmd() tea.Cmd {
	m.ui.navToken++
	return m.navDebounceCmd(m.ui.navToken)
}

func (m *model) loadVisiblePlaylistCoversCmd() tea.Cmd {
	m.normalizeLibraryPagination()
	seen := make(map[string]struct{})

	add := func(url string) {
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		if !m.ui.imgs.shouldQueueLoad(url) {
			return
		}
		seen[url] = struct{}{}
		m.enqueueCoverURL(url)
	}

	if sel, ok := m.selectedPlaylist(); ok {
		add(sel.summary.ImageURL)
	}
	if sel, ok := m.selectedAlbum(); ok {
		add(sel.summary.ImageURL)
	}
	for _, pl := range m.visiblePlaylistItems() {
		add(pl.summary.ImageURL)
	}
	for _, pl := range m.visibleAlbumItems() {
		add(pl.summary.ImageURL)
	}
	items := m.browse.playlistList.Items()
	if m.browse.playlistList.FilterState() == list.Unfiltered && len(items) > 0 {
		center := clampInt(m.browse.playlistList.GlobalIndex(), 0, len(items)-1)
		half := coverPreloadWindow / 2
		start := max(0, center-half)
		end := min(len(items), center+half+1)
		for _, item := range items[start:end] {
			pl, ok := item.(playlistItem)
			if !ok {
				continue
			}
			add(pl.summary.ImageURL)
		}
	}
	albumItems := m.browse.albumList.Items()
	if m.browse.albumList.FilterState() == list.Unfiltered && len(albumItems) > 0 {
		center := clampInt(m.browse.albumList.GlobalIndex(), 0, len(albumItems)-1)
		half := coverPreloadWindow / 2
		start := max(0, center-half)
		end := min(len(albumItems), center+half+1)
		for _, item := range albumItems[start:end] {
			pl, ok := item.(playlistItem)
			if !ok {
				continue
			}
			add(pl.summary.ImageURL)
		}
	}

	return m.drainCoverQueueCmd(coverQueueDrainBatch)
}

func (m *model) loadLibraryCoversCmd(limit int) tea.Cmd {
	seen := make(map[string]struct{})
	added := 0

	add := func(url string) {
		if limit > 0 && added >= limit {
			return
		}
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		if !m.ui.imgs.shouldQueueLoad(url) {
			return
		}
		seen[url] = struct{}{}
		m.enqueueCoverURL(url)
		added++
	}

	for _, item := range m.browse.playlistList.Items() {
		if limit > 0 && added >= limit {
			break
		}
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		add(pl.summary.ImageURL)
	}
	for _, item := range m.browse.albumList.Items() {
		if limit > 0 && added >= limit {
			break
		}
		al, ok := item.(playlistItem)
		if !ok {
			continue
		}
		add(al.summary.ImageURL)
	}

	return m.drainCoverQueueCmd(added)
}

func (m model) visiblePlaylistItems() []playlistItem {
	visible := m.browse.playlistList.VisibleItems()
	if len(visible) == 0 {
		return nil
	}

	perPage := m.browse.playlistList.Paginator.PerPage
	if perPage <= 0 {
		perPage = len(visible)
	}
	start := m.browse.playlistList.Paginator.Page * perPage
	if start < 0 || start >= len(visible) {
		return nil
	}
	end := min(len(visible), start+perPage)

	out := make([]playlistItem, 0, end-start)
	for _, item := range visible[start:end] {
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		out = append(out, pl)
	}
	return out
}

func (m model) visibleAlbumItems() []playlistItem {
	visible := m.browse.albumList.VisibleItems()
	if len(visible) == 0 {
		return nil
	}

	perPage := m.browse.albumList.Paginator.PerPage
	if perPage <= 0 {
		perPage = len(visible)
	}
	start := m.browse.albumList.Paginator.Page * perPage
	if start < 0 || start >= len(visible) {
		return nil
	}
	end := min(len(visible), start+perPage)

	out := make([]playlistItem, 0, end-start)
	for _, item := range visible[start:end] {
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		out = append(out, pl)
	}
	return out
}

func (m *model) maybeLoadMorePlaylistsCmd(activeList list.Model) tea.Cmd {
	if m.browse.playlistsLoading || m.browse.playlistsExhausted {
		return nil
	}
	if activeList.FilterState() != list.Unfiltered {
		return nil
	}

	items := m.browse.playlistList.Items()
	if len(items) == 0 || len(items) >= playlistLoadMax {
		if len(items) >= playlistLoadMax {
			m.browse.playlistsExhausted = true
		}
		return nil
	}

	remaining := len(activeList.VisibleItems()) - activeList.Index() - 1
	threshold := max(12, activeList.Paginator.PerPage)
	if remaining > threshold {
		return nil
	}

	nextOffset := len(items)
	limit := min(playlistLoadBatchSize, playlistLoadMax-nextOffset)
	if limit <= 0 {
		m.browse.playlistsExhausted = true
		return nil
	}

	m.browse.playlistsLoading = true
	return m.loadPlaylistsCmd(nextOffset, limit)
}

func (m *model) setActivePlaylist(playlistID string, canReadTracks bool, ownerID string, collaborative bool) {
	m.browse.activePlaylistID = playlistID
	m.browse.activePlaylistOwnerID = ownerID
	m.browse.activePlaylistCollaborative = collaborative
	m.browse.activePlaylistItemIDs = nil
	m.browse.activePlaylistItemNextOffset = 0
	m.browse.activePlaylistItemHasMore = playlistID != "" && canReadTracks
	m.browse.activePlaylistItemLoading = false
	m.browse.playlistItemRetryCount = 0
	if m.browse.preloadedItemIDs == nil {
		m.browse.preloadedItemIDs = make(map[string]struct{})
	}
	for id := range m.browse.preloadedItemIDs {
		delete(m.browse.preloadedItemIDs, id)
	}
	m.browse.trackCache.Clear()
}

func (m *model) maybeLoadMorePlaylistItemsCmd(limit int) tea.Cmd {
	if !m.shouldLoadPlaylistItems() || limit <= 0 || m.browse.activePlaylistID == "" || !m.browse.activePlaylistItemHasMore || m.browse.activePlaylistItemLoading || m.transport.status == nil || m.transport.status.TrackID == "" {
		return nil
	}
	currentNorm := golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
	currentIndex := -1
	for i, trackID := range m.browse.activePlaylistItemIDs {
		if golibrespot.NormalizeSpotifyId(trackID) == currentNorm {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return nil
	}
	if currentIndex >= 0 && len(m.browse.activePlaylistItemIDs)-currentIndex-1 >= limit {
		return nil
	}
	m.browse.activePlaylistItemLoading = true
	m.browse.activePlaylistLoadToken++
	return m.loadPlaylistItemsCmd(m.browse.activePlaylistID, m.browse.activePlaylistItemNextOffset, m.browse.activePlaylistLoadToken)
}

func (m model) canReadPlaylistTracks(pl spotify.PlaylistSummary) bool {
	if m.browse.currentUserID == "" {
		return false
	}
	return pl.OwnerID == m.browse.currentUserID || pl.Collaborative
}

func (m model) shouldLoadPlaylistItems() bool {
	return m.service != nil
}

func (m model) resolveCatalog() spotify.PlaylistCatalog {
	if m.catalog != nil {
		return m.catalog
	}
	if m.service != nil {
		return m.service
	}
	return nil
}

func (m *model) fireOnSongChange(prev, next *spotify.PlaybackStatus) {
	if m.transport.onSongChange == "" {
		return
	}
	prevID := ""
	if prev != nil {
		prevID = golibrespot.NormalizeSpotifyId(prev.TrackID)
	}
	nextID := ""
	nextName := ""
	nextArtist := ""
	if next != nil {
		nextID = golibrespot.NormalizeSpotifyId(next.TrackID)
		nextName = next.TrackName
		nextArtist = next.ArtistName
	}
	if nextID == "" || nextID == prevID {
		return
	}
	if m.transport.lastPlayedID == nextID {
		return
	}
	m.transport.lastPlayedID = nextID
	cmd := m.transport.onSongChange
	go func(name, artist, id string) {
		execCmd(cmd, name, artist, id)
	}(nextName, nextArtist, nextID)
}

func execCmd(template, trackName, artistName, trackID string) {
	r := strings.NewReplacer(
		"{track}", trackName,
		"{artist}", artistName,
		"{id}", trackID,
	)
	expanded := r.Replace(template)
	parts := strings.Fields(expanded)
	if len(parts) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if len(parts) > 1 {
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
	} else {
		cmd = exec.CommandContext(ctx, parts[0])
	}
	cmd.Env = append(os.Environ(),
		"ORPHEUS_TRACK="+trackName,
		"ORPHEUS_ARTIST="+artistName,
		"ORPHEUS_TRACK_ID="+trackID,
	)
	if err := cmd.Run(); err != nil {
		slog.Warn("on-song-change hook failed", "cmd", template, "error", err)
	}
}
