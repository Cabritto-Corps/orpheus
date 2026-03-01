package tui

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

// ── Screen identifiers ────────────────────────────────────────────────────────

type screen string

const (
	screenPlaylist          screen = "playlist"
	screenPlayback          screen = "playback"
	playlistTrackPageSize          = 100
	queuePollEvery                 = 4
	playlistLoadBatchSize          = 20
	playlistLoadMax                = 500
	playlistTrackPreloadMax        = 500
	coverPreloadWindow             = 6
	uiTickInterval                 = 200 * time.Millisecond
	navDebounceInterval            = 120 * time.Millisecond
	volSeekDebounceInterval        = 150 * time.Millisecond
)

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	ctx        context.Context
	catalog    spotify.PlaylistCatalog
	service    *spotify.Service
	deviceName string
	tuiCmdCh   chan librespot.TUICommand

	// Timing
	pollInterval time.Duration
	pollTick     int
	pollElapsed  time.Duration

	// Screen state
	screen         screen
	modal          bool
	navToken       int
	actionInFlight bool

	volDebouncePending  int
	volDebounceToken    int
	seekDebouncePending int
	seekDebounceToken   int

	// Playback data
	status          *spotify.PlaybackStatus
	queue           []spotify.QueueItem
	queueHasMore    bool
	stableQueueLen  int // frozen at each track transition; drives "+ n more" display

	activePlaylistID              string
	activePlaylistOwnerID         string
	activePlaylistCollaborative   bool
	activePlaylistTrackIDs        []string
	activePlaylistTrackNextOffset int
	activePlaylistTrackHasMore    bool
	activePlaylistTrackLoading    bool
	activePlaylistLoadToken       int
	preloadedTrackIDs             map[string]struct{}
	playlistsLoading              bool
	playlistsExhausted            bool
	playlistsErr                  error
	playlistsRetryCount           int
	playbackErr                   error
	preloadInFlight               bool
	playlistTrackRetryCount       int
	currentUserID                 string

	// List components — browser and modal have separate scroll/filter state
	playlistList list.Model
	modalList    list.Model

	// Image cache (pointer — shared across all model copies in the update cycle)
	imgs *imgCache

	// Layout
	width     int
	height    int
	nerdFonts bool

	// UI components
	help help.Model
	keys keyMap
}

// ── playlistItem ──────────────────────────────────────────────────────────────

type playlistItem struct {
	summary spotify.PlaylistSummary
}

func (p playlistItem) Title() string { return p.summary.Name }
func (p playlistItem) FilterValue() string {
	return p.summary.Name + " " + p.summary.Owner
}

func (p playlistItem) Description() string {
	return "by " + p.summary.Owner
}

// ── Constructor ───────────────────────────────────────────────────────────────

func newModel(ctx context.Context, catalog spotify.PlaylistCatalog, service *spotify.Service, cfg config.Config, tuiCmdCh chan librespot.TUICommand) model {
	delegate := newPlaylistDelegate()

	// Browser list (full-screen left panel)
	browser := list.New(nil, delegate, 40, 20)
	browser.Title = "Playlists"
	browser.SetShowStatusBar(true)
	browser.SetFilteringEnabled(true)
	browser.SetShowFilter(true)
	browser.SetShowHelp(false)
	applyListStyles(&browser)

	// Modal list (popup picker — separate scroll state, same items)
	modal := list.New(nil, delegate, 40, 20)
	modal.Title = ""
	modal.SetShowTitle(false)
	modal.SetShowStatusBar(false)
	modal.SetFilteringEnabled(true)
	modal.SetShowFilter(true)
	modal.SetShowHelp(false)
	applyListStyles(&modal)

	h := newHelp()

	return model{
		ctx:                 ctx,
		catalog:             catalog,
		service:             service,
		deviceName:          cfg.DeviceName,
		tuiCmdCh:            tuiCmdCh,
		pollInterval:        cfg.PollInterval,
		screen:              screenPlaylist,
		playlistList:        browser,
		modalList:           modal,
		imgs:                newImgCache(),
		preloadedTrackIDs:   make(map[string]struct{}),
		playlistsLoading:    true,
		nerdFonts:           cfg.NerdFonts,
		volDebouncePending:  -1,
		seekDebouncePending: -1,
		help:                h,
		keys:                newKeys(),
	}
}

// ── Entry point ───────────────────────────────────────────────────────────────

