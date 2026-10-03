package tui

import (
	"errors"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.syncListKeyMaps()
	k := m.ui.keys
	filtering := m.isFiltering()

	// ctrl+c is quit's guaranteed key through modals, capture, and filter mode.
	if isQuitSignal(msg) {
		return m, tea.Quit
	}

	// An open modal owns every other key (focus trap); Esc always closes.
	if kind := m.modalKind(); kind != modalNone {
		return m.routeModalKey(msg, kind)
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
			// Hide the cover with the modal frame; closing re-places it without waiting for a tick.
			return m, m.kittyOverlayCmd()
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
			return m, tea.Batch(m.loadVisiblePlaylistCoversCmd(), m.kittyOverlayCmd())
		}
	}

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

// updateBrowseList forwards a key to a browse list and restores the
// pre-filter selection when search is cancelled: bubbles moves the cursor
// to the top when filtering opens (GoToStart) and leaves it there on esc,
// so the cover preview would snap to whatever the filter happened to land
// on. Enter keeps the filtered selection — only cancel restores.
func (m *model) updateBrowseList(l *list.Model, msg tea.KeyPressMsg) tea.Cmd {
	pre := l.FilterState()
	preIdx := l.GlobalIndex()
	var cmd tea.Cmd
	*l, cmd = l.Update(msg)
	post := l.FilterState()
	switch {
	case pre != list.Filtering && post == list.Filtering:
		// Filter opened: remember where the user was.
		m.browse.filterSavedIdx = preIdx
		m.browse.filterRestorable = true
	case pre == list.Filtering && post == list.Filtering:
		// Typing: keep the armed state.
	case pre == list.Filtering && post == list.Unfiltered:
		// Cancel (esc) or empty-accept: reseat the pre-filter selection.
		if m.browse.filterRestorable && m.browse.filterSavedIdx < len(l.Items()) {
			l.Select(m.browse.filterSavedIdx)
		}
		m.browse.filterRestorable = false
	default:
		// Accept (FilterApplied) and plain navigation disarm: only cancel restores.
		m.browse.filterRestorable = false
	}
	return cmd
}

func (m model) handlePlaylistKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if m.browse.playlistList.FilterState() == list.Filtering {
		prevURL := selectedImageURLFromList(m.browse.playlistList)
		cmd := m.updateBrowseList(&m.browse.playlistList, msg)
		nextURL := selectedImageURLFromList(m.browse.playlistList)
		cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd(), m.kittyOverlayCmd()}
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
	cmd := m.updateBrowseList(&m.browse.playlistList, msg)
	nextURL := selectedImageURLFromList(m.browse.playlistList)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd(), m.kittyOverlayCmd()}
	if nextURL != "" && nextURL != prevURL {
		cmds = append(cmds, m.loadImageCmd(nextURL, false))
	}
	return m, tea.Batch(cmds...)
}

func (m model) handleAlbumKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if m.browse.albumList.FilterState() == list.Filtering {
		prevURL := selectedImageURLFromList(m.browse.albumList)
		cmd := m.updateBrowseList(&m.browse.albumList, msg)
		nextURL := selectedImageURLFromList(m.browse.albumList)
		cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd(), m.kittyOverlayCmd()}
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
	cmd := m.updateBrowseList(&m.browse.albumList, msg)
	nextURL := selectedImageURLFromList(m.browse.albumList)
	cmds := []tea.Cmd{cmd, m.scheduleNavDebounceCmd(), m.kittyOverlayCmd()}
	if nextURL != "" && nextURL != prevURL {
		cmds = append(cmds, m.loadImageCmd(nextURL, false))
	}
	return m, tea.Batch(cmds...)
}

// routeModalKey dispatches a key to the open dialog; quit-first already ran in handleKey.
func (m model) routeModalKey(msg tea.KeyPressMsg, kind modalKind) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if kind == modalHelp {
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
		return m, m.kittyOverlayCmd()
	}
	if kind == modalTrackPopup {
		return m.handleTrackPopupKey(msg)
	}
	return m.handleSettingsKey(msg)
}

func (m model) isFiltering() bool {
	for _, l := range m.filterableLists() {
		if l.FilterState() == list.Filtering {
			return true
		}
	}
	return false
}

// Queue commands address the visible view: position 0 is the first shown entry.
func (m *model) handleQueueKey(msg tea.KeyPressMsg) tea.Cmd {
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
		// Jump loads a track, so it must not fire mid-transition like next/prev.
		if m.transport.transition.Pending() {
			return nil
		}
		if q[cursor].Queued {
			return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueJump, QueueIndex: cursor})
		}
		// Context rows play from the current context.
		if uri := m.currentContextURI(); uri != "" {
			if m.transport.status != nil {
				m.transport.pendingContextFrom = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
			}
			return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandPlayContextFromTrack, URI: uri, TrackID: q[cursor].ID})
		}
		return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueJump, QueueIndex: cursor})
	case keyMatches(msg, k.QueueRemove):
		if !q[cursor].Queued {
			m.transport.playbackErr = errors.New("only queued tracks can be removed")
			return nil
		}
		return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueRemove, QueueIndex: cursor})
	case keyMatches(msg, k.QueueMoveUp):
		if cursor > 0 {
			if !q[cursor].Queued || !q[cursor-1].Queued {
				m.transport.playbackErr = errors.New("only queued tracks can be reordered")
				return nil
			}
			return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueReorder, QueueIndex: cursor, QueueTargetIndex: cursor - 1})
		}
		return nil
	case keyMatches(msg, k.QueueMoveDown):
		if cursor < len(q)-1 {
			if !q[cursor].Queued || !q[cursor+1].Queued {
				m.transport.playbackErr = errors.New("only queued tracks can be reordered")
				return nil
			}
			return m.sendTUICommandOrRetry(librespot.TUICommand{Kind: librespot.TUICommandQueueReorder, QueueIndex: cursor, QueueTargetIndex: cursor + 1})
		}
		return nil
	default:
		return nil
	}
}

