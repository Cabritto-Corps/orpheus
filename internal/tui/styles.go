package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

const (
	colorBlue      = lipgloss.Color("#4A90D9")
	colorBlueLight = lipgloss.Color("#7AB8E6")
	colorOffWhite  = lipgloss.Color("#E8EAED")
	colorGray      = lipgloss.Color("#9CA3AF")
	colorMutedBlue = lipgloss.Color("#5B7A9E")
	colorDivider   = lipgloss.Color("#2A3A4A")
	colorError     = lipgloss.Color("#FF5757")
)

var (
	styleHeaderStatus = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	styleHeaderPlaying = lipgloss.NewStyle().
				Foreground(colorBlue).
				Bold(true)

	styleHeaderPaused = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	styleHeaderCenter = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorOffWhite)

	styleHeaderSub = lipgloss.NewStyle().
			Foreground(colorMutedBlue)

	styleHeaderVolume = lipgloss.NewStyle().
				Foreground(colorGray)

	styleHeaderDevice = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	styleHeaderNowPlaying = lipgloss.NewStyle().
				Foreground(colorGray)
)

var (
	styleError = lipgloss.NewStyle().
			Foreground(colorError)

	styleDimmed = lipgloss.NewStyle().
			Foreground(colorMutedBlue)
)

var (
	styleDivider = lipgloss.NewStyle().
			Foreground(colorDivider)

	styleSectionLabel = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorMutedBlue)
)

func sectionDivider(w int) string {
	return styleDivider.Render(strings.Repeat("─", w))
}

func verticalDivider(h int) string {
	lines := make([]string, h)
	for i := range lines {
		lines[i] = styleDivider.Render("│")
	}
	return strings.Join(lines, "\n")
}

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

var (
	styleTrackName = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorOffWhite)

	styleArtistName = lipgloss.NewStyle().
			Foreground(colorBlue)

	styleAlbumName = lipgloss.NewStyle().
			Foreground(colorMutedBlue)
)

var (
	styleSectionTitle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBlue)

	styleQueueHeader = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	styleQueueCurrentTrack = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBlue)

	styleQueueCurrentArtist = lipgloss.NewStyle().
				Foreground(colorBlueLight)

	styleQueueCurrentDur = lipgloss.NewStyle().
				Foreground(colorBlueLight)

	styleQueueIndex = lipgloss.NewStyle().
			Foreground(colorMutedBlue)

	styleQueueTrack = lipgloss.NewStyle().
			Foreground(colorGray)

	styleQueueArtist = lipgloss.NewStyle().
				Foreground(colorMutedBlue)

	styleQueueDuration = lipgloss.NewStyle().
				Foreground(colorMutedBlue)
)

var (
	stylePlayerSeparator = lipgloss.NewStyle().
				Foreground(colorDivider)

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

func newPlaylistModalDelegate(searching bool) list.DefaultDelegate {
	d := newPlaylistDelegate()
	d.ShowDescription = true
	d.SetSpacing(0)

	// Keep author text subtle in modal by default.
	d.Styles.NormalDesc = d.Styles.NormalDesc.Foreground(colorGray)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.Foreground(colorGray)
	d.Styles.DimmedDesc = d.Styles.DimmedDesc.Foreground(colorGray)

	if searching {
		// In search mode, dim both lines while keeping different tones.
		d.Styles.NormalTitle = d.Styles.NormalTitle.Foreground(colorGray)
		d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(colorGray)
		d.Styles.DimmedTitle = d.Styles.DimmedTitle.Foreground(colorMutedBlue)

		d.Styles.NormalDesc = d.Styles.NormalDesc.Foreground(colorMutedBlue)
		d.Styles.SelectedDesc = d.Styles.SelectedDesc.Foreground(colorMutedBlue)
		d.Styles.DimmedDesc = d.Styles.DimmedDesc.Foreground(colorDivider)
	}

	return d
}

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