func Run(ctx context.Context, catalog spotify.PlaylistCatalog, service *spotify.Service, cfg config.Config, tuiCmdCh chan librespot.TUICommand, playbackStateCh <-chan *librespot.PlaybackStateUpdate) error {
	m := newModel(ctx, catalog, service, cfg, tuiCmdCh)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if playbackStateCh != nil {
		StartPlaybackStateListener(playbackStateCh, p.Send, ctx)
	}
	_, err := p.Run()
	return err
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.getCurrentUserIDCmd(),
		m.tickCmd(),
	)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// ── Window resize ──────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		leftW, _ := m.splitWidths()
		listInnerW := leftW - 6 // border(2) + padding(2) + list margin(2)
		listInnerH := m.height - chromeH - 4

		m.playlistList.SetSize(listInnerW, listInnerH)
		// Modal list is sized dynamically in modalView on each render.
		return m, tea.Batch(
			m.loadVisiblePlaylistCoversCmd(),
			m.maybeLoadMorePlaylistsCmd(m.playlistList),
		)

	// ── Periodic tick (poll or progress interpolation) ─────────────────────
	case tickMsg:
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
		if m.status == nil || !m.status.Playing {
			interval = min(interval*2, 5*time.Second)
		}
		m.pollElapsed += uiTickInterval
		if m.pollElapsed < interval {
			return m, m.tickCmd()
		}
		m.pollElapsed = 0
		m.pollTick++
		pollQueue := m.pollTick%queuePollEvery == 0
		return m, tea.Batch(m.pollCmd(pollQueue), m.tickCmd())

	// ── Playback state (pushed from librespot bridge) ──────────────────────
	case playbackStateMsg:
		prevStatus := m.status
		prevAlbumURL := ""
		if prevStatus != nil {
			prevAlbumURL = prevStatus.AlbumImageURL
		}
		m.status = mergeStatusFromPrevious(prevStatus, m.queue, msg.status)
		prevTrackID := ""
		if prevStatus != nil {
			prevTrackID = normalizeQueueID(prevStatus.TrackID)
		}
		nextTrackID := ""
		if msg.status != nil {
			nextTrackID = normalizeQueueID(msg.status.TrackID)
		}
		// Track transitions (or initial load) reset the stable tail baseline.
		// Shuffle/seek/volume/metadata updates do not — the queue reorders but the count stays.
		refreshQueue := len(m.queue) == 0 || (nextTrackID != "" && nextTrackID != prevTrackID)
		m.queue = mergeQueueWithRest(m.queue, msg.queue)
		if refreshQueue {
			m.stableQueueLen = len(m.queue)
			m.queueHasMore = msg.queueHasMore
		}
		m.playbackErr = nil
		cmds := []tea.Cmd{}
		if m.status != nil && m.status.AlbumImageURL != prevAlbumURL {
			cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
		}
		return m, tea.Batch(cmds...)

	// ── Playback state (polled from Web API) ─────────────────────────────────
	case pollMsg:
		if msg.err != nil {
			if errors.Is(msg.err, spotify.ErrNoActiveTrack) {
				m.status = nil
				m.queue = nil
				m.queueHasMore = false
				m.stableQueueLen = 0
				m.playbackErr = nil
			} else {
				m.playbackErr = msg.err
				slog.Error("poll status failed", "error", msg.err)
			}
			return m, nil
		}
		m.pollElapsed = 0
		prevAlbumURL := ""
		if m.status != nil {
			prevAlbumURL = m.status.AlbumImageURL
		}
		m.status = msg.status
		m.playbackErr = nil
		if msg.queueFetched {
			m.queue = msg.queue
			m.queueHasMore = false
			m.stableQueueLen = len(m.queue)
		}
		if msg.queueErr != nil {
			m.playbackErr = msg.queueErr
			slog.Error("fetch queue failed", "error", msg.queueErr)
		}

		cmds := make([]tea.Cmd, 0, 3)
		if m.status != nil && m.status.AlbumImageURL != prevAlbumURL {
			cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
		}
		if msg.queueFetched {
			if toQueue := m.nextTracksToPreload(playlistTrackPreloadMax); len(toQueue) > 0 && !m.preloadInFlight {
				m.preloadInFlight = true
				cmds = append(cmds, m.preloadQueueCmd(toQueue))
			}
			if cmd := m.maybeLoadMorePlaylistTracksCmd(playlistTrackPreloadMax); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	// ── Playlists loaded ───────────────────────────────────────────────────
	case playlistsMsg:
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
			seen[pl.summary.ID] = struct{}{}
		}
		for _, pl := range msg.items {
			if _, exists := seen[pl.ID]; exists {
				continue
			}
			items = append(items, playlistItem{summary: pl})
			seen[pl.ID] = struct{}{}
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

	case currentUserIDMsg:
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

	case playlistTracksMsg:
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
		for _, trackID := range msg.trackIDs {
			if trackID == "" {
				continue
			}
			if _, exists := seen[trackID]; exists {
				continue
			}
			seen[trackID] = struct{}{}
			m.activePlaylistTrackIDs = append(m.activePlaylistTrackIDs, trackID)
		}
		m.activePlaylistTrackNextOffset = msg.nextOffset
		m.activePlaylistTrackHasMore = msg.hasMore
		cmds := make([]tea.Cmd, 0, 2)
		if toQueue := m.nextTracksToPreload(playlistTrackPreloadMax); len(toQueue) > 0 && !m.preloadInFlight {
			m.preloadInFlight = true
			cmds = append(cmds, m.preloadQueueCmd(toQueue))
		}
		if cmd := m.maybeLoadMorePlaylistTracksCmd(playlistTrackPreloadMax); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case preloadQueueMsg:
		m.preloadInFlight = false
		if len(msg.trackIDs) > 0 {
			if m.preloadedTrackIDs == nil {
				m.preloadedTrackIDs = make(map[string]struct{}, len(msg.trackIDs))
			}
			for _, trackID := range msg.trackIDs {
				m.preloadedTrackIDs[trackID] = struct{}{}
			}
		}
		if msg.err != nil {
			slog.Warn("queue preload partial failure", "queued", len(msg.trackIDs), "error", msg.err)
		}
		return m, nil

	case navDebounceMsg:
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

	// ── Image loaded ───────────────────────────────────────────────────────
	case imageLoadedMsg:
		if msg.err != nil {
			// Image fetch failure is non-fatal; cover just shows placeholder.
			return m, nil
		}
		// The image is already stored in m.imgs by the cmd; nothing else needed.
		return m, nil

	case actionReconcileMsg:
		m.actionInFlight = false
		if msg.err != nil {
			m.playbackErr = msg.err
			if msg.rollback != nil {
				m.status = msg.rollback
				slog.Error("playback action failed", "error", msg.err)
			} else {
				slog.Info("reconcile failed", "error", msg.err)
			}
			return m, nil
		}
		if msg.status == nil {
			return m, m.reconcileCmd()
		}
		m.playbackErr = nil
		m.pollElapsed = 0
		prevAlbumURL := ""
		if m.status != nil {
			prevAlbumURL = m.status.AlbumImageURL
		}
		m.status = msg.status
		if msg.queueFetched {
			m.queue = msg.queue
			m.queueHasMore = false
			m.stableQueueLen = len(m.queue)
		}
		cmds := make([]tea.Cmd, 0, 3)
		if m.status != nil && m.status.AlbumImageURL != prevAlbumURL {
			cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
		}
		if msg.queueFetched {
			if toQueue := m.nextTracksToPreload(playlistTrackPreloadMax); len(toQueue) > 0 && !m.preloadInFlight {
				m.preloadInFlight = true
				cmds = append(cmds, m.preloadQueueCmd(toQueue))
			}
			if cmd := m.maybeLoadMorePlaylistTracksCmd(playlistTrackPreloadMax); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	// ── Action result ──────────────────────────────────────────────────────
	case actionMsg:
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
		}
		return m, m.pollCmd(true)

	case volDebounceMsg:
		if msg.token != m.volDebounceToken || m.volDebouncePending < 0 {
			return m, nil
		}
		target := m.volDebouncePending
		m.volDebouncePending = -1
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
		m.actionInFlight = true
		v := target
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.SetVolume(ctx, m.deviceName, v)
		}, rollback)

	case seekDebounceMsg:
		if msg.token != m.seekDebounceToken || m.seekDebouncePending < 0 {
			return m, nil
		}
		target := m.seekDebouncePending
		m.seekDebouncePending = -1
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSeek, Position: int64(target)}:
			default:
			}
			return m, nil
		}
		rollback := cloneStatus(m.status)
		if m.status != nil {
			m.status.ProgressMS = target
		}
		m.actionInFlight = true
		p := target
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Seek(ctx, m.deviceName, p)
		}, rollback)

	// ── Async filter results from bubbles list ─────────────────────────────
	// filterItems() runs in a goroutine and sends FilterMatchesMsg back.
	// We must forward it to whichever list is currently active so the
	// displayed items update in real-time as the user types.
	case list.FilterMatchesMsg:
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

	// ── Keys ───────────────────────────────────────────────────────────────
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// ── Key dispatch ──────────────────────────────────────────────────────────────

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys

	// Global keys (work everywhere).
	switch {
	case keyMatches(msg, k.Quit):
		return m, tea.Quit
	case keyMatches(msg, k.ToggleHelp):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	}

	// Modal is open: only modal navigation + close/select.
	if m.modal {
		return m.handleModalKey(msg)
	}

	// Playlist browser screen.
	if m.screen == screenPlaylist {
		return m.handlePlaylistKey(msg)
	}

	// Playback screen.
	return m.handlePlaybackKey(msg)
}

