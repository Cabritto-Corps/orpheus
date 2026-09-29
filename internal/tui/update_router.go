package tui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case tea.ColorProfileMsg:
		// Single write point for the bundle's session-fixed profile: the
		// runtime delivers this once at startup, and construction seeds
		// it from the environment so tests and non-runtime paths agree.
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
		// Key releases carry no action: a release must never re-fire the
		// press it follows (especially while a modal owns the keys), so
		// releases are ignored explicitly rather than falling through.
		return m, nil
	default:
		return m, nil
	}
}
