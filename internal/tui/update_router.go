package tui

import (
	"time"

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
	case playerBackendMsg:
		return m.handlePlayerBackendMsg(msg)
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
	case inputRetryMsg:
		return m, m.pumpInputExecutor()
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

func (m model) handlePlayerBackendMsg(msg playerBackendMsg) (tea.Model, tea.Cmd) {
	m.transport.playerConnecting = false
	if msg.err != nil {
		// No global reset here: this failure must survive as the UI's startup error.
		// The startup reveal gate must also resolve, or the failure stays hidden
		// behind a connecting placeholder.
		m.browse.librarySettled = true
		m.transport.playbackErr = msg.err
		return m, nil
	}
	if msg.catalog != nil {
		if m.catalogSource != nil {
			m.catalogSource.set(msg.catalog)
		}
		m.catalog = msg.catalog
		// Arm the sync bound: the player backend pushes no state at attach,
		// so an idle startup reveals on the grace timer instead of waiting.
		m.transport.revealArmed = true
		m.transport.revealGraceEnd = time.Now().Add(firstStateGrace)
		return m, tea.Batch(
			m.loadPlaylistsCmd(),
			// The deadline re-render: the hold must release on its own,
			// decoupled from whichever tick interval applies then.
			tea.Tick(firstStateGrace, func(time.Time) tea.Msg { return revealGraceMsg{} }),
		)
	}
	return m, nil
}