func (m model) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	if m.modalList.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.modalList, cmd = m.modalList.Update(msg)
		return m, tea.Batch(cmd, m.scheduleNavDebounceCmd())
	}
	switch {
	case keyMatches(msg, k.CloseModal):
		m.modal = false
		m.modalList.ResetFilter()
		return m, nil

	case keyMatches(msg, k.Select):
		sel, ok := m.modalList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel, "play-from-modal")
	}

	var cmd tea.Cmd
	m.modalList, cmd = m.modalList.Update(msg)
	return m, tea.Batch(cmd, m.scheduleNavDebounceCmd())
}

func (m model) handlePlaylistKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	if m.playlistList.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.playlistList, cmd = m.playlistList.Update(msg)
		return m, tea.Batch(cmd, m.scheduleNavDebounceCmd())
	}
	switch {
	case keyMatches(msg, k.Refresh):
		m.playlistsLoading = true
		m.playlistsExhausted = false
		m.playlistsErr = nil
		m.playlistsRetryCount = 0
		return m, m.loadPlaylistsCmd(0, playlistLoadBatchSize)

	case keyMatches(msg, k.Select):
		sel, ok := m.playlistList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel, "play-from-browser")
	}

	// Forward to list (handles navigation, filtering, pagination).
	var cmd tea.Cmd
	m.playlistList, cmd = m.playlistList.Update(msg)
	return m, tea.Batch(cmd, m.scheduleNavDebounceCmd())
}

