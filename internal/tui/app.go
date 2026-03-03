package tui

import (
	"context"
	"errors"
	"log/slog"
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
type modalKind string

const (
	screenPlaylist          screen = "playlist"
	screenPlayback          screen = "playback"
	playlistTrackPageSize          = 100
	queuePollEvery                 = 4
	playlistLoadBatchSize          = 20
	playlistLoadMax                = 500
	playlistTrackPreloadMax        = 500
	coverPreloadWindow             = 14
	imageLoadRetryMax              = 4
	uiTickInterval                 = 200 * time.Millisecond
	navDebounceInterval            = 120 * time.Millisecond
	volSeekDebounceInterval        = 150 * time.Millisecond
	volSettleWindow                = 3 * time.Second
	seekSettleWindow               = 1200 * time.Millisecond
	reconcileActionWindow          = 2 * time.Second
	actionFastPollWindow           = 3 * time.Second
	idlePollBackoffMax             = 5 * time.Second

	modalKindNone     modalKind = ""
	modalKindPlaylist modalKind = "playlist"
	modalKindHelp     modalKind = "help"
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
	modalKind      modalKind
	navToken       int
	actionInFlight bool

	volDebouncePending  int
	volDebounceToken    int
	volSentAt           time.Time
	volSentTarget       int
	seekSentAt          time.Time
	seekSentTarget      int
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
	imageRetryCount               map[string]int
	imageRetryToken               map[string]int
	playlistsLoading              bool
	playlistsExhausted            bool
	albumsForbidden               bool
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
	return p.summary.Name + " " + p.summary.Owner + " " + p.summary.Kind
}

func (p playlistItem) Description() string {
	switch p.summary.Kind {
	case spotify.ContextKindAlbum:
		return "album by " + p.summary.Owner
	default:
		return "playlist by " + p.summary.Owner
	}
}

func newModel(ctx context.Context, catalog spotify.PlaylistCatalog, service *spotify.Service, cfg config.Config, tuiCmdCh chan librespot.TUICommand) model {
	delegate := newPlaylistDelegate()
	modalDelegate := newPlaylistModalDelegate(false)

	browser := list.New(nil, delegate, 40, 20)
	browser.Title = "Library"
	browser.SetShowStatusBar(true)
	browser.SetFilteringEnabled(true)
	browser.SetShowFilter(true)
	browser.SetShowHelp(false)
	browser.FilterInput.Prompt = "Search: "
	applyListStyles(&browser)

	modal := list.New(nil, modalDelegate, 40, 20)
	modal.Title = ""
	modal.SetShowTitle(false)
	modal.SetShowStatusBar(false)
	modal.SetFilteringEnabled(true)
	modal.SetShowFilter(true)
	modal.SetShowHelp(false)
	modal.FilterInput.Prompt = "Search: "
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
		modalKind:           modalKindNone,
		playlistList:        browser,
		modalList:           modal,
		imgs:                newImgCache(),
		preloadedTrackIDs:   make(map[string]struct{}),
		trackCache:          make(map[string]spotify.QueueItem),
		imageRetryCount:     make(map[string]int),
		imageRetryToken:     make(map[string]int),
		playlistsLoading:    true,
		nerdFonts:           cfg.NerdFonts,
		volDebouncePending:  -1,
		seekDebouncePending: -1,
		volSentTarget:       -1,
		seekSentTarget:      -1,
		help:                h,
		keys:                newKeys(),
	}
}

func (m *model) syncModalDelegate() {
	searching := m.modalList.FilterState() != list.Unfiltered
	m.modalList.SetDelegate(newPlaylistModalDelegate(searching))
}

func selectedImageURLFromList(l list.Model) string {
	sel, ok := l.SelectedItem().(playlistItem)
	if !ok {
		return ""
	}
	return sel.summary.ImageURL
}

