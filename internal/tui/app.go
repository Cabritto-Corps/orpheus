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

	"orpheus/internal/cache"
	"orpheus/internal/config"
	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

type tab string

const (
	tabPlaylists                  tab = "playlists"
	tabAlbums                     tab = "albums"
	tabPlayer                     tab = "player"
	playlistItemPageSize              = 100
	queuePollEvery                    = 4
	playlistLoadBatchSize             = 25
	playlistLoadMax                   = 500
	playlistItemPreloadMax            = 500
	coverPreloadWindow                = 14
	imageLoadRetryMax                 = 4
	coverRefreshEvery                 = 25
	playerCoverRefreshEvery           = 10
	libraryCoverRefreshEvery          = 150
	libraryCoverRefreshBatch          = 32
	libraryMetaRefreshEvery           = 300
	coverQueueDrainBatch              = 12
	kittyProtocolFallbackFailures     = 8
	trackMetadataTTL                  = 2 * time.Hour
	uiTickInterval                    = 200 * time.Millisecond
	navDebounceInterval               = 120 * time.Millisecond
	volSeekDebounceInterval           = 150 * time.Millisecond
	volSettleWindow                   = 3 * time.Second
	seekSettleWindow                  = 1200 * time.Millisecond
	reconcileActionWindow             = 2 * time.Second
	actionFastPollWindow              = 3 * time.Second
	idlePollBackoffMax                = 5 * time.Second
)

type model struct {
	ctx        context.Context
	catalog    spotify.PlaylistCatalog
	service    *spotify.Service
	deviceName string
	tuiCmdCh   chan librespot.TUICommand

	pollInterval            time.Duration
	pollTick                int
	pollElapsed             time.Duration
	coverRefreshTick        int
	playerCoverRefreshTick  int
	libraryCoverRefreshTick int
	libraryMetaRefreshTick  int
	actionFastPollUntil     time.Time

	activeTab      tab
	helpOpen       bool
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

	status                       *spotify.PlaybackStatus
	queue                        []spotify.QueueItem
	queueHasMore                 bool
	stableQueueLen               int
	pendingContextFrom           string
	pendingContextFromAt         time.Time
	transportTransitionPending   bool
	transportTransitionFromTrack string
	transportTransitionStartedAt time.Time
	transportRecoveryPending     bool
	transportStuckCount          int
	inputQueue                   []playbackInput
	executorState                commandExecutorState

	activePlaylistID             string
	activePlaylistOwnerID        string
	activePlaylistCollaborative  bool
	activePlaylistItemIDs        []string
	activePlaylistItemNextOffset int
	activePlaylistItemHasMore    bool
	activePlaylistItemLoading    bool
	activePlaylistLoadToken      int
	preloadedItemIDs             map[string]struct{}
	trackCache                   *cache.TTL[string, spotify.QueueItem]
	imageRetryCount              map[string]int
	imageRetryToken              map[string]int
	coverResolveInFlight         map[string]struct{}
	coverQueue                   []string
	coverQueued                  map[string]struct{}
	coverStats                   coverPipelineStats
	playerCoverFailStreak        int
	playlistsLoading             bool
	playlistsExhausted           bool
	albumsForbidden              bool
	playlistsErr                 error
	playlistsRetryCount          int
	playbackErr                  error
	playlistItemRetryCount       int
	currentUserID                string

	playlistList list.Model
	albumList    list.Model

	imgs *imgCache

	width     int
	height    int
	nerdFonts bool

	help help.Model
	keys keyMap
}