func (m model) handlePlaybackKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case keyMatches(msg, k.Refresh):
		if m.tuiCmdCh != nil {
			return m, nil
		}
		return m, m.pollCmd(true)

	case keyMatches(msg, k.OpenPicker):
		m.modal = true
		m.modalList.ResetFilter()
		m.modalList.Select(0)
		return m, nil

	case keyMatches(msg, k.PlayPause):
		if m.tuiCmdCh != nil {
			kind := librespot.TUICommandResume
			if m.status != nil && m.status.Playing {
				kind = librespot.TUICommandPause
			}
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: kind}:
			default:
			}
			return m, nil
		}
		if m.actionInFlight {
			return m, nil
		}
		rollback := cloneStatus(m.status)
		shouldPlay := rollback == nil || !rollback.Playing
		if m.status != nil {
			m.status.Playing = shouldPlay
		}
		m.actionInFlight = true
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			if shouldPlay {
				return m.service.Play(ctx, m.deviceName)
			}
			return m.service.Pause(ctx, m.deviceName)
		}, rollback)

	case keyMatches(msg, k.Next):
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSkipNext}:
			default:
			}
			return m, nil
		}
		if m.actionInFlight {
			return m, nil
		}
		rollback := cloneStatus(m.status)
		if m.status != nil {
			m.status.ProgressMS = 0
			m.status.Playing = true
			if len(m.queue) > 0 {
				m.status.TrackID = m.queue[0].ID
				m.status.TrackName = m.queue[0].Name
				m.status.ArtistName = m.queue[0].Artist
				m.status.AlbumImageURL = ""
			}
		}
		m.actionInFlight = true
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Next(ctx, m.deviceName)
		}, rollback)

	case keyMatches(msg, k.Prev):
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandSkipPrev}:
			default:
			}
			return m, nil
		}
		if m.actionInFlight {
			return m, nil
		}
		rollback := cloneStatus(m.status)
		if m.status != nil {
			m.status.ProgressMS = 0
			m.status.Playing = true
		}
		m.actionInFlight = true
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Previous(ctx, m.deviceName)
		}, rollback)

	case keyMatches(msg, k.Shuffle):
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandShuffle}:
			default:
			}
			return m, nil
		}
		if m.actionInFlight {
			return m, nil
		}
		rollback := cloneStatus(m.status)
		nextShuffle := true
		if m.status != nil {
			nextShuffle = !m.status.ShuffleState
			m.status.ShuffleState = nextShuffle
		}
		m.actionInFlight = true
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Shuffle(ctx, m.deviceName, nextShuffle)
		}, rollback)

	case keyMatches(msg, k.VolUp):
		if m.actionInFlight || m.status == nil {
			return m, nil
		}
		target := 50
		if m.volDebouncePending >= 0 {
			target = clampInt(m.volDebouncePending+5, 0, 100)
		} else {
			target = clampInt(m.status.Volume+5, 0, 100)
		}
		m.status.Volume = target
		m.volDebouncePending = target
		m.volDebounceToken++
		return m, m.volDebounceCmd(m.volDebounceToken)

	case keyMatches(msg, k.VolDown):
		if m.actionInFlight || m.status == nil {
			return m, nil
		}
		target := 50
		if m.volDebouncePending >= 0 {
			target = clampInt(m.volDebouncePending-5, 0, 100)
		} else {
			target = clampInt(m.status.Volume-5, 0, 100)
		}
		m.status.Volume = target
		m.volDebouncePending = target
		m.volDebounceToken++
		return m, m.volDebounceCmd(m.volDebounceToken)

	case keyMatches(msg, k.SeekBack):
		if m.actionInFlight || m.status == nil {
			return m, nil
		}
		target := 0
		if m.seekDebouncePending >= 0 {
			target = max(0, m.seekDebouncePending-5000)
		} else {
			target = max(0, m.status.ProgressMS-5000)
		}
		m.status.ProgressMS = target
		m.seekDebouncePending = target
		m.seekDebounceToken++
		return m, m.seekDebounceCmd(m.seekDebounceToken)

	case keyMatches(msg, k.SeekFwd):
		if m.actionInFlight || m.status == nil {
			return m, nil
		}
		target := 0
		if m.seekDebouncePending >= 0 {
			target = m.seekDebouncePending + 5000
			if m.status.DurationMS > 0 {
				target = min(target, m.status.DurationMS)
			}
		} else {
			target = m.status.ProgressMS + 5000
			if m.status.DurationMS > 0 {
				target = min(target, m.status.DurationMS)
			}
		}
		m.status.ProgressMS = target
		m.seekDebouncePending = target
		m.seekDebounceToken++
		return m, m.seekDebounceCmd(m.seekDebounceToken)
	}

	return m, nil
}

