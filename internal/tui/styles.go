package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
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
	colorPage        = lipgloss.Color(defaultColors.Page)
	colorPanel       = lipgloss.Color(panelFromPage(defaultColors.Page))

	themeBoldTitles   bool
	themeItalicDescs  bool
	activeGlyphs      = defaultGlyphs
	activeCover       = defaultCover
	activeBackgrounds = defaultBackgrounds
)

// panelFromPage derives the panel tone from the page when a theme does not
// set one explicitly: a barely-there lift toward white keeps the frame
// continuous (the header band is the only differentiated surface, and the
// modal boxes float) without banded contrast breaks.
func panelFromPage(page string) string {
	if page == "" {
		return ""
	}
	lifted := mixHex(page, "#FFFFFF", 0.035)
	if lifted == "" {
		return page
	}
	return lifted
}

// mixChannel blends two 8-bit channels (t=0 → a, t=1 → b), clamped.
func mixChannel(a, b uint8, t float64) uint8 {
	v := float64(a) + (float64(b)-float64(a))*t
	if v < 0 {
		v = 0
	}
	if v > 255 {
		v = 255
	}
	return uint8(v + 0.5)
}

// mixHex blends two hex colors (t=0 → a, t=1 → b); non-hex inputs (ANSI
// names, 0-15 indices) return "" so callers can fall back gracefully.
func mixHex(a, b string, t float64) string {
	ra, ga, ba, ok := hexToRGB(a)
	if !ok {
		return ""
	}
	rb, gb, bb, ok := hexToRGB(b)
	if !ok {
		return ""
	}
	return fmt.Sprintf("#%02X%02X%02X", mixChannel(ra, rb, t), mixChannel(ga, gb, t), mixChannel(ba, bb, t))
}

