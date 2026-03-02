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
	volSettleWindow                = 3 * time.Second
)

type model struct {
	ctx        context.Context
	catalog    spotify.PlaylistCatalog
	service    *spotify.Service
	deviceName string
	tuiCmdCh   chan librespot.TUICommand

	pollInterval        time.Duration
	pollTick            int
	pollElapsed         time.Duration
	actionFastPollUntil time.Time

	screen         screen
	modal          bool
	navToken       int
	actionInFlight bool

	volDebouncePending  int
	volDebounceToken    int
	volSentAt           time.Time
	volSentTarget       int
	seekDebouncePending int
	seekDebounceToken   int

	status             *spotify.PlaybackStatus
	queue              []spotify.QueueItem
	queueHasMore       bool
	stableQueueLen     int
	pendingContextFrom string

	activePlaylistID              string
	activePlaylistOwnerID         string
	activePlaylistCollaborative   bool
	activePlaylistTrackIDs        []string
	activePlaylistTrackNextOffset int
	activePlaylistTrackHasMore    bool
	activePlaylistTrackLoading    bool
	activePlaylistLoadToken       int
	preloadedTrackIDs             map[string]struct{}
	trackCache                    map[string]spotify.QueueItem
	playlistsLoading              bool
	playlistsExhausted            bool
	playlistsErr                  error
	playlistsRetryCount           int
	playbackErr                   error
	playlistTrackRetryCount       int
	currentUserID                 string

	playlistList list.Model
	modalList    list.Model

	imgs *imgCache

	width     int
	height    int
	nerdFonts bool

	help help.Model
	keys keyMap
}

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