// ── Utilities ─────────────────────────────────────────────────────────────────

func (m model) selectAndPlayPlaylist(sel playlistItem, action string) (tea.Model, tea.Cmd) {
	m.screen = screenPlayback
	m.playbackErr = nil
	canReadTracks := m.shouldLoadPlaylistTracks() && m.canReadPlaylistTracks(sel.summary)
	m.setActivePlaylist(sel.summary.ID, canReadTracks, sel.summary.OwnerID, sel.summary.Collaborative)
	if m.tuiCmdCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandPlayContext, URI: sel.summary.URI}:
		default:
		}
		cmds := []tea.Cmd{m.loadImageCmd(sel.summary.ImageURL)}
		if canReadTracks {
			m.activePlaylistTrackLoading = true
			m.activePlaylistLoadToken++
			cmds = append(cmds, m.loadPlaylistTracksCmd(sel.summary.ID, 0, m.activePlaylistLoadToken))
		}
		return m, tea.Batch(cmds...)
	}
	cmds := []tea.Cmd{
		m.actionCmd(func(ctx context.Context) error {
			return m.service.PlayPlaylist(ctx, m.deviceName, sel.summary.URI)
		}, action),
		m.loadImageCmd(sel.summary.ImageURL),
	}
	if canReadTracks {
		m.activePlaylistTrackLoading = true
		m.activePlaylistLoadToken++
		cmds = append(cmds, m.loadPlaylistTracksCmd(sel.summary.ID, 0, m.activePlaylistLoadToken))
	}
	return m, tea.Batch(cmds...)
}

