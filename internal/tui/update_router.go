package tui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSizeMsg(msg)
	case tickMsg:
		return m.handleTickMsg()
	case playbackStateMsg:
		return m.handlePlaybackStateMsg(msg)
	case pollMsg:
		return m.handlePollMsg(msg)
	case playlistsMsg:
		return m.handlePlaylistsMsg(msg)
	case currentUserIDMsg:
		return m.handleCurrentUserIDMsg(msg)
	case playlistItemsMsg:
		return m.handlePlaylistItemsMsg(msg)
	case navDebounceMsg:
		return m.handleNavDebounceMsg(msg)
	case imageLoadedMsg:
		return m.handleImageLoadedMsg(msg)
	case imageRetryMsg:
		return m.handleImageRetryMsg(msg)
	case coverImageResolvedMsg:
		return m.handleCoverImageResolvedMsg(msg)
	case actionReconcileMsg:
		return m.handleActionReconcileMsg(msg)
	case actionMsg:
		return m.handleActionMsg(msg)
	case volDebounceMsg:
		return m.handleVolDebounceMsg(msg)
	case seekDebounceMsg:
		return m.handleSeekDebounceMsg(msg)
	case list.FilterMatchesMsg:
		return m.handleFilterMatchesMsg(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	default:
		return m, nil
	}
}