type coverPipelineStats struct {
	Enqueued      uint64
	Launched      uint64
	Loaded        uint64
	Failed        uint64
	Retried       uint64
	ResolveOK     uint64
	ResolveFailed uint64
	Skipped       uint64
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

	browser := list.New(nil, delegate, 40, 20)
	browser.SetShowTitle(false)
	browser.SetShowStatusBar(false)
	browser.SetFilteringEnabled(true)
	browser.SetShowFilter(true)
	browser.SetShowHelp(false)
	browser.FilterInput.Prompt = "Search: "
	applyListStyles(&browser)

	albums := list.New(nil, delegate, 40, 20)
	albums.SetShowTitle(false)
	albums.SetShowStatusBar(false)
	albums.SetFilteringEnabled(true)
	albums.SetShowFilter(true)
	albums.SetShowHelp(false)
	albums.FilterInput.Prompt = "Search: "
	applyListStyles(&albums)

	h := newHelp()

	return model{
		ctx:                  ctx,
		catalog:              catalog,
		service:              service,
		deviceName:           cfg.DeviceName,
		tuiCmdCh:             tuiCmdCh,
		pollInterval:         cfg.PollInterval,
		activeTab:            tabPlaylists,
		playlistList:         browser,
		albumList:            albums,
		imgs:                 newImgCache(),
		preloadedItemIDs:     make(map[string]struct{}),
		trackCache:           cache.NewTTL[string, spotify.QueueItem](4096, trackMetadataTTL),
		imageRetryCount:      make(map[string]int),
		imageRetryToken:      make(map[string]int),
		coverResolveInFlight: make(map[string]struct{}),
		coverQueued:          make(map[string]struct{}),
		playlistsLoading:     true,
		nerdFonts:            cfg.NerdFonts,
		volDebouncePending:   -1,
		seekDebouncePending:  -1,
		volSentTarget:        -1,
		seekSentTarget:       -1,
		help:                 h,
		keys:                 newKeys(),
	}
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
	if m.libraryHasImageURL(url) {
		return true
	}
	return false
}

func (m model) libraryHasImageURL(url string) bool {
	if url == "" {
		return false
	}
	for _, item := range m.playlistList.Items() {
		pl, ok := item.(playlistItem)
		if ok && pl.summary.ImageURL == url {
			return true
		}
	}
	for _, item := range m.albumList.Items() {
		al, ok := item.(playlistItem)
		if ok && al.summary.ImageURL == url {
			return true
		}
	}
	return false
}