// keyMatches delegates to key.Matches which handles modifier combos correctly.
func keyMatches(msg tea.KeyMsg, b key.Binding) bool {
	return key.Matches(msg, b)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func cloneStatus(status *spotify.PlaybackStatus) *spotify.PlaybackStatus {
	if status == nil {
		return nil
	}
	cp := *status
	return &cp
}

func (m *model) scheduleNavDebounceCmd() tea.Cmd {
	m.navToken++
	return m.navDebounceCmd(m.navToken)
}

func (m *model) interpolatePlaybackProgress(step time.Duration) {
	if step <= 0 || m.status == nil || !m.status.Playing || m.status.DurationMS <= 0 {
		return
	}
	next := m.status.ProgressMS + int(step/time.Millisecond)
	m.status.ProgressMS = min(next, m.status.DurationMS)
}

func (m model) loadVisiblePlaylistCoversCmd() tea.Cmd {
	seen := make(map[string]struct{})
	cmds := make([]tea.Cmd, 0, m.playlistList.Paginator.PerPage+coverPreloadWindow+1)

	add := func(url string) {
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}
		cmds = append(cmds, m.loadImageCmd(url))
	}

	if sel, ok := m.selectedPlaylist(); ok {
		add(sel.summary.ImageURL)
	}
	for _, pl := range m.visiblePlaylistItems() {
		add(pl.summary.ImageURL)
	}
	items := m.playlistList.Items()
	if m.playlistList.FilterState() == list.Unfiltered && len(items) > 0 {
		center := clampInt(m.playlistList.GlobalIndex(), 0, len(items)-1)
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

	return tea.Batch(cmds...)
}

func (m model) visiblePlaylistItems() []playlistItem {
	visible := m.playlistList.VisibleItems()
	if len(visible) == 0 {
		return nil
	}

	perPage := m.playlistList.Paginator.PerPage
	if perPage <= 0 {
		perPage = len(visible)
	}
	start := m.playlistList.Paginator.Page * perPage
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

func mergeStatusFromPrevious(prev *spotify.PlaybackStatus, queue []spotify.QueueItem, next *spotify.PlaybackStatus) *spotify.PlaybackStatus {
	if next == nil {
		return next
	}
	out := *next
	if out.TrackName != "" && out.ArtistName != "" && out.DurationMS > 0 {
		return &out
	}
	sameTrack := func(id string) bool { return id != "" && id == next.TrackID }
	if prev != nil && sameTrack(prev.TrackID) {
		if out.TrackName == "" && prev.TrackName != "" {
			out.TrackName = prev.TrackName
		}
		if out.ArtistName == "" && prev.ArtistName != "" {
			out.ArtistName = prev.ArtistName
		}
		if out.AlbumName == "" && prev.AlbumName != "" {
			out.AlbumName = prev.AlbumName
		}
		if out.AlbumImageURL == "" && prev.AlbumImageURL != "" {
			out.AlbumImageURL = prev.AlbumImageURL
		}
		if out.DurationMS <= 0 && prev.DurationMS > 0 {
			out.DurationMS = prev.DurationMS
		}
	}
	if len(queue) > 0 && sameTrack(queue[0].ID) {
		if out.TrackName == "" && queue[0].Name != "" {
			out.TrackName = queue[0].Name
		}
		if out.ArtistName == "" && queue[0].Artist != "" && queue[0].Artist != "-" {
			out.ArtistName = queue[0].Artist
		}
		if out.DurationMS <= 0 && queue[0].DurationMS > 0 {
			out.DurationMS = queue[0].DurationMS
		}
	}
	return &out
}

func normalizeQueueID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return id
	}
	if strings.HasPrefix(id, "spotify:") {
		parts := strings.Split(id, ":")
		if len(parts) >= 3 {
			return parts[len(parts)-1]
		}
	}
	return id
}

func mergeQueueNames(prev, next []spotify.QueueItem) []spotify.QueueItem {
	if len(next) == 0 {
		return next
	}
	byID := make(map[string]spotify.QueueItem, len(prev))
	for _, q := range prev {
		byID[normalizeQueueID(q.ID)] = q
	}
	out := make([]spotify.QueueItem, len(next))
	for i, q := range next {
		out[i] = q
		if q.Name != "" && q.Artist != "" {
			continue
		}
		key := normalizeQueueID(q.ID)
		if p, ok := byID[key]; ok {
			if out[i].Name == "" && p.Name != "" {
				out[i].Name = p.Name
			}
			if out[i].Artist == "" && p.Artist != "" {
				out[i].Artist = p.Artist
			}
			if out[i].DurationMS <= 0 && p.DurationMS > 0 {
				out[i].DurationMS = p.DurationMS
			}
		}
	}
	return out
}

