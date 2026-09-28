package tui

import (
	"errors"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// See syncListFilterBinding: the key-capture flow replaces m.ui.keys wholesale.
	m.syncListFilterBinding()
	k := m.ui.keys
	filtering := m.isFiltering()

	// ctrl+c is quit's guaranteed key: it punches through modals, key
	// capture and filter mode alike.
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// An open modal owns every other key (focus trap): the global keys
	// below stay inert while help, settings or the popup is up, and Esc
	// always closes. Pressing ? with help open dismisses it; entering
	// help or settings from inside another modal is not possible.
	if m.ui.helpOpen {
		if keyMatches(msg, k.CloseModal) || keyMatches(msg, k.ToggleHelp) {
			m.ui.helpOpen = false
		}
		switch {
		case keyMatches(msg, k.QueueUp):
			// Reassign: scrollHelp has a value receiver, so discarding its
			// return silently threw the scrolled copy away.
			m = m.scrollHelp(-3)
		case keyMatches(msg, k.QueueDown):
			m = m.scrollHelp(3)
		}
		return m, nil
	}

	if m.ui.settings.open {
		return m.handleSettingsKey(msg)
	}

	if m.ui.trackPopupOpen {
		return m.handleTrackPopupKey(msg)
	}

	switch {
	case keyMatches(msg, k.Quit):
		if !filtering {
			return m, tea.Quit
		}
	case keyMatches(msg, k.ToggleHelp):
		if !filtering {
			m.ui.helpOpen = !m.ui.helpOpen
			if m.ui.helpOpen {
				m.ensureHelpViewport()
			}
			return m, nil
		}
	case keyMatches(msg, k.Settings):
		if !filtering {
			return m.openSettings()
		}
	}

	if keyMatches(msg, k.Tab) {
		if !filtering {
			switch m.ui.activeTab {
			case tabPlaylists:
				m.ui.activeTab = tabAlbums
			case tabAlbums:
				m.ui.activeTab = tabPlayer
			case tabPlayer:
				m.ui.activeTab = tabPlaylists
			}
			m.normalizeLibraryPagination()
			m.ui.coverRefreshTick = 0
			return m, m.loadVisiblePlaylistCoversCmd()
		}
	}

	// Global playback keys: work on all tabs, not just player
	if !filtering {
		if action := m.matchGlobalPlaybackKey(msg); action != "" {
			m.enqueuePlaybackInput(action)
			return m, m.pumpInputExecutor()
		}
	}

	switch m.ui.activeTab {
	case tabPlaylists:
		return m.handlePlaylistKey(msg)
	case tabAlbums:
		return m.handleAlbumKey(msg)
	default:
		return m.handlePlaybackKey(msg)
	}
}

