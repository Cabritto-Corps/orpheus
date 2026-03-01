package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// ── Palette ──────────────────────────────────────────────────────────────────

const (
	colorBlue       = lipgloss.Color("#4A90D9")
	colorBlueLight  = lipgloss.Color("#7AB8E6")
	colorOffWhite   = lipgloss.Color("#E8EAED")
	colorGray       = lipgloss.Color("#9CA3AF")
	colorMutedBlue  = lipgloss.Color("#5B7A9E")
	colorError      = lipgloss.Color("#FF5757")
)

// ── Header / chrome ──────────────────────────────────────────────────────────

var (
	styleHeaderDevice = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	styleHeaderNowPlaying = lipgloss.NewStyle().
				Foreground(colorGray)

	styleHeaderPlaying = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true)

	styleHeaderPaused = lipgloss.NewStyle().
				Foreground(colorMutedBlue)
)

// ── Error / status ────────────────────────────────────────────────────────────

var (
	styleError = lipgloss.NewStyle().
			Foreground(colorError)

	styleDimmed = lipgloss.NewStyle().
			Foreground(colorMutedBlue)
)

// ── Panels ────────────────────────────────────────────────────────────────────

var (
	stylePanelBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorMutedBlue)

	stylePanelBorderActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBlue)
)

func panelStyle(w, h int) lipgloss.Style {
	return stylePanelBorder.
		Width(w).
		Height(h)
}

func activePanelStyle(w, h int) lipgloss.Style {
	return stylePanelBorderActive.
		Width(w).
		Height(h)
}

// ── Playlist browser / cover panel ───────────────────────────────────────────

var (
	styleCoverPlaceholder = lipgloss.NewStyle().
				Foreground(colorMutedBlue).
				Italic(true)

	stylePlaylistName = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorOffWhite)

	stylePlaylistMeta = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	stylePlaylistOwner = lipgloss.NewStyle().
				Foreground(colorBlue)
)

// ── Playback / now-playing ────────────────────────────────────────────────────

var (
	styleTrackName = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorOffWhite)

	styleArtistName = lipgloss.NewStyle().
			Foreground(colorBlue)
)

// ── Queue ─────────────────────────────────────────────────────────────────────

var (
	styleSectionTitle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBlue)

	styleQueueIndex = lipgloss.NewStyle().
			Foreground(colorMutedBlue)

	styleQueueTrack = lipgloss.NewStyle().
			Foreground(colorGray)

	styleQueueArtist = lipgloss.NewStyle().
				Foreground(colorMutedBlue)
)

// ── Player bar ────────────────────────────────────────────────────────────────

var (
	stylePlayerSeparator = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	stylePlayerTrack = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorOffWhite)

	stylePlayerArtist = lipgloss.NewStyle().
				Foreground(colorBlue)

	stylePlayerTime = lipgloss.NewStyle().
			Foreground(colorMutedBlue)

	stylePlayerVolume = lipgloss.NewStyle().
				Foreground(colorGray)

	stylePlayerVolIcon = lipgloss.NewStyle().
				Foreground(colorBlue)
)

// ── Modal ─────────────────────────────────────────────────────────────────────

var (
	styleModalBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(0, 1)

	styleModalTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBlue)

	styleModalHint = lipgloss.NewStyle().
			Foreground(colorMutedBlue)
)

// ── List delegate ─────────────────────────────────────────────────────────────

// newPlaylistDelegate returns a styled list delegate for playlist items.
func newPlaylistDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.SetSpacing(0)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colorGray).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colorOffWhite).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		Padding(0, 0, 0, 2)

	d.Styles.DimmedTitle = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		Padding(0, 0, 0, 2)

	d.Styles.DimmedDesc = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		Padding(0, 0, 0, 2)

	return d
}

// newListStyles applies the app colour scheme to a list.Model's own Styles field.
func applyListStyles(l *list.Model) {
	l.Styles.Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorOffWhite)

	l.Styles.TitleBar = lipgloss.NewStyle().
		Background(lipgloss.Color("")).
		Padding(0, 0, 1, 0)

	l.Styles.FilterPrompt = lipgloss.NewStyle().
		Foreground(colorBlue)

	l.Styles.FilterCursor = lipgloss.NewStyle().
		Foreground(colorBlueLight)

	l.Styles.StatusBar = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	l.Styles.StatusEmpty = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	l.Styles.NoItems = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		Padding(1, 2)

	l.Styles.PaginationStyle = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	l.Styles.ActivePaginationDot = lipgloss.NewStyle().
		Foreground(colorBlue).
		SetString("•")

	l.Styles.InactivePaginationDot = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		SetString("•")

	l.Styles.HelpStyle = lipgloss.NewStyle().
		Foreground(colorMutedBlue)
}