func (m model) hasMissingLibraryImageURLs() bool {
	for _, item := range m.playlistList.Items() {
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		if strings.TrimSpace(pl.summary.ImageURL) == "" {
			return true
		}
	}
	for _, item := range m.albumList.Items() {
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

func coverResolveKey(kind, id string) string {
	return strings.TrimSpace(kind) + ":" + strings.TrimSpace(id)
}

func (m *model) queueCoverResolveCmd(kind, id string) tea.Cmd {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind == "" || id == "" {
		return nil
	}
	key := coverResolveKey(kind, id)
	if _, exists := m.coverResolveInFlight[key]; exists {
		return nil
	}
	cmd := m.resolveContextImageURLCmd(kind, id)
	if cmd == nil {
		return nil
	}
	m.coverResolveInFlight[key] = struct{}{}
	return cmd
}

func (m *model) queueMissingLibraryImageResolvesCmd(limit int) tea.Cmd {
	if limit <= 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, limit)
	for _, item := range m.playlistList.Items() {
		if len(cmds) >= limit {
			break
		}
		pl, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(pl.summary.ImageURL) != "" {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindPlaylist, pl.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	for _, item := range m.albumList.Items() {
		if len(cmds) >= limit {
			break
		}
		al, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(al.summary.ImageURL) != "" {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindAlbum, al.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) queueResolvesForImageURLCmd(url string, limit int) tea.Cmd {
	url = strings.TrimSpace(url)
	if url == "" || limit <= 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, limit)
	for _, item := range m.playlistList.Items() {
		if len(cmds) >= limit {
			break
		}
		pl, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(pl.summary.ImageURL) != url {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindPlaylist, pl.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	for _, item := range m.albumList.Items() {
		if len(cmds) >= limit {
			break
		}
		al, ok := item.(playlistItem)
		if !ok || strings.TrimSpace(al.summary.ImageURL) != url {
			continue
		}
		if cmd := m.queueCoverResolveCmd(spotify.ContextKindAlbum, al.summary.ID); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) enqueueCoverURL(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		return
	}
	if _, exists := m.coverQueued[url]; exists {
		return
	}
	m.coverQueued[url] = struct{}{}
	m.coverQueue = append(m.coverQueue, url)
	m.coverStats.Enqueued++
}

func (m *model) drainCoverQueueCmd(limit int) tea.Cmd {
	if limit <= 0 {
		limit = coverQueueDrainBatch
	}
	cmds := make([]tea.Cmd, 0, limit)
	for len(m.coverQueue) > 0 && len(cmds) < limit {
		url := m.coverQueue[0]
		m.coverQueue = m.coverQueue[1:]
		delete(m.coverQueued, url)
		if !m.imgs.shouldQueueLoad(url) {
			m.coverStats.Skipped++
			continue
		}
		cmd := m.loadImageCmd(url)
		if cmd == nil {
			m.coverStats.Skipped++
			continue
		}
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *model) maybeFallbackFromKittyOnPlayerFailures(url string) {
	if m.imgs == nil || m.imgs.protocol != imageProtocolKitty {
		return
	}
	if m.status == nil || strings.TrimSpace(m.status.AlbumImageURL) == "" {
		return
	}
	if strings.TrimSpace(m.status.AlbumImageURL) != strings.TrimSpace(url) {
		return
	}
	if m.playerCoverFailStreak < kittyProtocolFallbackFailures {
		return
	}
	m.imgs.protocol = imageProtocolNone
	m.playerCoverFailStreak = 0
	slog.Warn("disabling kitty image protocol after repeated player cover failures", "url", url)
}

func (m *model) applyResolvedContextImageURL(kind, id, imageURL string) bool {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	imageURL = strings.TrimSpace(imageURL)
	if kind == "" || id == "" || imageURL == "" {
		return false
	}
	updated := false
	switch kind {
	case spotify.ContextKindPlaylist:
		items := m.playlistList.Items()
		for i, item := range items {
			pl, ok := item.(playlistItem)
			if !ok || pl.summary.ID != id {
				continue
			}
			if strings.TrimSpace(pl.summary.ImageURL) == imageURL {
				return false
			}
			pl.summary.ImageURL = imageURL
			items[i] = pl
			updated = true
			break
		}
		if updated {
			m.playlistList.SetItems(items)
		}
	case spotify.ContextKindAlbum:
		items := m.albumList.Items()
		for i, item := range items {
			al, ok := item.(playlistItem)
			if !ok || al.summary.ID != id {
				continue
			}
			if strings.TrimSpace(al.summary.ImageURL) == imageURL {
				return false
			}
			al.summary.ImageURL = imageURL
			items[i] = al
			updated = true
			break
		}
		if updated {
			m.albumList.SetItems(items)
		}
	}
	return updated
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
	inVolSettle := m.volDebouncePending >= 0 ||
		(m.volSentTarget >= 0 && time.Since(m.volSentAt) < volSettleWindow)
	if inVolSettle && msg.status != nil && prevStatus != nil {
		msg.status.Volume = prevStatus.Volume
	}
	if inVolSettle && msg.status != nil && m.volSentTarget >= 0 {
		msg.status.Volume = m.volSentTarget
	}
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
	prevShuffleState := false
	if prevStatus != nil {
		prevShuffleState = prevStatus.ShuffleState
	}
	shuffleChanged := msg.status != nil && msg.status.ShuffleState != prevShuffleState
	newShuffleActive := msg.status != nil && msg.status.ShuffleState
	if m.shouldApplyIncomingQueue(nextTrackID) {
		m.applyMergedQueue(msg.queue, msg.queueHasMore, refreshQueue || shuffleChanged, true, newShuffleActive)
	}
	m.status = mergeStatusFromPrevious(prevStatus, m.queue, msg.status, m.trackCache)
	m.maybeClearTransportTransition(m.status)
	m.playbackErr = nil
	cmds := []tea.Cmd{}
	if shouldQueueAlbumImageLoad(prevStatus, m.status) {
		cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
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
		m.transportTransitionPending = false
		m.syncExecutorState()
		return m, m.pumpInputExecutor()
	}
	m.pollElapsed = 0
	prevStatus := m.status
	prevTrackID := ""
	if m.status != nil {
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
	m.maybeClearTransportTransition(m.status)
	m.playbackErr = nil
	if msg.queueFetched {
		incomingTrack := ""
		if msg.status != nil {
			incomingTrack = normalizeQueueID(msg.status.TrackID)
		}
		if m.shouldApplyIncomingQueue(incomingTrack) {
			m.applyMergedQueue(msg.queue, false, true, true, m.status != nil && m.status.ShuffleState)
		}
	}
	if msg.queueErr != nil {
		m.playbackErr = msg.queueErr
		slog.Error("fetch queue failed", "error", msg.queueErr)
	}
	m.status = mergeStatusFromPrevious(prevStatus, m.queue, m.status, m.trackCache)

	cmds := make([]tea.Cmd, 0, 3)
	if shouldQueueAlbumImageLoad(prevStatus, m.status) {
		cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
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
	m.actionInFlight = false
	m.syncExecutorState()
	if msg.err != nil {
		m.transportTransitionPending = false
		m.syncExecutorState()
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
		return m, m.pumpInputExecutor()
	}
	if msg.status == nil {
		return m, m.reconcileCmd()
	}
	m.playbackErr = nil
	m.pollElapsed = 0
	prevStatus := m.status
	prevTrackID := ""
	if m.status != nil {
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
	m.maybeClearTransportTransition(m.status)
	if msg.queueFetched {
		incomingTrack := ""
		if msg.status != nil {
			incomingTrack = normalizeQueueID(msg.status.TrackID)
		}
		if m.shouldApplyIncomingQueue(incomingTrack) {
			m.applyMergedQueue(msg.queue, false, true, true, m.status != nil && m.status.ShuffleState)
		}
	}
	m.status = mergeStatusFromPrevious(prevStatus, m.queue, m.status, m.trackCache)
	cmds := make([]tea.Cmd, 0, 3)
	if shouldQueueAlbumImageLoad(prevStatus, m.status) {
		cmds = append(cmds, m.loadImageCmd(m.status.AlbumImageURL))
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

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys

	switch {
	case keyMatches(msg, k.Quit):
		return m, tea.Quit
	case keyMatches(msg, k.ToggleHelp):
		m.helpOpen = !m.helpOpen
		return m, nil
	}

	if m.helpOpen {
		if keyMatches(msg, k.CloseModal) {
			m.helpOpen = false
		}
		return m, nil
	}

	if keyMatches(msg, k.Tab) {
		filtering := (m.activeTab == tabPlaylists && m.playlistList.FilterState() == list.Filtering) ||
			(m.activeTab == tabAlbums && m.albumList.FilterState() == list.Filtering)
		if !filtering {
			switch m.activeTab {
			case tabPlaylists:
				m.activeTab = tabAlbums
			case tabAlbums:
				m.activeTab = tabPlayer
			case tabPlayer:
				m.activeTab = tabPlaylists
			}
			m.coverRefreshTick = 0
			return m, m.loadVisiblePlaylistCoversCmd()
		}
	}

	switch m.activeTab {
	case tabPlaylists:
		return m.handlePlaylistKey(msg)
	case tabAlbums:
		return m.handleAlbumKey(msg)
	default:
		return m.handlePlaybackKey(msg)
	}
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

func (m model) handleAlbumKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	if m.albumList.FilterState() == list.Filtering {
		prevURL := selectedImageURLFromList(m.albumList)
		var cmd tea.Cmd
		m.albumList, cmd = m.albumList.Update(msg)
		nextURL := selectedImageURLFromList(m.albumList)
		cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
		if nextURL != "" && nextURL != prevURL {
			cmds = append(cmds, m.loadImageCmd(nextURL))
		}
		return m, tea.Batch(cmds...)
	}
	switch {
	case keyMatches(msg, k.Select):
		sel, ok := m.albumList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel, "play-from-browser")
	}

	prevURL := selectedImageURLFromList(m.albumList)
	var cmd tea.Cmd
	m.albumList, cmd = m.albumList.Update(msg)
	nextURL := selectedImageURLFromList(m.albumList)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
	if nextURL != "" && nextURL != prevURL {
		cmds = append(cmds, m.loadImageCmd(nextURL))
	}
	return m, tea.Batch(cmds...)
}

func (m model) handlePlaybackKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	var action playbackInputKind
	switch {
	case keyMatches(msg, k.Refresh):
		action = playbackInputRefresh
	case keyMatches(msg, k.PlayPause):
		action = playbackInputPlayPause
	case keyMatches(msg, k.Next):
		action = playbackInputNext
	case keyMatches(msg, k.Prev):
		action = playbackInputPrev
	case keyMatches(msg, k.Shuffle):
		action = playbackInputShuffle
	case keyMatches(msg, k.Loop):
		action = playbackInputLoop
	case keyMatches(msg, k.VolUp):
		action = playbackInputVolUp
	case keyMatches(msg, k.VolDown):
		action = playbackInputVolDown
	case keyMatches(msg, k.SeekBack):
		action = playbackInputSeekBack
	case keyMatches(msg, k.SeekFwd):
		action = playbackInputSeekFwd
	default:
		return m, nil
	}
	m.enqueuePlaybackInput(action)
	return m, m.pumpInputExecutor()
}

func (m model) selectAndPlayPlaylist(sel playlistItem, action string) (tea.Model, tea.Cmd) {
	m.activeTab = tabPlayer
	m.playbackErr = nil
	isPlaylist := sel.summary.Kind != spotify.ContextKindAlbum
	canReadTracks := isPlaylist && m.shouldLoadPlaylistItems() && m.canReadPlaylistTracks(sel.summary)
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
		m.pendingContextFromAt = time.Now()
	}
	m.queue = nil
	m.queueHasMore = false
	m.stableQueueLen = 0
	if m.tuiCmdCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandPlayContext, URI: sel.summary.URI}:
			m.beginTransportTransition()
		default:
		}
		cmds := []tea.Cmd{m.loadImageCmd(sel.summary.ImageURL)}
		if canReadTracks {
			m.activePlaylistItemLoading = true
			m.activePlaylistLoadToken++
			cmds = append(cmds, m.loadPlaylistItemsCmd(sel.summary.ID, 0, m.activePlaylistLoadToken))
		}
		return m, tea.Batch(cmds...)
	}
	cmds := []tea.Cmd{
		m.actionCmd(func(ctx context.Context) error {
			return m.service.PlayPlaylist(ctx, m.deviceName, sel.summary.URI)
		}, action),
		m.loadImageCmd(sel.summary.ImageURL),
	}
	m.beginTransportTransition()
	m.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
	if canReadTracks {
		m.activePlaylistItemLoading = true
		m.activePlaylistLoadToken++
		cmds = append(cmds, m.loadPlaylistItemsCmd(sel.summary.ID, 0, m.activePlaylistLoadToken))
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

func (m *model) loadVisiblePlaylistCoversCmd() tea.Cmd {
	seen := make(map[string]struct{})

	add := func(url string) {
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		if !m.imgs.shouldQueueLoad(url) {
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
	albumItems := m.albumList.Items()
	if m.albumList.FilterState() == list.Unfiltered && len(albumItems) > 0 {
		center := clampInt(m.albumList.GlobalIndex(), 0, len(albumItems)-1)
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
		if !m.imgs.shouldQueueLoad(url) {
			return
		}
		seen[url] = struct{}{}
		m.enqueueCoverURL(url)
		added++
	}

	for _, item := range m.playlistList.Items() {
		if limit > 0 && added >= limit {
			break
		}
		pl, ok := item.(playlistItem)
		if !ok {
			continue
		}
		add(pl.summary.ImageURL)
	}
	for _, item := range m.albumList.Items() {
		if limit > 0 && added >= limit {
			break
		}
		al, ok := item.(playlistItem)
		if !ok {
			continue
		}
		add(al.summary.ImageURL)
	}

	return m.drainCoverQueueCmd(coverQueueDrainBatch)
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

func (m model) visibleAlbumItems() []playlistItem {
	visible := m.albumList.VisibleItems()
	if len(visible) == 0 {
		return nil
	}

	perPage := m.albumList.Paginator.PerPage
	if perPage <= 0 {
		perPage = len(visible)
	}
	start := m.albumList.Paginator.Page * perPage
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
	m.activePlaylistItemIDs = nil
	m.activePlaylistItemNextOffset = 0
	m.activePlaylistItemHasMore = playlistID != "" && canReadTracks
	m.activePlaylistItemLoading = false
	m.playlistItemRetryCount = 0
	if m.preloadedItemIDs == nil {
		m.preloadedItemIDs = make(map[string]struct{})
	}
	for id := range m.preloadedItemIDs {
		delete(m.preloadedItemIDs, id)
	}
	m.trackCache.Clear()
}

func (m *model) maybeLoadMorePlaylistItemsCmd(limit int) tea.Cmd {
	if !m.shouldLoadPlaylistItems() || limit <= 0 || m.activePlaylistID == "" || !m.activePlaylistItemHasMore || m.activePlaylistItemLoading || m.status == nil || m.status.TrackID == "" {
		return nil
	}
	currentNorm := normalizeQueueID(m.status.TrackID)
	currentIndex := -1
	for i, trackID := range m.activePlaylistItemIDs {
		if normalizeQueueID(trackID) == currentNorm {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return nil
	}
	if currentIndex >= 0 && len(m.activePlaylistItemIDs)-currentIndex-1 >= limit {
		return nil
	}
	m.activePlaylistItemLoading = true
	m.activePlaylistLoadToken++
	return m.loadPlaylistItemsCmd(m.activePlaylistID, m.activePlaylistItemNextOffset, m.activePlaylistLoadToken)
}

func (m model) canReadPlaylistTracks(pl spotify.PlaylistSummary) bool {
	if m.currentUserID == "" {
		return false
	}
	return pl.OwnerID == m.currentUserID || pl.Collaborative
}

func (m model) shouldLoadPlaylistItems() bool {
	return m.service != nil
}

func (m model) nextTracksToPreload(limit int) []string {
	if limit <= 0 || m.status == nil || m.status.TrackID == "" || len(m.activePlaylistItemIDs) == 0 || m.activePlaylistID == "" {
		return nil
	}
	if m.status.ShuffleState {
		return nil
	}

	currentNorm := normalizeQueueID(m.status.TrackID)
	currentIndex := -1
	for i, trackID := range m.activePlaylistItemIDs {
		if normalizeQueueID(trackID) == currentNorm {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		return nil
	}

	blocked := make(map[string]struct{}, len(m.queue)+len(m.preloadedItemIDs)+1)
	for _, q := range m.queue {
		if q.ID != "" {
			blocked[normalizeQueueID(q.ID)] = struct{}{}
		}
	}
	for trackID := range m.preloadedItemIDs {
		blocked[normalizeQueueID(trackID)] = struct{}{}
	}
	blocked[currentNorm] = struct{}{}

	out := make([]string, 0, limit)
	for i := currentIndex + 1; i < len(m.activePlaylistItemIDs) && len(out) < limit; i++ {
		trackID := m.activePlaylistItemIDs[i]
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