func (m model) handlePlaylistKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if m.browse.playlistList.FilterState() == list.Filtering {
		prevURL := selectedImageURLFromList(m.browse.playlistList)
		var cmd tea.Cmd
		m.browse.playlistList, cmd = m.browse.playlistList.Update(msg)
		nextURL := selectedImageURLFromList(m.browse.playlistList)
		cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
		if nextURL != "" && nextURL != prevURL {
			cmds = append(cmds, m.loadImageCmd(nextURL, false))
		}
		return m, tea.Batch(cmds...)
	}
	if keyMatches(msg, k.PlayPause) {
		if sel, ok := m.browse.playlistList.SelectedItem().(playlistItem); ok {
			return m.openTrackPopup(sel)
		}
		return m, nil
	}
	switch {
	case keyMatches(msg, k.Refresh):
		m.browse.playlistsLoading = true
		m.browse.albumsForbidden = false
		m.browse.playlistsErr = nil
		m.browse.playlistsRetryCount = 0
		return m, m.loadPlaylistsCmd()

	case keyMatches(msg, k.Select):
		sel, ok := m.browse.playlistList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel)
	}

	prevURL := selectedImageURLFromList(m.browse.playlistList)
	var cmd tea.Cmd
	m.browse.playlistList, cmd = m.browse.playlistList.Update(msg)
	nextURL := selectedImageURLFromList(m.browse.playlistList)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
	if nextURL != "" && nextURL != prevURL {
		cmds = append(cmds, m.loadImageCmd(nextURL, false))
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleAlbumKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if m.browse.albumList.FilterState() == list.Filtering {
		prevURL := selectedImageURLFromList(m.browse.albumList)
		var cmd tea.Cmd
		m.browse.albumList, cmd = m.browse.albumList.Update(msg)
		nextURL := selectedImageURLFromList(m.browse.albumList)
		cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
		if nextURL != "" && nextURL != prevURL {
			cmds = append(cmds, m.loadImageCmd(nextURL, false))
		}
		return m, tea.Batch(cmds...)
	}
	if keyMatches(msg, k.PlayPause) {
		if sel, ok := m.browse.albumList.SelectedItem().(playlistItem); ok {
			return m.openTrackPopup(sel)
		}
		return m, nil
	}
	switch {
	case keyMatches(msg, k.Refresh):
		m.browse.playlistsLoading = true
		m.browse.albumsForbidden = false
		m.browse.playlistsErr = nil
		m.browse.playlistsRetryCount = 0
		return m, m.loadPlaylistsCmd()
	case keyMatches(msg, k.Select):
		sel, ok := m.browse.albumList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel)
	}

	prevURL := selectedImageURLFromList(m.browse.albumList)
	var cmd tea.Cmd
	m.browse.albumList, cmd = m.browse.albumList.Update(msg)
	nextURL := selectedImageURLFromList(m.browse.albumList)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd()}
	if nextURL != "" && nextURL != prevURL {
		cmds = append(cmds, m.loadImageCmd(nextURL, false))
	}
	return m, tea.Batch(cmds...)
}

func (m model) isFiltering() bool {
	if m.ui.trackPopupOpen && m.ui.trackPopupList.FilterState() == list.Filtering {
		return true
	}
	return (m.ui.activeTab == tabPlaylists && m.browse.playlistList.FilterState() == list.Filtering) ||
		(m.ui.activeTab == tabAlbums && m.browse.albumList.FilterState() == list.Filtering)
}

// handleQueueKey handles the up-next panel's interaction keys (player tab).
// Cursor positions and command payloads use the visible-view addressing the
// backend expects: position 0 is the entry the panel shows first.
func (m *model) handleQueueKey(msg tea.KeyMsg) tea.Cmd {
	q := m.visibleQueue()
	if len(q) == 0 {
		return nil
	}

	k := m.ui.keys
	cursor := min(m.transport.queueCursor, len(q)-1)
	switch {
	case keyMatches(msg, k.QueueUp):
		if cursor > 0 {
			m.transport.queueCursor = cursor - 1
		}
		return nil
	case keyMatches(msg, k.QueueDown):
		if cursor < len(q)-1 {
			m.transport.queueCursor = cursor + 1
		}
		return nil
	case keyMatches(msg, k.QueueJump):
		// Jump loads a track — same class as next/prev, so it must not
		// fire while a transport transition is mid-flight.
		if m.transport.transition.Pending() {
			return nil
		}
		return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueJump, QueueIndex: cursor})
	case keyMatches(msg, k.QueueRemove):
		return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueRemove, QueueIndex: cursor})
	case keyMatches(msg, k.QueueMoveUp):
		if cursor > 0 {
			return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueReorder, QueueIndex: cursor, QueueTargetIndex: cursor - 1})
		}
		return nil
	case keyMatches(msg, k.QueueMoveDown):
		if cursor < len(q)-1 {
			return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueReorder, QueueIndex: cursor, QueueTargetIndex: cursor + 1})
		}
		return nil
	default:
		return nil
	}
}

