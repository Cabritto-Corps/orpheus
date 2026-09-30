package tui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// Mirror live modal state onto the overlay slot: cmd closures check it at
// delivery time, dropping emissions stale since a modal opened.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.update(msg)
	if mm, ok := nm.(model); ok && mm.ui.imgs != nil {
		mm.ui.imgs.setOverlaySuppressed(mm.modalKind() != modalNone)
	}
	return nm, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case tea.ColorProfileMsg:
		// Construction seeds this too, so tests and the runtime agree.
		m.styles.colorProfile = msg.Profile
		return m, nil
	case tickMsg:
		return m.handleTickMsg()
	case playbackStateMsg:
		return m.handlePlaybackStateMsg(msg)
	case connectionLostMsg:
		m.transport.playbackErr = msg.err
		return m, nil
	case playlistsMsg:
		return m.handlePlaylistsMsg(msg)
	case navDebounceMsg:
		return m.handleNavDebounceMsg(msg)
	case imageLoadedMsg:
		return m.handleImageLoadedMsg(msg)
	case imageRetryMsg:
		return m.handleImageRetryMsg(msg)
	case coverImageResolvedMsg:
		return m.handleCoverImageResolvedMsg(msg)
	case coverImageURLsBatchResolvedMsg:
		return m.handleCoverImageURLsBatchResolvedMsg(msg)
	case imagesBatchLoadedMsg:
		return m.handleImagesBatchLoadedMsg(msg)
	case volDebounceMsg:
		return m.handleVolDebounceMsg(msg)
	case seekDebounceMsg:
		return m.handleSeekDebounceMsg(msg)
	case tuiCmdRetryMsg:
		return m.handleTUICmdRetryMsg(msg)
	case trackPopupItemsMsg:
		return m.handleTrackPopupItemsMsg(msg)
	case list.FilterMatchesMsg:
		return m.handleFilterMatchesMsg(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.KeyReleaseMsg:
		// Releases must never re-fire a press: ignore them explicitly instead of falling through.
		return m, nil
	default:
		return m, nil
	}
}