func hexToRGB(s string) (r, g, b uint8, ok bool) {
	hex := strings.TrimPrefix(s, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	parse := func(s string) (uint8, bool) {
		v, err := strconv.ParseUint(s, 16, 8)
		return uint8(v), err == nil
	}
	r, ok = parse(hex[0:2])
	if !ok {
		return 0, 0, 0, false
	}
	g, ok = parse(hex[2:4])
	if !ok {
		return 0, 0, 0, false
	}
	b, ok = parse(hex[4:6])
	if !ok {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// applyTheme rebuilds every package-level style from the resolved theme
// state (palette + glyph + typography + cover layers). It must be called
// once at model construction, before any delegate or help model is built,
// and never concurrently with rendering.
func applyTheme(st themeState) {
	themeEpoch++
	resetStringCaches()
	colorBlue = lipgloss.Color(st.colors.Blue)
	colorBlueLight = lipgloss.Color(st.colors.BlueLight)
	colorOffWhite = lipgloss.Color(st.colors.OffWhite)
	colorGray = lipgloss.Color(st.colors.Gray)
	colorMutedBlue = lipgloss.Color(st.colors.MutedBlue)
	colorDimBlue = lipgloss.Color(st.colors.DimBlue)
	colorDivider = lipgloss.Color(st.colors.Divider)
	colorError = lipgloss.Color(st.colors.Error)
	colorScrim = lipgloss.Color(st.colors.Scrim)
	colorSelectionFg = lipgloss.Color(st.colors.SelectionFg)
	colorSelectionBg = lipgloss.Color(st.colors.SelectionBg)
	colorPage = lipgloss.Color(st.colors.Page)
	panel := st.colors.Panel
	if panel == "" {
		panel = panelFromPage(st.colors.Page)
	}
	colorPanel = lipgloss.Color(panel)
	themeBoldTitles = st.typography.BoldTitles
	themeItalicDescs = st.typography.ItalicDescs
	activeGlyphs = st.glyphs
	activeCover = st.cover
	activeBackgrounds = st.backgrounds

	styleHeaderStatus = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleHeaderPlaying = lipgloss.NewStyle().
		Foreground(colorBlue).
		Bold(true)

	styleHeaderPaused = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleHeaderCenter = lipgloss.NewStyle().
		Bold(themeBoldTitles).
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
		Bold(themeBoldTitles).
		Foreground(colorOffWhite)

	stylePlaylistOwner = lipgloss.NewStyle().
		Italic(themeItalicDescs).
		Foreground(colorGray)

	styleTrackName = lipgloss.NewStyle().
		Bold(themeBoldTitles).
		Foreground(colorOffWhite)

	styleArtistName = lipgloss.NewStyle().
		Italic(themeItalicDescs).
		Foreground(colorGray)

	styleAlbumName = lipgloss.NewStyle().
		Italic(themeItalicDescs).
		Foreground(colorMutedBlue)

	styleQueueHeader = lipgloss.NewStyle().
		Foreground(colorMutedBlue)

	styleQueueTrack = lipgloss.NewStyle().
		Foreground(colorGray)

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

	styleProgressBarEmpty = lipgloss.NewStyle().
		Foreground(colorGray)

	styleTrackPopupTitle = lipgloss.NewStyle().
		Bold(themeBoldTitles).
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

	modalBg := colorPanel
	if activeBackgrounds.Style == "solid" {
		modalBg = colorPage
	}
	styleModalBox = lipgloss.NewStyle().
		Border(themeBorder()).
		BorderForeground(colorBlue).
		Background(modalBg).
		Padding(0, 1)

	styleModalTitle = lipgloss.NewStyle().
		Bold(themeBoldTitles).
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
	styleQueueTrack        lipgloss.Style
	styleQueueCursor       lipgloss.Style
	styleQueueSelected     lipgloss.Style
	styleQueuePlaying      lipgloss.Style
	stylePlayerTime        lipgloss.Style
	styleProgressBarEmpty  lipgloss.Style
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

// themeBorder returns the theme's box border family for modal boxes and
// placeholder art (cover frames have their own border setting).
func themeBorder() lipgloss.Border {
	switch activeGlyphs.Border {
	case "thick":
		return lipgloss.ThickBorder()
	case "double":
		return lipgloss.DoubleBorder()
	case "ascii":
		return lipgloss.Border{
			Top: "-", Bottom: "-", Left: "|", Right: "|",
			TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
		}
	default:
		return lipgloss.RoundedBorder()
	}
}

// themeBarRunes returns the progress bar's full/empty runes.
func themeBarRunes() (full, empty rune) {
	if activeGlyphs.Bar == "line" {
		return '━', '─'
	}
	return '█', '░'
}

// themePlayPauseGlyphs returns the transport pair for the configured set;
// nerd-font terminals keep the NF glyphs (icon() decides).
func themePlayPauseGlyphs() (play, pause string) {
	switch activeGlyphs.PlayPause {
	case "bold":
		return "►", "⏸"
	case "thin":
		return "▷", "⏸"
	case "ascii":
		return ">", "||"
	default:
		return "▶", "⏸"
	}
}

// themeNowPlayingGlyph returns the now-playing marker for the configured
// set ("" for the plain style).
func themeNowPlayingGlyph() string {
	switch activeGlyphs.NowPlaying {
	case "dot":
		return "●"
	case "play":
		return "▶"
	case "arrow":
		return "→"
	default:
		return "♪"
	}
}

// coverFrameBorder returns the theme's cover frame border, ok=false when
// the frame style is "none".
func coverFrameBorder() (lipgloss.Border, bool) {
	switch activeCover.Frame {
	case "rounded":
		return lipgloss.RoundedBorder(), true
	case "thick":
		return lipgloss.ThickBorder(), true
	default:
		return lipgloss.Border{}, false
	}
}

// coverFrameFits reports whether a cell is large enough to inset the art
// inside a frame without starving it.
func coverFrameFits(cols, rows int) bool {
	_, ok := coverFrameBorder()
	return ok && cols >= 6 && rows >= 3
}

// themedSpinner constructs a bubbles spinner from the theme's style name.
func themedSpinner() spinner.Model {
	preset := spinner.MiniDot
	switch activeGlyphs.Spinner {
	case "dot":
		preset = spinner.Dot
	case "line":
		preset = spinner.Line
	case "points":
		preset = spinner.Points
	case "meter":
		preset = spinner.Meter
	case "pulse":
		preset = spinner.Pulse
	}
	return spinner.New(spinner.WithSpinner(preset))
}

func init() {
	applyTheme(themePresetState("default"))
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

// newBrowseList builds one of the two library browsers with the shared
// chrome configuration (chrome flags off, search prompt, themed styles).
func newBrowseList() list.Model {
	l := list.New(nil, newCachedPlaylistDelegate(), 40, 20)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowFilter(true)
	l.SetShowHelp(false)
	l.FilterInput.Prompt = "Search: "
	applyListStyles(&l)
	return l
}

func newPlaylistDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.SetSpacing(0)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Bold(themeBoldTitles).
		Foreground(colorBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Italic(themeItalicDescs).
		Foreground(colorMutedBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colorOffWhite).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Italic(themeItalicDescs).
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
		Bold(themeBoldTitles).
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