// New filterable surfaces register their list here.
func (m *model) allFilterLists() []*list.Model {
	return []*list.Model{&m.browse.playlistList, &m.browse.albumList, &m.ui.trackPopupList}
}

// Tab-scoping is load-bearing: an inactive tab's filter must not freeze the new tab's keys.
func (m model) filterableLists() []*list.Model {
	if m.ui.trackPopupOpen {
		return []*list.Model{&m.ui.trackPopupList}
	}
	switch m.ui.activeTab {
	case tabPlaylists:
		return []*list.Model{&m.browse.playlistList}
	case tabAlbums:
		return []*list.Model{&m.browse.albumList}
	}
	return nil
}

// Snapshot bubbles' browse bindings once; per-keypress reconciliation strips
// app-owned keys from this stable base so runtime rebinds work both ways.
var (
	defaultListNextPage = list.DefaultKeyMap().NextPage
	defaultListPrevPage = list.DefaultKeyMap().PrevPage
)

// syncListKeyMaps reconciles list KeyMaps with the live keyMap so bubbles dispatches nothing the app owns.
// Capture flow replaces m.ui.keys wholesale, so this runs on every keypress; bubbles v2's quit binding stays
// disabled and app-owned page keys stay unbound by lists.
func (m *model) syncListKeyMaps() {
	claimed := make(map[string]struct{}, 64)
	for _, meta := range actionRegistry {
		for _, spec := range meta.bind(m.ui.keys).Keys() {
			claimed[canonicalKeySpec(spec)] = struct{}{}
		}
	}
	for _, l := range m.allFilterLists() {
		l.KeyMap.Filter = m.ui.keys.Filter
		l.DisableQuitKeybindings()
		l.KeyMap.NextPage = unclaimedListKeys(defaultListNextPage, claimed)
		l.KeyMap.PrevPage = unclaimedListKeys(defaultListPrevPage, claimed)
	}
}

func unclaimedListKeys(def key.Binding, claimed map[string]struct{}) key.Binding {
	var keep []string
	for _, spec := range def.Keys() {
		if _, ok := claimed[canonicalKeySpec(spec)]; !ok {
			keep = append(keep, spec)
		}
	}
	help := def.Help()
	return key.NewBinding(key.WithKeys(keep...), key.WithHelp(help.Key, help.Desc))
}

func (m model) matchGlobalPlaybackKey(msg tea.KeyPressMsg) playbackInputKind {
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

func (m model) handlePlaybackKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

// newTrackPopupList uses shared chrome with readable status and pagination dots on dark themes.
func newTrackPopupList(s *themeStyles, termW, termH int) list.Model {
	_, listW, listH := popupModalSize(termW, termH)
	popup := list.New(nil, newTrackPopupDelegate(s), listW, listH)
	popup.SetShowTitle(false)
	popup.SetShowStatusBar(true)
	popup.SetFilteringEnabled(true)
	popup.SetShowFilter(true)
	popup.SetShowHelp(false)
	popup.FilterInput.Prompt = "/ "
	popup.SetStatusBarItemName("track", "tracks")
	popup.Styles.Filter.Focused.Prompt = lipgloss.NewStyle().Foreground(s.colorMutedBlue)
	popup.Styles.Filter.Blurred.Prompt = lipgloss.NewStyle().Foreground(s.colorMutedBlue)
	popup.Styles.StatusBar = lipgloss.NewStyle().Foreground(s.colorOffWhite).PaddingLeft(1)
	popup.Styles.ActivePaginationDot = lipgloss.NewStyle().Foreground(s.colorBlue).SetString(" •")
	popup.Styles.InactivePaginationDot = lipgloss.NewStyle().Foreground(s.colorDimBlue).SetString(" •")
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

	popup := newTrackPopupList(m.styles, m.ui.width, m.ui.height)
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
			// Without a reply, a full backend queue would leave the popup on "Loading…" forever.
			m.ui.trackPopupOpen = false
			m.transport.playbackErr = errors.New("couldn't load tracks — player busy")
		}
	} else {
		m.ui.trackPopupItems = []spotify.QueueItem{}
	}
	// Hide the cover at once with a pure delete.
	return m, m.kittyOverlayCmd()
}

func (m model) handleTrackPopupKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	if m.ui.trackPopupList.FilterState() == list.Filtering {
		// Bubbles consumes the first esc/enter in search; close/play only see non-search keys.
		var cmd tea.Cmd
		m.ui.trackPopupList, cmd = m.ui.trackPopupList.Update(msg)
		return m, cmd
	}
	switch {
	case keyMatches(msg, k.CloseModal):
		m.ui.trackPopupOpen = false
		return m, m.kittyOverlayCmd()
	case keyMatches(msg, k.Quit):
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