// syncListFilterBinding points every list's search binding at the
// configured filter key. Bubbles dispatches filtering off its own KeyMap,
// which the repo never otherwise touches, so without this a keys.json
// rebind of the search action would only change the help text.
func (m *model) syncListFilterBinding() {
	m.browse.playlistList.KeyMap.Filter = m.ui.keys.Filter
	m.browse.albumList.KeyMap.Filter = m.ui.keys.Filter
	m.ui.trackPopupList.KeyMap.Filter = m.ui.keys.Filter
}

func (m model) matchGlobalPlaybackKey(msg tea.KeyMsg) playbackInputKind {
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.VolUp):
		return playbackInputVolUp
	case keyMatches(msg, k.VolDown):
		return playbackInputVolDown
	default:
		return ""
	}
}

func (m model) handlePlaybackKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if cmd := m.handleQueueKey(msg); cmd != nil {
		return m, cmd
	}
	if m.shouldBlockTransportInput(msg) {
		switch {
		case keyMatches(msg, k.PlayPause):
			m.enqueuePlaybackInput(playbackInputPlayPause)
		case keyMatches(msg, k.Next):
			m.enqueuePlaybackInput(playbackInputNext)
		case keyMatches(msg, k.Prev):
			m.enqueuePlaybackInput(playbackInputPrev)
		case keyMatches(msg, k.Shuffle):
			m.enqueuePlaybackInput(playbackInputShuffle)
		case keyMatches(msg, k.Loop):
			m.enqueuePlaybackInput(playbackInputLoop)
		}
		return m, nil
	}
	var action playbackInputKind
	switch {
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

type trackPopupItemsMsg struct {
	token int
	items []spotify.QueueItem
}

// newTrackPopupList builds the track popup's list with the shared chrome:
// themed filter prompt, a readable status bar (the item count stays visible
// even on a single page) and pagination dots the framework's default greys
// bury on dark themes.
func newTrackPopupList(termW, termH int) list.Model {
	_, listW, listH := popupModalSize(termW, termH)
	popup := list.New(nil, newTrackPopupDelegate(), listW, listH)
	popup.SetShowTitle(false)
	popup.SetShowStatusBar(true)
	popup.SetFilteringEnabled(true)
	popup.SetShowFilter(true)
	popup.SetShowHelp(false)
	popup.FilterInput.Prompt = "/ "
	popup.SetStatusBarItemName("track", "tracks")
	popup.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(colorMutedBlue)
	popup.Styles.StatusBar = lipgloss.NewStyle().Foreground(colorOffWhite).PaddingLeft(1)
	popup.Styles.ActivePaginationDot = lipgloss.NewStyle().Foreground(colorBlue).SetString(" •")
	popup.Styles.InactivePaginationDot = lipgloss.NewStyle().Foreground(colorDimBlue).SetString(" •")
	return popup
}

func (m model) openTrackPopup(sel playlistItem) (tea.Model, tea.Cmd) {
	m.ui.trackPopupOpen = true
	m.ui.trackPopupReqToken++
	m.ui.trackPopupWaitTicks = 0
	m.ui.trackPopupKind = sel.summary.Kind
	m.ui.trackPopupID = sel.summary.ID
	m.ui.trackPopupURI = sel.summary.URI
	m.ui.trackPopupName = sel.summary.Name
	m.ui.trackPopupItems = nil

	popup := newTrackPopupList(m.ui.width, m.ui.height)
	m.ui.trackPopupList = popup
	m.ui.trackPopupWidth = m.ui.trackPopupList.Width() - 4

	if m.tuiCmdCh != nil && m.contextTracksCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{
			Kind:     librespot.TUICommandGetContextTracks,
			URI:      sel.summary.URI,
			ReqToken: m.ui.trackPopupReqToken,
			ResultCh: m.contextTracksCh,
		}:
		default:
			// The backend queue is full; without a reply the popup would
			// sit on "Loading…" forever.
			m.ui.trackPopupOpen = false
			m.transport.playbackErr = errors.New("couldn't load tracks — player busy")
		}
	} else {
		m.ui.trackPopupItems = []spotify.QueueItem{}
	}
	return m, nil
}

