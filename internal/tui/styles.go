package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

var (
	colorBlue      = lipgloss.Color(themePresets["default"].Blue)
	colorBlueLight = lipgloss.Color(themePresets["default"].BlueLight)
	colorOffWhite  = lipgloss.Color(themePresets["default"].OffWhite)
	colorGray      = lipgloss.Color(themePresets["default"].Gray)
	colorMutedBlue = lipgloss.Color(themePresets["default"].MutedBlue)
	colorDimBlue   = lipgloss.Color(themePresets["default"].DimBlue)
	colorDivider   = lipgloss.Color(themePresets["default"].Divider)
	colorError     = lipgloss.Color(themePresets["default"].Error)
)

// applyTheme rebuilds every package-level style from the resolved theme
// colors. It must be called once at model construction, before any delegate
// or help model is built, and never concurrently with rendering.
func applyTheme(c themeColors) {
	themeEpoch++
	resetStringCaches()
	colorBlue = lipgloss.Color(c.Blue)
	colorBlueLight = lipgloss.Color(c.BlueLight)
	colorOffWhite = lipgloss.Color(c.OffWhite)
	colorGray = lipgloss.Color(c.Gray)
	colorMutedBlue = lipgloss.Color(c.MutedBlue)
	colorDimBlue = lipgloss.Color(c.DimBlue)
	colorDivider = lipgloss.Color(c.Divider)
	colorError = lipgloss.Color(c.Error)

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

	styleError = lipgloss.NewStyle().
		Foreground(colorError)

	styleDimmed = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleDivider = lipgloss.NewStyle().
		Foreground(colorDivider)

	styleSectionLabel = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorMutedBlue)

	stylePlaylistName = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorOffWhite)

	stylePlaylistOwner = lipgloss.NewStyle().
		Foreground(colorGray)

	styleTrackName = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorOffWhite)

	styleArtistName = lipgloss.NewStyle().
		Foreground(colorGray)

	styleAlbumName = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleQueueHeader = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleQueueIndex = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleQueueTrack = lipgloss.NewStyle().
		Foreground(colorGray)

	styleQueueArtist = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleQueueCursor = lipgloss.NewStyle().
		Foreground(colorBlueLight)

	stylePlayerTime = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleProgressBarFilled = lipgloss.NewStyle().
		Foreground(colorBlue)

	styleProgressBarEmpty = lipgloss.NewStyle().
		Foreground(colorDivider)

	styleVolumeBarFilled = lipgloss.NewStyle().
		Foreground(colorBlue)

	styleVolumeBarEmpty = lipgloss.NewStyle().
		Foreground(colorDivider)

	stylePlaceholderBorder = lipgloss.NewStyle().
		Foreground(colorDivider)

	styleTrackPopupTitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorBlue)

	styleTrackPopupLoading = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleTrackPopupHint = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleTabActive = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorOffWhite).
		Background(colorDimBlue)

	styleTabInactive = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleModalBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Padding(0, 1)

	styleModalTitle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorBlue)

	styleModalHint = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleModalSelectedRow = lipgloss.NewStyle().
		Background(colorDimBlue).
		Foreground(colorOffWhite)
}

var (
	styleHeaderStatus      lipgloss.Style
	styleHeaderPlaying     lipgloss.Style
	styleHeaderPaused      lipgloss.Style
	styleHeaderCenter      lipgloss.Style
	styleHeaderSub         lipgloss.Style
	styleHeaderVolume      lipgloss.Style
	styleError             lipgloss.Style
	styleDimmed            lipgloss.Style
	styleDivider           lipgloss.Style
	styleSectionLabel      lipgloss.Style
	stylePlaylistName      lipgloss.Style
	stylePlaylistOwner     lipgloss.Style
	styleTrackName         lipgloss.Style
	styleArtistName        lipgloss.Style
	styleAlbumName         lipgloss.Style
	styleQueueHeader       lipgloss.Style
	styleQueueIndex        lipgloss.Style
	styleQueueTrack        lipgloss.Style
	styleQueueArtist       lipgloss.Style
	styleQueueCursor       lipgloss.Style
	stylePlayerTime        lipgloss.Style
	styleProgressBarFilled lipgloss.Style
	styleProgressBarEmpty  lipgloss.Style
	styleVolumeBarFilled   lipgloss.Style
	styleVolumeBarEmpty    lipgloss.Style
	stylePlaceholderBorder lipgloss.Style
	styleTrackPopupTitle   lipgloss.Style
	styleTrackPopupLoading lipgloss.Style
	styleTrackPopupHint    lipgloss.Style
	styleTabActive         lipgloss.Style
	styleTabInactive       lipgloss.Style
	styleModalBox          lipgloss.Style
	styleModalTitle        lipgloss.Style
	styleModalHint         lipgloss.Style
	styleModalSelectedRow  lipgloss.Style
)

func init() {
	applyTheme(themePresets["default"])
}

func sectionDivider(w int) string {
	return styleDivider.Render(strings.Repeat("─", w))
}

func verticalDivider(h int) string {
	if h <= 0 {
		return ""
	}
	line := styleDivider.Render("│")
	return strings.Repeat(line+"\n", h-1) + line
}

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
		Foreground(colorMutedBlue).
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
		Foreground(colorDimBlue).
		Padding(0, 0, 0, 2)

	d.Styles.DimmedDesc = lipgloss.NewStyle().
		Foreground(colorMutedBlue).
		Padding(0, 0, 0, 2)

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
