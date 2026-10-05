package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// Mirror live modal state onto the overlay slot: cmd closures check it at
// delivery time, dropping emissions stale since a modal opened.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, cmd := m.update(msg)
	if mm, ok := nm.(model); ok {
		if mm.ui.imgs != nil {
			mm.ui.imgs.setOverlaySuppressed(mm.modalKind() != modalNone)
		}
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
	case authLoginRequiredMsg:
		m.ui.authLoginURL = msg.url
		m.ui.authLoginOpen = msg.url != ""
		m.ui.authLoginNotice = ""
		m.ui.authLoginPlayback = true
		return m, nil
	case clientIDSavedMsg:
		m.ui.clientIDSaving = false
		if msg.err != nil {
			m.ui.clientIDNotice = "Could not save Client ID: " + msg.err.Error()
			return m, nil
		}
		m.ui.clientIDComplete = true
		m.ui.config.SpotifyClientID = strings.TrimSpace(m.ui.clientIDInput)
		m.ui.clientIDNotice = "Client ID saved. Continuing to Spotify sign-in…"
		if m.ui.authLoginOnly {
			return m, tea.Quit
		}
		return m, nil
	case authLoginCompleteMsg:
		m.ui.authLoginFinished = true
		m.ui.authLoginSucceeded = msg.err == nil
		if msg.err != nil {
			m.ui.authLoginError = msg.err.Error()
			m.ui.authLoginNotice = "Spotify sign-in failed. " + msg.err.Error()
			if m.ui.authLoginOnly {
				return m, tea.Quit
			}
		} else {
			m.ui.authLoginNotice = "Spotify sign-in complete. Run orpheus to start the app."
			if m.ui.authLoginOnly {
				return m, tea.Quit
			}
		}
		return m, nil
	case clientIDBrowserMsg:
		if msg.err != nil {
			m.ui.clientIDNotice = "Could not open your browser: " + msg.err.Error()
		} else {
			m.ui.clientIDNotice = "Spotify dashboard opened in your browser."
		}
		return m, nil
	case clientIDCopyMsg:
		if msg.err != nil {
			m.ui.clientIDNotice = "Could not copy the dashboard link: " + msg.err.Error()
		} else {
			m.ui.clientIDNotice = "Dashboard link copied to clipboard."
		}
		return m, nil
	case clientIDGuideBrowserMsg:
		if msg.err != nil {
			m.ui.clientIDNotice = "Could not open the setup guide: " + msg.err.Error()
		} else {
			m.ui.clientIDNotice = "Setup guide opened in your browser."
		}
		return m, nil
	case clientIDPasteMsg:
		if msg.err != nil {
			m.ui.clientIDNotice = "Clipboard access unavailable. Use your terminal's paste command instead."
		} else if m.ui.clientIDEditing {
			m.ui.clientIDInput += cleanClientIDPaste(msg.text)
			m.ui.clientIDNotice = ""
		}
		return m, nil
	case authBrowserResultMsg:
		if msg.err != nil {
			m.ui.authLoginNotice = "Could not open your browser: " + msg.err.Error()
		} else {
			m.ui.authLoginNotice = "Login page opened. Complete authorization in your browser."
		}
		return m, nil
	case authClipboardResultMsg:
		if msg.err != nil {
			m.ui.authLoginNotice = "Could not copy the login link: " + msg.err.Error()
		} else {
			m.ui.authLoginNotice = "Login link copied to clipboard."
		}
		return m, nil
	case connectionLostMsg:
		m.transport.playbackErr = msg.err
		return m, nil
	case playlistsMsg:
		return m.handlePlaylistsMsg(msg)
	case songsLibraryMsg:
		return m.handleSongsLibraryMsg(msg)
	case navDebounceMsg:
		return m.handleNavDebounceMsg(msg)
	case searchDebounceMsg:
		return m.handleSearchDebounceMsg(msg)
	case searchResultsMsg:
		return m.handleSearchResultsMsg(msg)
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
	case tea.PasteMsg:
		if m.ui.clientIDSetupOpen && m.ui.clientIDEditing {
			m.ui.clientIDInput += cleanClientIDPaste(msg.Content)
			m.ui.clientIDNotice = ""
		}
		if m.ui.activeTab == tabSearch && m.browse.search.input.Focused() {
			previous := m.browse.search.input.Value()
			m.browse.search.input, _ = m.browse.search.input.Update(msg)
			if query := strings.TrimSpace(m.browse.search.input.Value()); query != previous {
				return m.setSearchQuery(query)
			}
		}
		return m, nil
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
		// The failure must survive as the startup error, and the gate must
		// resolve now or it hides behind a connecting placeholder forever.
		m.browse.librarySettled = true
		m.transport.playbackErr = msg.err
		// A pending playback auth URL cannot be completed once the backend
		// connector has failed. Close its modal and show the actual failure in
		// the library panel instead of trapping the user on a stale login.
		m.ui.authLoginURL = ""
		m.ui.authLoginOpen = false
		m.ui.authLoginNotice = ""
		m.ui.authLoginPlayback = false
		return m, nil
	}
	m.ui.authLoginURL = ""
	m.ui.authLoginOpen = false
	m.ui.authLoginNotice = ""
	m.ui.authLoginPlayback = false
	if msg.catalog != nil {
		if m.catalogSource != nil {
			m.catalogSource.set(msg.catalog)
		}
		m.catalog = msg.catalog
		// The backend pushes no state at attach: bound the idle reveal.
		m.transport.revealArmed = true
		m.transport.revealGraceEnd = time.Now().Add(firstStateGrace)
		return m, tea.Batch(
			m.loadPlaylistsCmd(),
			tea.Tick(firstStateGrace, func(time.Time) tea.Msg { return revealGraceMsg{} }),
		)
	}
	return m, nil
}
