package tui

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Styles are values on the model, never globals: a theme change swaps
// the pointer, so every cache starts cold and no invalidation machinery
// is needed.
type themeStyles struct {
	colorBlue, colorBlueLight, colorOffWhite, colorGray, colorMutedBlue, colorDimBlue color.Color
	colorDivider, colorError, colorScrim                                              color.Color
	colorSelectionFg, colorSelectionBg, colorPage, colorPanel                         color.Color

	themeBoldTitles, themeItalicDescs bool
	activeGlyphs                      themeGlyphs
	activeCover                       themeCover
	activeBackgrounds                 themeBackgrounds

	styleHeaderStatus, styleHeaderPlaying, styleHeaderPaused              lipgloss.Style
	styleHeaderCenter, styleHeaderSub, styleHeaderVolume                  lipgloss.Style
	styleError, styleDimmed, styleDivider, styleSectionLabel              lipgloss.Style
	stylePlaylistName, stylePlaylistOwner                                 lipgloss.Style
	styleTrackName, styleArtistName, styleAlbumName                       lipgloss.Style
	styleQueueHeader, styleQueueTrack, styleQueueCursor                   lipgloss.Style
	styleQueueSelected, stylePlayerTime, styleProgressBarEmpty            lipgloss.Style
	styleTrackPopupTitle, styleTrackPopupLoading, styleTrackPopupHint     lipgloss.Style
	styleTabActive, styleTabInactive                                      lipgloss.Style
	styleModalBox, styleModalTitle, styleModalHint, styleModalSelectedRow lipgloss.Style

	tabBar      *stringCache[tabBarCacheKey]
	placeholder *stringCache[placeholderCacheKey]

	// One fg-block style per palette color, so the picker doesn't rebuild
	// styles per cell per frame.
	help         help.Model
	swatchStyles map[color.Color]lipgloss.Style

	// Width is set per render on a copy, so the frame path allocates
	// nothing (gradientBar used to construct one per call).
	barProgress progress.Model

	// Read once: hot paint paths compare instead of re-parsing the
	// environment per line (it is process-fixed).
	colorProfile colorprofile.Profile
}

// A barely-there lift keeps the frame continuous (modal boxes stay the
// only differentiated surfaces) without banded contrast breaks.
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

// Non-hex inputs (ANSI names, 0-15 indices) return "" so callers fall
// back gracefully.
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

func buildThemeStyles(st themeState) *themeStyles {
	s := &themeStyles{
		tabBar:       newStringCache[tabBarCacheKey](),
		placeholder:  newStringCache[placeholderCacheKey](),
		help:         help.New(),
		swatchStyles: map[color.Color]lipgloss.Style{},
		colorProfile: colorprofile.Env(os.Environ()),
	}
	s.colorBlue = lipgloss.Color(st.colors.Blue)
	s.colorBlueLight = lipgloss.Color(st.colors.BlueLight)
	s.colorOffWhite = lipgloss.Color(st.colors.OffWhite)
	s.colorGray = lipgloss.Color(st.colors.Gray)
	s.colorMutedBlue = lipgloss.Color(st.colors.MutedBlue)
	s.colorDimBlue = lipgloss.Color(st.colors.DimBlue)
	s.colorDivider = lipgloss.Color(st.colors.Divider)
	s.colorError = lipgloss.Color(st.colors.Error)
	s.colorScrim = lipgloss.Color(st.colors.Scrim)
	s.colorSelectionFg = lipgloss.Color(st.colors.SelectionFg)
	s.colorSelectionBg = lipgloss.Color(st.colors.SelectionBg)
	s.colorPage = lipgloss.Color(st.colors.Page)
	panel := st.colors.Panel
	if panel == "" {
		panel = panelFromPage(st.colors.Page)
	}
	s.colorPanel = lipgloss.Color(panel)
	s.themeBoldTitles = st.typography.BoldTitles
	s.themeItalicDescs = st.typography.ItalicDescs
	s.activeGlyphs = st.glyphs
	s.activeCover = st.cover
	s.activeBackgrounds = st.backgrounds

	full, empty := s.themeBarRunes()
	s.barProgress = progress.New(
		progress.WithoutPercentage(),
		progress.WithColors(s.colorBlue, s.colorBlueLight),
		progress.WithFillCharacters(full, empty),
	)
	s.barProgress.EmptyColor = s.colorGray

	s.styleHeaderStatus = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleHeaderPlaying = lipgloss.NewStyle().
		Foreground(s.colorBlue).
		Bold(true)

	s.styleHeaderPaused = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleHeaderCenter = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorOffWhite)

	s.styleHeaderSub = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleHeaderVolume = lipgloss.NewStyle().
		Foreground(s.colorGray)

	s.styleError = lipgloss.NewStyle().
		Foreground(s.colorError)

	s.styleDimmed = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleDivider = lipgloss.NewStyle().
		Foreground(s.colorDivider)

	s.styleSectionLabel = lipgloss.NewStyle().
		Bold(true).
		Foreground(s.colorMutedBlue)

	s.stylePlaylistName = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorOffWhite)

	s.stylePlaylistOwner = lipgloss.NewStyle().
		Italic(s.themeItalicDescs).
		Foreground(s.colorGray)

	s.styleTrackName = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorOffWhite)

	s.styleArtistName = lipgloss.NewStyle().
		Italic(s.themeItalicDescs).
		Foreground(s.colorGray)

	s.styleAlbumName = lipgloss.NewStyle().
		Italic(s.themeItalicDescs).
		Foreground(s.colorMutedBlue)

	s.styleQueueHeader = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleQueueTrack = lipgloss.NewStyle().
		Foreground(s.colorGray)

	s.styleQueueCursor = lipgloss.NewStyle().
		Foreground(s.colorBlueLight)

	s.styleQueueSelected = lipgloss.NewStyle().
		Foreground(s.colorSelectionFg).
		Background(s.colorSelectionBg)

	s.stylePlayerTime = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleProgressBarEmpty = lipgloss.NewStyle().
		Foreground(s.colorGray)

	s.styleTrackPopupTitle = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorBlue)

	s.styleTrackPopupLoading = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleTrackPopupHint = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleTabActive = lipgloss.NewStyle().
		Bold(true).
		Foreground(s.colorOffWhite).
		Background(s.colorDimBlue)

	s.styleTabInactive = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	modalBg := s.modalBoxBackground()
	s.styleModalBox = lipgloss.NewStyle().
		Border(s.themeBorder()).
		BorderForeground(s.colorBlue).
		Background(modalBg).
		Padding(0, 1)

	s.styleModalTitle = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorBlue)

	s.styleModalHint = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	s.styleModalSelectedRow = lipgloss.NewStyle().
		Background(s.colorSelectionBg).
		Foreground(s.colorSelectionFg)

	return s
}

