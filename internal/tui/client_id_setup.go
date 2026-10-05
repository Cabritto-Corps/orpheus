package tui

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"

	"orpheus/internal/config"
)

const spotifyDashboardURL = "https://developer.spotify.com/dashboard"
const spotifySetupTutorialURL = "https://github.com/Cabritto-Corps/orpheus/blob/main/tutorial.md"

func (m model) handleClientIDSetupKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEscape {
		if m.ui.clientIDComplete {
			m.ui.clientIDSetupOpen = false
		} else {
			m.ui.clientIDEditing = false
		}
		return m, nil
	}
	if m.ui.clientIDSaving || m.ui.clientIDComplete {
		return m, nil
	}
	if m.ui.clientIDEditing && msg.Code == 'v' && msg.Mod&tea.ModCtrl != 0 {
		return m, func() tea.Msg {
			text, err := clipboard.ReadAll()
			return clientIDPasteMsg{text: text, err: err}
		}
	}
	if !m.ui.clientIDEditing {
		switch {
		case keyMatches(msg, key.NewBinding(key.WithKeys("i"))):
			m.ui.clientIDEditing = true
			m.ui.clientIDNotice = "Type or paste your Spotify Client ID, then press enter to save."
			return m, nil
		case keyMatches(msg, key.NewBinding(key.WithKeys("o"))):
			cmd, err := browserCommand(spotifyDashboardURL)
			if err != nil {
				m.ui.clientIDNotice = "Could not open your browser: " + err.Error()
				return m, nil
			}
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return clientIDBrowserMsg{err: err} })
		case keyMatches(msg, key.NewBinding(key.WithKeys("c"))):
			return m, func() tea.Msg { return clientIDCopyMsg{err: clipboard.WriteAll(spotifyDashboardURL)} }
		case keyMatches(msg, key.NewBinding(key.WithKeys("t"))):
			cmd, err := browserCommand(spotifySetupTutorialURL)
			if err != nil {
				m.ui.clientIDNotice = "Could not open the setup guide: " + err.Error()
				return m, nil
			}
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return clientIDGuideBrowserMsg{err: err} })
		}
		return m, nil
	}
	switch {
	case msg.Code == tea.KeyEnter:
		id := strings.TrimSpace(m.ui.clientIDInput)
		if id == "" {
			m.ui.clientIDNotice = "Enter the Client ID from your Spotify app."
			return m, nil
		}
		m.ui.clientIDSaving = true
		path := m.ui.config.EnvPath
		return m, func() tea.Msg { return clientIDSavedMsg{err: config.SaveSpotifyClientID(path, id)} }
	case msg.Code == tea.KeyBackspace || msg.Code == tea.KeyDelete:
		if len(m.ui.clientIDInput) > 0 {
			m.ui.clientIDInput = m.ui.clientIDInput[:len(m.ui.clientIDInput)-1]
		}
		m.ui.clientIDNotice = ""
	case msg.Text != "":
		m.ui.clientIDInput += strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, msg.Text)
		m.ui.clientIDNotice = ""
	}
	return m, nil
}

func cleanClientIDPaste(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
}

func (m model) clientIDSetupModalView() string {
	w := min(68, m.ui.width-4)
	field := m.ui.clientIDInput
	if field == "" {
		if m.ui.clientIDEditing {
			field = "Type or paste your Client ID here"
		} else {
			field = "Press i to enter Client ID"
		}
	}
	if m.ui.clientIDEditing && !m.ui.clientIDComplete {
		field = "▏" + field
	}
	if m.ui.clientIDComplete {
		field = m.styles.styleDimmed.Render("Saved")
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		"Create a Spotify app and add this Redirect URI:",
		m.styles.styleModalHint.Render("http://127.0.0.1:8989/callback"),
		"Select Web API and Web Playback SDK.",
		"In User Management, add your Spotify account email as a user.",
		"",
		m.styles.modalRow("Open dashboard", "o", false, w),
		m.styles.modalRow("Open setup guide", "t", false, w),
		m.styles.modalRow("Copy dashboard link", "c", false, w),
		lipgloss.NewStyle().Width(max(12, w-modalContentInset)).Border(lipgloss.RoundedBorder()).Padding(0, 1).Render(field),
		m.styles.modalRow("Save Client ID", "enter", false, w),
	)
	if m.ui.clientIDSaving {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", "Saving Client ID…")
	}
	if m.ui.clientIDNotice != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", m.styles.styleModalHint.Render(m.ui.clientIDNotice))
	}
	hint := "o dashboard · t guide · c copy · i edit ID"
	if m.ui.clientIDEditing {
		hint = "terminal paste · enter save · esc shortcuts"
	}
	if m.ui.clientIDComplete {
		hint = "esc close · continuing to sign-in"
	}
	innerW := max(12, w-modalContentInset)
	wrapped := lipgloss.NewStyle().Width(innerW).Render(body)
	return m.styles.modalFrame(m.ui.width, m.ui.height,
		m.styles.styleModalTitle.Render("Spotify Web API setup"),
		m.styles.styleModalHint.Render(hint), body, w, lipgloss.Height(wrapped)+4)
}
