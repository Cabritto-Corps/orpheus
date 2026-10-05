package tui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/spotify"
)

type playerBackendMsg struct {
	ready   bool
	catalog spotify.PlaylistCatalog
	err     error
}

type authLoginRequiredMsg struct{ url string }
type authBrowserResultMsg struct{ err error }
type authClipboardResultMsg struct{ err error }
type clientIDSavedMsg struct{ err error }
type clientIDBrowserMsg struct{ err error }
type clientIDCopyMsg struct{ err error }
type clientIDGuideBrowserMsg struct{ err error }
type clientIDPasteMsg struct {
	text string
	err  error
}
type authLoginCompleteMsg struct{ err error }

// AuthLoginRequired opens the Spotify sign-in dialog with the private login URL.
func AuthLoginRequired(url string) tea.Msg {
	return authLoginRequiredMsg{url: url}
}

func PlayerBackendReady(catalog spotify.PlaylistCatalog) tea.Msg {
	return playerBackendMsg{ready: true, catalog: catalog}
}

// The deadline re-render: releases the idle-backend hold independently of
// whichever tick interval applies later.
type revealGraceMsg struct{}

type queueMetaRevealMsg struct{}

func PlayerBackendFailed(err error) tea.Msg {
	return playerBackendMsg{err: err}
}

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
	if m.ui.imgs == nil || m.ui.imgs.protocolForRender() != imageProtocolKitty {
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

func (m model) handlePlaybackStateMsg(msg playbackStateMsg) (tea.Model, tea.Cmd) {
	if !m.acceptPlaybackStateSeq(msg.seq) {
		return m, nil
	}
	// Any accepted push — even an idle nil-status one — ends the sync bound.
	m.transport.statePushSeen = true
	queueMetaArmedNow := false
	if msg.queueMetaPending && !m.transport.queueMetaPending {
		m.transport.queueMetaRevealEnd = time.Now().Add(queueMetaRevealGrace)
		queueMetaArmedNow = true
	}
	m.transport.queueMetaPending = msg.queueMetaPending
	prevStatus := m.transport.status
	prevQueueHead := queueHeadTrackID(m.transport.queue)
	inVolSettle := m.transport.volDebouncePending >= 0 ||
		(m.transport.volSentTarget >= 0 && time.Since(m.transport.volSentAt) < volSettleWindow)
	if inVolSettle && msg.status != nil && prevStatus != nil {
		msg.status.Volume = prevStatus.Volume
	}
	// Pin to the sent target only after the burst leaves the wire; while pending, the display is already optimistic.
	if inVolSettle && m.transport.volDebouncePending < 0 && msg.status != nil && m.transport.volSentTarget >= 0 {
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
	if msg.queueIncluded && m.shouldApplyIncomingQueue(nextTrackID) {
		m.applyMergedQueue(msg.queue, msg.queueHasMore, true, true)
	}
	m.transport.status = mergeStatusFromPrevious(prevStatus, m.transport.queue, msg.status)
	if m.transport.status != nil {
		m.smoothApplyProgress(m.transport.status.ProgressMS)
	}
	m.advancePlayerCoverEpochIfNeeded(prevStatus, m.transport.status, prevQueueHead, queueHeadTrackID(m.transport.queue))
	if !isFrozenHeartbeat(prevStatus, len(m.transport.queue), msg) {
		m.transport.playbackErr = nil
	}
	m.maybeClearTransportTransition(m.transport.status)
	m.fireOnSongChange(prevStatus, m.transport.status)
	cmds := []tea.Cmd{}
	if m.shouldEnsureAlbumImageLoad(prevStatus, m.transport.status) {
		cmds = append(cmds, m.loadImageCmd(m.transport.status.AlbumImageURL, true))
	}
	m.pinQueueHeadCovers()
	if cmd := m.sweepQueueCoversCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.pumpInputExecutor(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if queueMetaArmedNow {
		cmds = append(cmds, tea.Tick(queueMetaRevealGrace, func(time.Time) tea.Msg { return queueMetaRevealMsg{} }))
	}
	cmds = append(cmds, m.kittyOverlayCmd())
	return m, tea.Batch(cmds...)
}

func (m *model) scheduleNavDebounceCmd() tea.Cmd {
	m.ui.navToken++
	return m.navDebounceCmd(m.ui.navToken)
}

func (m *model) loadVisiblePlaylistCoversCmd() tea.Cmd {
	if m.ui.cover.sweepPaused() {
		return nil
	}
	m.normalizeLibraryPagination()
	urls := make([]string, 0, 64)
	add := func(url string) {
		if url == "" {
			return
		}
		urls = append(urls, url)
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
		center := min(max(m.browse.playlistList.GlobalIndex(), 0), len(items)-1)
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
		center := min(max(m.browse.albumList.GlobalIndex(), 0), len(albumItems)-1)
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

	// A prune must not touch the playing queue: a browse refresh would
	// otherwise drop the sweep the state handler just enqueued.
	keep := make(map[string]struct{}, len(urls))
	for _, u := range urls {
		keep[u] = struct{}{}
	}
	if m.transport.status != nil {
		keep[strings.TrimSpace(m.transport.status.AlbumImageURL)] = struct{}{}
	}
	for _, item := range m.transport.queue {
		if url := strings.TrimSpace(item.ImageURL); url != "" {
			keep[url] = struct{}{}
		}
	}
	m.ui.cover.pruneExcept(keep)
	for _, u := range urls {
		if !m.ui.imgs.shouldQueueLoad(u) {
			continue
		}
		m.enqueueCoverURL(u)
	}

	return m.drainCoverQueueCmd(coverQueueDrainBatch)
}

func (m *model) loadLibraryCoversCmd(limit int) tea.Cmd {
	if m.ui.cover.sweepPaused() {
		return nil
	}
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

	// Drain one batch so the first cover renders while the chained drain continues.
	if added > coverQueueDrainBatch {
		added = coverQueueDrainBatch
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

func (m model) resolveCatalog() spotify.PlaylistCatalog {
	if m.catalogSource != nil {
		return m.catalogSource.get()
	}
	return m.catalog
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
	// Single-flight: a slow hook must not stack goroutines during fast skips.
	if !m.transport.songChangeInFlight.CompareAndSwap(false, true) {
		return
	}
	cmd := m.transport.onSongChange
	go func(name, artist, id string) {
		defer m.transport.songChangeInFlight.Store(false)
		execCmd(cmd, name, artist, id)
	}(nextName, nextArtist, nextID)
}

const songChangeTimeout = 10 * time.Second

func execCmd(template, trackName, artistName, trackID string) {
	if err := runSongChangeHook(template, trackName, artistName, trackID); err != nil {
		slog.Warn("on-song-change hook failed", "cmd", template, "error", err)
	}
}

func runSongChangeHook(template, trackName, artistName, trackID string) error {
	return runSongChangeHookWithTimeout(template, trackName, artistName, trackID, songChangeTimeout)
}

func runSongChangeHookWithTimeout(template, trackName, artistName, trackID string, timeout time.Duration) error {
	cmd, _, cancel := newSongChangeCmdWithTimeout(template, trackName, artistName, trackID, timeout)
	if cmd == nil {
		return nil
	}
	defer cancel()
	return cmd.Run()
}

// Hook output is discarded: the child inherits the TUI tty and the
// cell-diffing renderer would persist anything it prints.
func newSongChangeCmd(template, trackName, artistName, trackID string) (*exec.Cmd, context.CancelFunc) {
	cmd, _, cancel := newSongChangeCmdWithTimeout(template, trackName, artistName, trackID, songChangeTimeout)
	return cmd, cancel
}

func newSongChangeCmdWithTimeout(template, trackName, artistName, trackID string, timeout time.Duration) (*exec.Cmd, context.Context, context.CancelFunc) {
	r := strings.NewReplacer(
		"{track}", trackName,
		"{artist}", artistName,
		"{id}", trackID,
	)
	expanded := r.Replace(template)
	parts := strings.Fields(expanded)
	if len(parts) == 0 {
		return nil, nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// The cancel must outlive Run: it carries the hook deadline.
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	var cmd *exec.Cmd
	if len(parts) > 1 {
		cmd = exec.CommandContext(ctx, parts[0], parts[1:]...) // nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	} else {
		cmd = exec.CommandContext(ctx, parts[0]) // nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	}
	cmd.Env = append(os.Environ(),
		"ORPHEUS_TRACK="+trackName,
		"ORPHEUS_ARTIST="+artistName,
		"ORPHEUS_TRACK_ID="+trackID,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd, ctx, cancel
}