// Mode checks read this instead of comparing style strings, so a new
// mode cannot silently inherit painted behavior.
func (s *themeStyles) transparentFrame() bool {
	return s.activeBackgrounds.Style == "transparent"
}

// Solid flattens to the page (one surface); transparent keeps the
// panel tone — the box is floating chrome and would otherwise dissolve
// into the terminal behind it.
func (s *themeStyles) modalBoxBackground() color.Color {
	if s.activeBackgrounds.Style == "solid" {
		return s.colorPage
	}
	return s.colorPanel
}

func (s *themeStyles) themeBorder() lipgloss.Border {
	switch s.activeGlyphs.Border {
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

func (s *themeStyles) themeBarRunes() (full, empty rune) {
	if s.activeGlyphs.Bar == "line" {
		return '━', '─'
	}
	return '█', '░'
}

func (s *themeStyles) themePlayPauseGlyphs() (play, pause string) {
	switch s.activeGlyphs.PlayPause {
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

func (s *themeStyles) themeNowPlayingGlyph() string {
	switch s.activeGlyphs.NowPlaying {
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

func (s *themeStyles) coverFrameBorder() (lipgloss.Border, bool) {
	switch s.activeCover.Frame {
	case "rounded":
		return lipgloss.RoundedBorder(), true
	case "thick":
		return lipgloss.ThickBorder(), true
	default:
		return lipgloss.Border{}, false
	}
}

func (s *themeStyles) coverFrameFits(cols, rows int) bool {
	_, ok := s.coverFrameBorder()
	return ok && cols >= 6 && rows >= 3
}

func themedSpinner(s *themeStyles) spinner.Model {
	preset := spinner.MiniDot
	switch s.activeGlyphs.Spinner {
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

func (s *themeStyles) sectionDivider(w int) string {
	return s.styleDivider.Render(strings.Repeat("─", max(0, w)))
}

func (s *themeStyles) verticalDivider(h int) string {
	if h <= 0 {
		return ""
	}
	line := s.styleDivider.Render("│")
	return strings.Repeat(line+"\n", h-1) + line
}

func newBrowseList(s *themeStyles, nowPlaying *string) list.Model {
	l := list.New(nil, newCachedPlaylistDelegate(s, nowPlaying), 40, 20)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowFilter(true)
	l.SetShowHelp(false)
	l.FilterInput.Prompt = "Search: "
	applyListStyles(&l, s)
	return l
}

func newPlaylistDelegate(s *themeStyles) list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.SetSpacing(0)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(s.colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Italic(s.themeItalicDescs).
		Foreground(s.colorMutedBlue).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(s.colorBlue).
		Padding(0, 0, 0, 1)

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(s.colorOffWhite).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Italic(s.themeItalicDescs).
		Foreground(s.colorMutedBlue).
		Padding(0, 0, 0, 2)

	d.Styles.DimmedTitle = lipgloss.NewStyle().
		Foreground(s.colorDimBlue).
		Padding(0, 0, 0, 2)

	d.Styles.DimmedDesc = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue).
		Padding(0, 0, 0, 2)

	return d
}

func applyListStyles(l *list.Model, s *themeStyles) {
	l.Styles.Title = lipgloss.NewStyle().
		Bold(s.themeBoldTitles).
		Foreground(s.colorOffWhite)

	l.Styles.TitleBar = lipgloss.NewStyle().
		Background(lipgloss.NoColor{}).
		Padding(0, 0, 1, 0)

	// v2 styles the prompt per focus state (v1 applied regardless), so
	// set both states.
	l.Styles.Filter.Focused.Prompt = lipgloss.NewStyle().
		Foreground(s.colorBlue)
	l.Styles.Filter.Blurred.Prompt = lipgloss.NewStyle().
		Foreground(s.colorBlue)

	l.Styles.Filter.Cursor.Color = s.colorBlueLight

	l.Styles.StatusBar = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	l.Styles.StatusEmpty = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	l.Styles.NoItems = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue).
		Padding(1, 2)

	l.Styles.PaginationStyle = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	l.Styles.ActivePaginationDot = lipgloss.NewStyle().
		Foreground(s.colorBlue).
		SetString("•")

	l.Styles.InactivePaginationDot = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue).
		SetString("•")

	l.Styles.HelpStyle = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)

	l.Styles.ArabicPagination = lipgloss.NewStyle().
		Foreground(s.colorMutedBlue)
}
