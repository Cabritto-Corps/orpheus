package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

var (
	defaultColors  = themePreset("default")
	colorBlue      = lipgloss.Color(defaultColors.Blue)
	colorBlueLight = lipgloss.Color(defaultColors.BlueLight)
	colorOffWhite  = lipgloss.Color(defaultColors.OffWhite)
	colorGray      = lipgloss.Color(defaultColors.Gray)
	colorMutedBlue = lipgloss.Color(defaultColors.MutedBlue)
	colorDimBlue   = lipgloss.Color(defaultColors.DimBlue)
	colorDivider   = lipgloss.Color(defaultColors.Divider)
	colorError     = lipgloss.Color(defaultColors.Error)
	colorScrim     = lipgloss.Color(defaultColors.Scrim)

	colorSelectionFg = lipgloss.Color(defaultColors.SelectionFg)
	colorSelectionBg = lipgloss.Color(defaultColors.SelectionBg)
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
	colorScrim = lipgloss.Color(c.Scrim)
	colorSelectionFg = lipgloss.Color(c.SelectionFg)
	colorSelectionBg = lipgloss.Color(c.SelectionBg)

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

	styleQueueSelected = lipgloss.NewStyle().
		Foreground(colorSelectionFg).
		Background(colorSelectionBg)

	styleQueuePlaying = lipgloss.NewStyle().
		Foreground(colorBlue).
		Bold(true)

	stylePlayerTime = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleProgressBarFilled = lipgloss.NewStyle().
		Foreground(colorBlue)

	styleProgressBarEmpty = lipgloss.NewStyle().
		Foreground(colorGray)

	styleVolumeBarFilled = lipgloss.NewStyle().
		Foreground(colorBlue)

	styleVolumeBarEmpty = lipgloss.NewStyle().
		// The empty track must stay visible on dark terminals; the divider
		// color reads as a gap there (~1.3:1).
		Foreground(colorGray)

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
		Background(colorSelectionBg).
		Foreground(colorSelectionFg)

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
	styleQueueSelected     lipgloss.Style
	styleQueuePlaying      lipgloss.Style
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
	applyTheme(themePreset("default"))
}

func sectionDivider(w int) string {
	return styleDivider.Render(strings.Repeat("─", max(0, w)))
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

	l.Styles.ArabicPagination = lipgloss.NewStyle().
		Foreground(colorMutedBlue)
}