const librespotQueueWindow = 32

func mergeQueueWithRest(prev, next []spotify.QueueItem) []spotify.QueueItem {
	merged := mergeQueueNames(prev, next)
	if len(next) > librespotQueueWindow || len(prev) <= librespotQueueWindow {
		return merged
	}
	for i := librespotQueueWindow; i < len(prev); i++ {
		merged = append(merged, prev[i])
	}
	return merged
}

func (m *model) maybeLoadMorePlaylistsCmd(activeList list.Model) tea.Cmd {
	if m.playlistsLoading || m.playlistsExhausted {
		return nil
	}
	if activeList.FilterState() != list.Unfiltered {
		return nil
	}

	items := m.playlistList.Items()
	if len(items) == 0 || len(items) >= playlistLoadMax {
		if len(items) >= playlistLoadMax {
			m.playlistsExhausted = true
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
		m.playlistsExhausted = true
		return nil
	}

	m.playlistsLoading = true
	return m.loadPlaylistsCmd(nextOffset, limit)
}

func (m *model) setActivePlaylist(playlistID string, canReadTracks bool, ownerID string, collaborative bool) {
	m.activePlaylistID = playlistID
	m.activePlaylistOwnerID = ownerID
	m.activePlaylistCollaborative = collaborative
	m.activePlaylistTrackIDs = nil
	m.activePlaylistTrackNextOffset = 0
	m.activePlaylistTrackHasMore = playlistID != "" && canReadTracks
	m.activePlaylistTrackLoading = false
	m.playlistTrackRetryCount = 0
	if m.preloadedTrackIDs == nil {
		m.preloadedTrackIDs = make(map[string]struct{})
		return
	}
	for id := range m.preloadedTrackIDs {
		delete(m.preloadedTrackIDs, id)
	}
}

func (m *model) maybeLoadMorePlaylistTracksCmd(limit int) tea.Cmd {
	if !m.shouldLoadPlaylistTracks() || limit <= 0 || m.activePlaylistID == "" || !m.activePlaylistTrackHasMore || m.activePlaylistTrackLoading || m.status == nil || m.status.TrackID == "" {
		return nil
	}
	currentIndex := -1
	for i, trackID := range m.activePlaylistTrackIDs {
		if trackID == m.status.TrackID {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return nil
	}
	if currentIndex >= 0 && len(m.activePlaylistTrackIDs)-currentIndex-1 >= limit {
		return nil
	}
	m.activePlaylistTrackLoading = true
	m.activePlaylistLoadToken++
	return m.loadPlaylistTracksCmd(m.activePlaylistID, m.activePlaylistTrackNextOffset, m.activePlaylistLoadToken)
}

func (m model) canReadPlaylistTracks(pl spotify.PlaylistSummary) bool {
	if m.currentUserID == "" {
		return false
	}
	return pl.OwnerID == m.currentUserID || pl.Collaborative
}

func (m model) shouldLoadPlaylistTracks() bool {
	return m.service != nil
}

func (m model) nextTracksToPreload(limit int) []string {
	if limit <= 0 || m.status == nil || m.status.TrackID == "" || len(m.activePlaylistTrackIDs) == 0 || m.activePlaylistID == "" {
		return nil
	}

	currentIndex := -1
	for i, trackID := range m.activePlaylistTrackIDs {
		if trackID == m.status.TrackID {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return nil
	}

	blocked := make(map[string]struct{}, len(m.queue)+len(m.preloadedTrackIDs)+1)
	for _, q := range m.queue {
		if q.ID != "" {
			blocked[q.ID] = struct{}{}
		}
	}
	for trackID := range m.preloadedTrackIDs {
		blocked[trackID] = struct{}{}
	}
	blocked[m.status.TrackID] = struct{}{}

	out := make([]string, 0, limit)
	for i := currentIndex + 1; i < len(m.activePlaylistTrackIDs) && len(out) < limit; i++ {
		trackID := m.activePlaylistTrackIDs[i]
		if trackID == "" {
			continue
		}
		if _, exists := blocked[trackID]; exists {
			continue
		}
		out = append(out, trackID)
		blocked[trackID] = struct{}{}
	}
	return out
}