func (m model) handleTrackPopupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if m.ui.trackPopupList.FilterState() == list.Filtering {
		// While searching inside the popup everything goes to the filter:
		// bubbles exits the filter on the first esc and accepts on enter,
		// so the close and play actions below only see keys typed outside
		// of search mode.
		var cmd tea.Cmd
		m.ui.trackPopupList, cmd = m.ui.trackPopupList.Update(msg)
		return m, cmd
	}
	switch {
	case keyMatches(msg, k.CloseModal):
		m.ui.trackPopupOpen = false
		return m, nil
	case keyMatches(msg, k.Quit):
		// The bubbles list binds q to its own quit; inside the modal
		// Esc closes and q stays inert (ctrl+c already quit above).
		return m, nil
	case keyMatches(msg, k.Select):
		sel, ok := m.ui.trackPopupList.SelectedItem().(trackItem)
		if !ok {
			return m, nil
		}
		trackIndex := 0
		for i, item := range m.ui.trackPopupItems {
			if item.ID == sel.item.ID {
				trackIndex = i
				break
			}
		}
		m.ui.trackPopupOpen = false
		return m.playFromTrack(trackIndex)
	}
	var cmd tea.Cmd
	m.ui.trackPopupList, cmd = m.ui.trackPopupList.Update(msg)
	return m, cmd
}

func (m model) playFromTrack(trackIndex int) (tea.Model, tea.Cmd) {
	m.ui.activeTab = tabPlayer
	if m.transport.status != nil && m.transport.status.TrackID != "" {
		m.transport.pendingContextFrom = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
		m.transport.pendingContextFromAt = time.Now()
	}
	m.transport.queue = nil
	m.transport.queueHasMore = false
	m.transport.stableQueueLen = 0
	if m.transport.status != nil {
		m.transport.status.ProgressMS = 0
		m.transport.status.DurationMS = 0
	}
	m.transport.interpolationSyncAt = time.Time{}
	m.transport.interpolationProgressMS = 0
	m.beginTransportTransition()

	trackID := ""
	if trackIndex >= 0 && trackIndex < len(m.ui.trackPopupItems) {
		trackID = m.ui.trackPopupItems[trackIndex].ID
	}

	if m.tuiCmdCh != nil {
		nowPlayingContextURI = m.ui.trackPopupURI
		cmd := librespot.TUICommand{
			Kind:    librespot.TUICommandPlayContextFromTrack,
			URI:     m.ui.trackPopupURI,
			TrackID: trackID,
		}
		return m, m.sendTUICommandOrRetry(cmd)
	}

	return m, nil
}

func (m model) selectAndPlayPlaylist(sel playlistItem) (tea.Model, tea.Cmd) {
	m.ui.activeTab = tabPlayer
	m.transport.playbackErr = nil
	if m.transport.status != nil {
		m.transport.pendingContextFrom = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
		m.transport.pendingContextFromAt = time.Now()
	}
	m.transport.queue = nil
	m.transport.queueHasMore = false
	m.transport.stableQueueLen = 0
	if m.transport.status != nil {
		m.transport.status.ProgressMS = 0
		m.transport.status.DurationMS = 0
	}
	m.transport.interpolationSyncAt = time.Time{}
	m.transport.interpolationProgressMS = 0
	if m.tuiCmdCh != nil {
		m.beginTransportTransition()
		nowPlayingContextURI = sel.summary.URI
		cmds := []tea.Cmd{
			m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandPlayContext, URI: sel.summary.URI}),
			m.loadImageCmd(sel.summary.ImageURL, true),
		}
		return m, tea.Batch(cmds...)
	}
	cmds := []tea.Cmd{
		m.loadImageCmd(sel.summary.ImageURL, true),
	}
	m.beginTransportTransition()
	return m, tea.Batch(cmds...)
}
