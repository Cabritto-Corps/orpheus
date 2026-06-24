package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	golibrespot "github.com/elxgy/go-librespot"

	"orpheus/internal/librespot"
	"orpheus/internal/spotify"
)

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys

	switch {
	case keyMatches(msg, k.Quit):
		return m, tea.Quit
	case keyMatches(msg, k.ToggleHelp):
		m.ui.helpOpen = !m.ui.helpOpen
		return m, nil
	}

	if m.ui.helpOpen {
		if keyMatches(msg, k.CloseModal) {
			m.ui.helpOpen = false
		}
		return m, nil
	}

	if m.ui.trackPopupOpen {
		return m.handleTrackPopupKey(msg)
	}

	if keyMatches(msg, k.Tab) {
		filtering := (m.ui.activeTab == tabPlaylists && m.browse.playlistList.FilterState() == list.Filtering) ||
			(m.ui.activeTab == tabAlbums && m.browse.albumList.FilterState() == list.Filtering)
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
			m.ui.cachedBodyLayoutValid = false
			return m, m.loadVisiblePlaylistCoversCmd()
		}
	}

	// Global playback keys: work on all tabs, not just player
	if action := m.matchGlobalPlaybackKey(msg); action != "" {
		m.enqueuePlaybackInput(action)
		return m, m.pumpInputExecutor()
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
		m.browse.playlistsExhausted = false
		m.browse.albumsForbidden = false
		m.browse.playlistsErr = nil
		m.browse.playlistsRetryCount = 0
		return m, m.loadPlaylistsCmd(0, playlistLoadBatchSize)

	case keyMatches(msg, k.Select):
		sel, ok := m.browse.playlistList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel, "play-from-browser")
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
	case keyMatches(msg, k.Select):
		sel, ok := m.browse.albumList.SelectedItem().(playlistItem)
		if !ok {
			return m, nil
		}
		return m.selectAndPlayPlaylist(sel, "play-from-browser")
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
	if m.shouldBlockTransportInput(msg) {
		k := m.ui.keys
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
	k := m.ui.keys
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

type trackPopupItemsMsg struct {
	items []spotify.QueueItem
}

func (m model) openTrackPopup(sel playlistItem) (tea.Model, tea.Cmd) {
	m.ui.trackPopupOpen = true
	m.ui.trackPopupKind = sel.summary.Kind
	m.ui.trackPopupID = sel.summary.ID
	m.ui.trackPopupURI = sel.summary.URI
	m.ui.trackPopupName = sel.summary.Name
	m.ui.trackPopupItems = nil

	modalW := min(m.ui.width-8, 60)
	bodyH := m.ui.height - headerH - tabBarH - 2
	innerH := max(bodyH-4, 10)

	delegate := newTrackPopupDelegate()
	popup := list.New(nil, delegate, modalW, innerH)
	popup.SetShowTitle(false)
	popup.SetShowStatusBar(true)
	popup.SetFilteringEnabled(true)
	popup.SetShowFilter(true)
	popup.SetShowHelp(false)
	popup.FilterInput.Prompt = "/ "
	popup.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(colorMutedBlue)
	popup.Styles.StatusBar = lipgloss.NewStyle().Foreground(colorMutedBlue).PaddingLeft(1)
	m.ui.trackPopupList = popup
	m.ui.trackPopupWidth = modalW - 4

	if m.tuiCmdCh != nil && m.contextTracksCh != nil {
		select {
		case m.tuiCmdCh <- librespot.TUICommand{
			Kind:     librespot.TUICommandGetContextTracks,
			URI:      sel.summary.URI,
			ResultCh: m.contextTracksCh,
		}:
		default:
		}
	} else {
		m.ui.trackPopupItems = []spotify.QueueItem{}
	}
	return m, nil
}

func (m model) handleTrackPopupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.CloseModal):
		m.ui.trackPopupOpen = false
		return m, nil
	case keyMatches(msg, k.Select):
		if m.ui.trackPopupList.FilterState() == list.Filtering {
			break
		}
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
		select {
		case m.tuiCmdCh <- librespot.TUICommand{
			Kind:    librespot.TUICommandPlayContextFromTrack,
			URI:     m.ui.trackPopupURI,
			TrackID: trackID,
		}:
		default:
		}
		return m, nil
	}

	cmds := []tea.Cmd{
		m.actionCmd(func(ctx context.Context) error {
			return m.service.PlayPlaylist(ctx, m.deviceName, m.ui.trackPopupURI)
		}, "play-from-track"),
	}
	m.ui.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
	return m, tea.Batch(cmds...)
}

func (m model) selectAndPlayPlaylist(sel playlistItem, action string) (tea.Model, tea.Cmd) {
	m.ui.activeTab = tabPlayer
	m.transport.playbackErr = nil
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
		select {
		case m.tuiCmdCh <- librespot.TUICommand{Kind: librespot.TUICommandPlayContext, URI: sel.summary.URI}:
			m.beginTransportTransition()
		default:
		}
		cmds := []tea.Cmd{m.loadImageCmd(sel.summary.ImageURL, true)}
		if canReadTracks {
			m.browse.activePlaylistItemLoading = true
			m.browse.activePlaylistLoadToken++
			cmds = append(cmds, m.loadPlaylistItemsCmd(sel.summary.ID, 0, m.browse.activePlaylistLoadToken))
		}
		return m, tea.Batch(cmds...)
	}
	cmds := []tea.Cmd{
		m.actionCmd(func(ctx context.Context) error {
			return m.service.PlayPlaylist(ctx, m.deviceName, sel.summary.URI)
		}, action),
		m.loadImageCmd(sel.summary.ImageURL, true),
	}
	m.beginTransportTransition()
	m.ui.actionFastPollUntil = time.Now().Add(actionFastPollWindow)
	if canReadTracks {
		m.browse.activePlaylistItemLoading = true
		m.browse.activePlaylistLoadToken++
		cmds = append(cmds, m.loadPlaylistItemsCmd(sel.summary.ID, 0, m.browse.activePlaylistLoadToken))
	}
	return m, tea.Batch(cmds...)
}