func (m model) needsImageURL(url string) bool {
	if url == "" {
		return false
	}
	if m.status != nil && m.status.AlbumImageURL == url {
		return true
	}
	if sel, ok := m.selectedPlaylist(); ok && sel.summary.ImageURL == url {
		return true
	}
	if m.modal {
		if selURL := selectedImageURLFromList(m.modalList); selURL == url {
			return true
		}
	}
	for _, pl := range m.visiblePlaylistItems() {
		if pl.summary.ImageURL == url {
			return true
		}
	}
	return false
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
	// Playback-state pushes don't carry a pre-override device volume, so this
	// path can only clear the settle guard by timeout.
	if m.volSentTarget >= 0 && time.Since(m.volSentAt) >= volSettleWindow {
		m.volSentTarget = -1
	}
	incomingProgress := -1
	if msg.status != nil {
		incomingProgress = msg.status.ProgressMS
	}
	if m.shouldApplySeekSettle(msg.status) {
		msg.status.ProgressMS = m.clampSeekTarget(m.seekSettleProgress())
	}
	m.clearSeekSettleTarget(incomingProgress)
	m.status = mergeStatusFromPrevious(prevStatus, m.queue, msg.status)
	prevTrackID := ""
	if prevStatus != nil {
		prevTrackID = normalizeQueueID(prevStatus.TrackID)
	}
	nextTrackID := ""
	if msg.status != nil {
		nextTrackID = normalizeQueueID(msg.status.TrackID)
	}
	if prevTrackID != "" && nextTrackID != "" && nextTrackID != prevTrackID {
		m.seekDebouncePending = -1
		m.seekSentTarget = -1
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
	prevTrackID := ""
	if m.status != nil {
		prevAlbumURL = m.status.AlbumImageURL
		prevTrackID = normalizeQueueID(m.status.TrackID)
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
	incomingProgress := -1
	if msg.status != nil {
		incomingProgress = msg.status.ProgressMS
	}
	if m.shouldApplySeekSettle(msg.status) {
		msg.status.ProgressMS = m.clampSeekTarget(m.seekSettleProgress())
	}
	// Clear guard only when the device actually reports the target (real confirmation),
	// not after the override above, which would always match.
	m.clearVolumeSettleTarget(incomingVol)
	m.clearSeekSettleTarget(incomingProgress)
	if msg.status != nil {
		nextTrackID := normalizeQueueID(msg.status.TrackID)
		if prevTrackID != "" && nextTrackID != "" && nextTrackID != prevTrackID {
			m.seekDebouncePending = -1
			m.seekSentTarget = -1
		}
	}
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
		m.seekSentTarget = -1
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
	prevTrackID := ""
	if m.status != nil {
		prevAlbumURL = m.status.AlbumImageURL
		prevTrackID = normalizeQueueID(m.status.TrackID)
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
	incomingProgress := -1
	if msg.status != nil {
		incomingProgress = msg.status.ProgressMS
	}
	if m.shouldApplySeekSettle(msg.status) {
		msg.status.ProgressMS = m.clampSeekTarget(m.seekSettleProgress())
	}
	m.clearVolumeSettleTarget(reconciledVol)
	m.clearSeekSettleTarget(incomingProgress)
	if msg.status != nil {
		nextTrackID := normalizeQueueID(msg.status.TrackID)
		if prevTrackID != "" && nextTrackID != "" && nextTrackID != prevTrackID {
			m.seekDebouncePending = -1
			m.seekSentTarget = -1
		}
	}
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
		if m.modal && m.modalKind == modalKindHelp {
			m.modal = false
			m.modalKind = modalKindNone
			return m, nil
		}
		m.modal = true
		m.modalKind = modalKindHelp
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
	if m.modalKind == modalKindHelp {
		switch {
		case keyMatches(msg, k.CloseModal), keyMatches(msg, k.ToggleHelp):
			m.modal = false
			m.modalKind = modalKindNone
			return m, nil
		}
		return m, nil
	}
	if m.modalList.FilterState() == list.Filtering {
		prevURL := selectedImageURLFromList(m.modalList)
		var cmd tea.Cmd
		m.modalList, cmd = m.modalList.Update(msg)
		m.syncModalDelegate()
		nextURL := selectedImageURLFromList(m.modalList)
		cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
		if nextURL != "" && nextURL != prevURL {
			cmds = append(cmds, m.loadImageCmd(nextURL))
		}
		return m, tea.Batch(cmds...)
	}
	switch {
	case keyMatches(msg, k.CloseModal):
		m.modal = false
		m.modalKind = modalKindNone
		m.modalList.ResetFilter()
		m.syncModalDelegate()
		return m, nil

	case keyMatches(msg, k.Select):
		sel, ok := m.modalList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel, "play-from-modal")
	}

	prevURL := selectedImageURLFromList(m.modalList)
	var cmd tea.Cmd
	m.modalList, cmd = m.modalList.Update(msg)
	m.syncModalDelegate()
	nextURL := selectedImageURLFromList(m.modalList)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
	if nextURL != "" && nextURL != prevURL {
		cmds = append(cmds, m.loadImageCmd(nextURL))
	}
	return m, tea.Batch(cmds...)
}

func (m model) handlePlaylistKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	if m.playlistList.FilterState() == list.Filtering {
		prevURL := selectedImageURLFromList(m.playlistList)
		var cmd tea.Cmd
		m.playlistList, cmd = m.playlistList.Update(msg)
		nextURL := selectedImageURLFromList(m.playlistList)
		cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
		if nextURL != "" && nextURL != prevURL {
			cmds = append(cmds, m.loadImageCmd(nextURL))
		}
		return m, tea.Batch(cmds...)
	}
	switch {
	case keyMatches(msg, k.Refresh):
		m.playlistsLoading = true
		m.playlistsExhausted = false
		m.albumsForbidden = false
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

	prevURL := selectedImageURLFromList(m.playlistList)
	var cmd tea.Cmd
	m.playlistList, cmd = m.playlistList.Update(msg)
	nextURL := selectedImageURLFromList(m.playlistList)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
	if nextURL != "" && nextURL != prevURL {
		cmds = append(cmds, m.loadImageCmd(nextURL))
	}
	return m, tea.Batch(cmds...)
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
		m.modalKind = modalKindPlaylist
		m.modalList.ResetFilter()
		m.syncModalDelegate()
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
		m.beginReconcileAction(reconcileActionWindow)
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
		m.applyOptimisticSkip(true)
		m.beginReconcileAction(reconcileActionWindow)
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
		m.applyOptimisticSkip(false)
		m.beginReconcileAction(reconcileActionWindow)
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Previous(ctx, m.deviceName)
		}, rollback)

	case keyMatches(msg, k.Shuffle):
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandShuffle}:
			default:
			}
			m.clearPreloadedTracks()
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
		m.clearPreloadedTracks()
		m.stableQueueLen = len(m.queue)
		m.beginReconcileAction(0)
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.Shuffle(ctx, m.deviceName, nextShuffle)
		}, rollback)

	case keyMatches(msg, k.Loop):
		if m.status == nil || m.actionInFlight {
			return m, nil
		}
		rollback := cloneStatus(m.status)
		m.status.RepeatContext, m.status.RepeatTrack = nextRepeatMode(m.status.RepeatContext, m.status.RepeatTrack)
		if m.tuiCmdCh != nil {
			select {
			case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandCycleRepeat}:
			default:
			}
			return m, nil
		}
		if m.service == nil {
			return m, nil
		}
		m.beginReconcileAction(reconcileActionWindow)
		state := repeatModeString(m.status.RepeatContext, m.status.RepeatTrack)
		return m, m.actionWithReconcileCmd(func(ctx context.Context) error {
			return m.service.SetRepeat(ctx, m.deviceName, state)
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
		current := m.seekSettleProgress()
		target := m.clampSeekTarget(current - 5000)
		if target == current {
			return m, nil
		}
		m.status.ProgressMS = target
		m.seekDebouncePending = target
		m.seekDebounceToken++
		return m, m.seekDebounceCmd(m.seekDebounceToken)

	case keyMatches(msg, k.SeekFwd):
		if m.actionInFlight || m.status == nil {
			return m, nil
		}
		current := m.seekSettleProgress()
		target := m.clampSeekTarget(current + 5000)
		if target == current {
			return m, nil
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
	m.modalKind = modalKindNone
	m.playbackErr = nil
	isPlaylist := sel.summary.Kind != spotify.ContextKindAlbum
	canReadTracks := isPlaylist && m.shouldLoadPlaylistTracks() && m.canReadPlaylistTracks(sel.summary)
	activeID := ""
	ownerID := ""
	collaborative := false
	if isPlaylist {
		activeID = sel.summary.ID
		ownerID = sel.summary.OwnerID
		collaborative = sel.summary.Collaborative
	}
	m.setActivePlaylist(activeID, canReadTracks, ownerID, collaborative)
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
	m.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
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

func nextRepeatMode(repeatContext, repeatTrack bool) (bool, bool) {
	if repeatTrack {
		return false, false
	}
	if repeatContext {
		return false, true
	}
	return true, false
}

func repeatModeString(repeatContext, repeatTrack bool) string {
	if repeatTrack {
		return "track"
	}
	if repeatContext {
		return "context"
	}
	return "off"
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

func (m *model) scheduleNavDebounceCmd() tea.Cmd {
	m.navToken++
	return m.navDebounceCmd(m.navToken)
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
