package tui

import (
	"os/exec"
	"runtime"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
)

func browserCommand(url string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url), nil
	case "windows":
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url), nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return exec.Command("xdg-open", url), nil
	default:
		return nil, exec.ErrNotFound
	}
}

func (m model) openAuthLoginCmd() tea.Cmd {
	command, err := browserCommand(m.ui.authLoginURL)
	if err != nil {
		return func() tea.Msg { return authBrowserResultMsg{err: err} }
	}
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return authBrowserResultMsg{err: err}
	})
}

func (m model) copyAuthLoginURLCmd() tea.Cmd {
	url := m.ui.authLoginURL
	return func() tea.Msg {
		return authClipboardResultMsg{err: clipboard.WriteAll(url)}
	}
}

func (m model) handleAuthLoginKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.ui.authLoginOnly && m.ui.authLoginFinished &&
		(keyMatches(msg, m.ui.keys.CloseModal) || keyMatches(msg, m.ui.keys.Select)) {
		return m, tea.Quit
	}
	if keyMatches(msg, m.ui.keys.CloseModal) {
		m.ui.authLoginOpen = false
		m.ui.authLoginNotice = "Press L to reopen the Spotify login dialog."
		return m, nil
	}
	if keyMatches(msg, m.ui.keys.Select) || keyMatches(msg, key.NewBinding(key.WithKeys("o"))) {
		return m, m.openAuthLoginCmd()
	}
	if keyMatches(msg, key.NewBinding(key.WithKeys("c"))) {
		return m, m.copyAuthLoginURLCmd()
	}
	return m, nil
}

func (m model) authLoginModalView() string {
	modalW := min(68, m.ui.width-4)
	title := "Spotify sign-in required"
	description := "Orpheus needs your permission to connect to Spotify."
	if m.ui.authLoginPlayback {
		title = "Spotify playback sign-in required"
		description = "This is separate from the Web API login."
	}
	body := lipgloss.JoinVertical(lipgloss.Left,
		description,
		m.authPlaybackDescription(),
		"",
		m.styles.modalRow("Open login page", "enter / o", false, modalW),
		m.styles.modalRow("Copy login link", "c", false, modalW),
	)
	if m.ui.authLoginNotice != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", m.styles.styleModalHint.Render(m.ui.authLoginNotice))
	}
	hint := m.styles.styleModalHint.Render("enter open · c copy · esc close")
	contentWidth := max(12, modalW-modalContentInset)
	wrappedBody := lipgloss.NewStyle().Width(contentWidth).Render(body)
	modalH := lipgloss.Height(wrappedBody) + 4
	return m.styles.modalFrame(
		m.ui.width,
		m.ui.height,
		m.styles.styleModalTitle.Render(title),
		hint,
		body,
		modalW,
		modalH,
	)
}

func (m model) authPlaybackDescription() string {
	if m.ui.authLoginPlayback {
		return "Authorize Orpheus playback to connect and stream."
	}
	return ""
}