func newModel(ctx context.Context, catalog spotify.PlaylistCatalog, service *spotify.Service, cfg config.Config, tuiCmdCh chan librespot.TUICommand) model {
	delegate := newPlaylistDelegate()

	browser := list.New(nil, delegate, 40, 20)
	browser.Title = "Playlists"
	browser.SetShowStatusBar(true)
	browser.SetFilteringEnabled(true)
	browser.SetShowFilter(true)
	browser.SetShowHelp(false)
	applyListStyles(&browser)

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
		trackCache:          make(map[string]spotify.QueueItem),
		playlistsLoading:    true,
		nerdFonts:           cfg.NerdFonts,
		volDebouncePending:  -1,
		seekDebouncePending: -1,
		volSentTarget:       -1,
		help:                h,
		keys:                newKeys(),
	}
}

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

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.getCurrentUserIDCmd(),
		m.tickCmd(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		leftW, _ := m.splitWidths()
		listInnerW := leftW - 3
		listInnerH := m.height - chromeH - 4

		m.playlistList.SetSize(listInnerW, listInnerH)
		return m, tea.Batch(
			m.loadVisiblePlaylistCoversCmd(),
			m.maybeLoadMorePlaylistsCmd(m.playlistList),
		)

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
		if !m.actionFastPollUntil.IsZero() && time.Now().Before(m.actionFastPollUntil) {
			interval = uiTickInterval
		} else if m.status == nil || !m.status.Playing {
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

	case playbackStateMsg:
		return m.handlePlaybackStateMsg(msg)

	case pollMsg:
		return m.handlePollMsg(msg)

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

	case imageLoadedMsg:
		return m, nil

	case actionReconcileMsg:
		return m.handleActionReconcileMsg(msg)

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
		m.volSentTarget = target
		m.volSentAt = time.Now()
		m.actionFastPollUntil = time.Now().Add(3 * time.Second)
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

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m model) handlePlaybackStateMsg(msg playbackStateMsg) (tea.Model, tea.Cmd) {
	prevStatus := m.status
	prevAlbumURL := ""
	if prevStatus != nil {
		prevAlbumURL = prevStatus.AlbumImageURL
	}
	inVolSettle := m.volDebouncePending >= 0 ||
		(m.volSentTarget >= 0 && time.Since(m.volSentAt) < volSettleWindow)
	if inVolSettle && msg.status != nil && prevStatus != nil {
		msg.status.Volume = prevStatus.Volume
	}
	if inVolSettle && msg.status != nil && m.volSentTarget >= 0 {
		msg.status.Volume = m.volSentTarget
	}
	// Check device confirmation using incoming volume BEFORE the override above.
	// prevStatus.Volume was already set to the optimistic target by the key handler,
	// so use the raw device report from msg.queue instead.
	// We can't recover the pre-override value here; use timeout-only clear.
	if m.volSentTarget >= 0 && time.Since(m.volSentAt) >= volSettleWindow {
		m.volSentTarget = -1
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
	refreshQueue := len(m.queue) == 0 || (nextTrackID != "" && nextTrackID != prevTrackID)
	if nextTrackID != m.pendingContextFrom {
		m.pendingContextFrom = ""
	}
	prevShuffleState := false
	if prevStatus != nil {
		prevShuffleState = prevStatus.ShuffleState
	}
	shuffleChanged := m.status != nil && m.status.ShuffleState != prevShuffleState
	m.applyMergedQueue(msg.queue, msg.queueHasMore, refreshQueue || shuffleChanged, true)
	m.playbackErr = nil
	cmds := []tea.Cmd{}
	if m.status != nil && m.status.AlbumImageURL != prevAlbumURL {
		cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
	}
	return m, tea.Batch(cmds...)
}

func (m model) handlePollMsg(msg pollMsg) (tea.Model, tea.Cmd) {
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
	inVolSettle := m.volDebouncePending >= 0 ||
		(m.volSentTarget >= 0 && time.Since(m.volSentAt) < volSettleWindow)
	incomingVol := -1
	if msg.status != nil {
		incomingVol = msg.status.Volume
	}
	if inVolSettle && msg.status != nil && m.volSentTarget >= 0 {
		msg.status.Volume = m.volSentTarget
	}
	// Clear guard only when the device actually reports the target (real confirmation),
	// not after the override above, which would always match.
	m.clearVolumeSettleTarget(incomingVol)
	m.status = msg.status
	m.playbackErr = nil
	if msg.queueFetched {
		incomingTrack := ""
		if msg.status != nil {
			incomingTrack = normalizeQueueID(msg.status.TrackID)
		}
		if m.shouldApplyIncomingQueue(incomingTrack) {
			m.applyMergedQueue(msg.queue, false, true, true)
		}
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
		if cmd := m.maybeLoadMorePlaylistTracksCmd(playlistTrackPreloadMax); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleActionReconcileMsg(msg actionReconcileMsg) (tea.Model, tea.Cmd) {
	m.actionInFlight = false
	if msg.err != nil {
		m.playbackErr = msg.err
		if m.volDebouncePending < 0 {
			m.volSentTarget = -1
		}
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
	inVolSettle := m.volDebouncePending >= 0 ||
		(m.volSentTarget >= 0 && time.Since(m.volSentAt) < volSettleWindow)
	reconciledVol := -1
	if msg.status != nil {
		reconciledVol = msg.status.Volume
	}
	if inVolSettle && msg.status != nil && m.volSentTarget >= 0 {
		msg.status.Volume = m.volSentTarget
	}
	m.clearVolumeSettleTarget(reconciledVol)
	m.status = msg.status
	if msg.queueFetched {
		incomingTrack := ""
		if msg.status != nil {
			incomingTrack = normalizeQueueID(msg.status.TrackID)
		}
		if m.shouldApplyIncomingQueue(incomingTrack) {
			m.applyMergedQueue(msg.queue, false, true, true)
		}
	}
	cmds := make([]tea.Cmd, 0, 3)
	if m.status != nil && m.status.AlbumImageURL != prevAlbumURL {
		cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
	}
	if msg.queueFetched {
		if cmd := m.maybeLoadMorePlaylistTracksCmd(playlistTrackPreloadMax); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys

	switch {
	case keyMatches(msg, k.Quit):
		return m, tea.Quit
	case keyMatches(msg, k.ToggleHelp):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil
	}

	if m.modal {
		return m.handleModalKey(msg)
	}

	if m.screen == screenPlaylist {
		return m.handlePlaylistKey(msg)
	}

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
		m.actionFastPollUntil = time.Now().Add(2 * time.Second)
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
		m.actionFastPollUntil = time.Now().Add(2 * time.Second)
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
		m.actionFastPollUntil = time.Now().Add(2 * time.Second)
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Previous(ctx, m.deviceName)
		}, rollback)

	case keyMatches(msg, k.Shuffle):
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandShuffle}:
			default:
			}
			for id := range m.preloadedTrackIDs {
				delete(m.preloadedTrackIDs, id)
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
		// Invalidate preloaded tracks so they don't corrupt the new playback order.
		for id := range m.preloadedTrackIDs {
			delete(m.preloadedTrackIDs, id)
		}
		m.stableQueueLen = len(m.queue)
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

func (m model) selectAndPlayPlaylist(sel playlistItem, action string) (tea.Model, tea.Cmd) {
	m.screen = screenPlayback
	m.modal = false
	m.playbackErr = nil
	canReadTracks := m.shouldLoadPlaylistTracks() && m.canReadPlaylistTracks(sel.summary)
	m.setActivePlaylist(sel.summary.ID, canReadTracks, sel.summary.OwnerID, sel.summary.Collaborative)
	if m.status != nil {
		m.pendingContextFrom = normalizeQueueID(m.status.TrackID)
	}
	m.queue = nil
	m.queueHasMore = false
	m.stableQueueLen = 0
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
	m.actionFastPollUntil = time.Now().Add(3 * time.Second)
	if canReadTracks {
		m.activePlaylistTrackLoading = true
		m.activePlaylistLoadToken++
		cmds = append(cmds, m.loadPlaylistTracksCmd(sel.summary.ID, 0, m.activePlaylistLoadToken))
	}
	return m, tea.Batch(cmds...)
}

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

func (m *model) clearVolumeSettleTarget(observed int) {
	if m.volSentTarget < 0 {
		return
	}
	if observed >= 0 && observed == m.volSentTarget {
		m.volSentTarget = -1
		return
	}
	if time.Since(m.volSentAt) >= volSettleWindow {
		m.volSentTarget = -1
	}
}

func (m *model) shouldApplyIncomingQueue(incomingTrack string) bool {
	if m.pendingContextFrom == "" {
		return true
	}
	if incomingTrack == m.pendingContextFrom {
		m.queue = nil
		m.stableQueueLen = 0
		m.queueHasMore = false
		return false
	}
	m.pendingContextFrom = ""
	return true
}

func (m *model) applyMergedQueue(incoming []spotify.QueueItem, queueHasMore bool, updateStable bool, updateHasMore bool) {
	preserveTail := !(m.status != nil && m.status.ShuffleState)
	m.queue = mergeQueueWithRest(m.queue, incoming, m.trackCache, preserveTail)
	if updateStable {
		m.stableQueueLen = len(m.queue)
	}
	if updateHasMore {
		m.queueHasMore = queueHasMore
	}
	m.rebuildPreloadedFromQueue()
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

func mergeQueueNames(prev, next []spotify.QueueItem, cache map[string]spotify.QueueItem) []spotify.QueueItem {
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
		if (out[i].Name == "" || out[i].Artist == "") && cache != nil {
			if c, ok := cache[key]; ok {
				if out[i].Name == "" && c.Name != "" {
					out[i].Name = c.Name
				}
				if out[i].Artist == "" && c.Artist != "" {
					out[i].Artist = c.Artist
				}
				if out[i].DurationMS <= 0 && c.DurationMS > 0 {
					out[i].DurationMS = c.DurationMS
				}
			}
		}
	}
	return out
}

const librespotQueueWindow = 32

func mergeQueueWithRest(prev, next []spotify.QueueItem, cache map[string]spotify.QueueItem, preserveTail bool) []spotify.QueueItem {
	merged := mergeQueueNames(prev, next, cache)
	if !preserveTail || len(next) > librespotQueueWindow || len(prev) <= librespotQueueWindow {
		return merged
	}
	seen := make(map[string]struct{}, len(merged))
	for _, q := range merged {
		if q.ID != "" {
			seen[normalizeQueueID(q.ID)] = struct{}{}
		}
	}
	for i := librespotQueueWindow; i < len(prev); i++ {
		if prev[i].ID != "" {
			if _, dup := seen[normalizeQueueID(prev[i].ID)]; dup {
				continue
			}
		}
		merged = append(merged, prev[i])
	}
	return merged
}

func (m *model) rebuildPreloadedFromQueue() {
	if m.preloadedTrackIDs == nil {
		m.preloadedTrackIDs = make(map[string]struct{}, len(m.queue))
	} else {
		for k := range m.preloadedTrackIDs {
			delete(m.preloadedTrackIDs, k)
		}
	}
	for _, q := range m.queue {
		if q.ID != "" {
			m.preloadedTrackIDs[normalizeQueueID(q.ID)] = struct{}{}
		}
	}
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
	}
	for id := range m.preloadedTrackIDs {
		delete(m.preloadedTrackIDs, id)
	}
	for id := range m.trackCache {
		delete(m.trackCache, id)
	}
}

func (m *model) maybeLoadMorePlaylistTracksCmd(limit int) tea.Cmd {
	if !m.shouldLoadPlaylistTracks() || limit <= 0 || m.activePlaylistID == "" || !m.activePlaylistTrackHasMore || m.activePlaylistTrackLoading || m.status == nil || m.status.TrackID == "" {
		return nil
	}
	currentNorm := normalizeQueueID(m.status.TrackID)
	currentIndex := -1
	for i, trackID := range m.activePlaylistTrackIDs {
		if normalizeQueueID(trackID) == currentNorm {
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
	if m.status.ShuffleState {
		return nil
	}

	currentNorm := normalizeQueueID(m.status.TrackID)
	currentIndex := -1
	for i, trackID := range m.activePlaylistTrackIDs {
		if normalizeQueueID(trackID) == currentNorm {
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
			blocked[normalizeQueueID(q.ID)] = struct{}{}
		}
	}
	for trackID := range m.preloadedTrackIDs {
		blocked[normalizeQueueID(trackID)] = struct{}{}
	}
	blocked[currentNorm] = struct{}{}

	out := make([]string, 0, limit)
	for i := currentIndex + 1; i < len(m.activePlaylistTrackIDs) && len(out) < limit; i++ {
		trackID := m.activePlaylistTrackIDs[i]
		if trackID == "" {
			continue
		}
		norm := normalizeQueueID(trackID)
		if _, exists := blocked[norm]; exists {
			continue
		}
		out = append(out, trackID)
		blocked[norm] = struct{}{}
	}
	return out
}
