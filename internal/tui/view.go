package tui

import (
	"fmt"
	"image/color"
	"os"
	"strings"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

const (
	headerH    = 3
	tabBarH    = 2
	playerBarH = 2
	// playerBarGap is the spacing between the bar's four segments (state
	// icon, elapsed, progress, total); playerBarGaps is how many gaps that
	// spacing fills. The bar's width budget was an unexplained magic 8.
	playerBarGap  = 2
	playerBarGaps = 4
	volumeBarW    = 6
	gaugeW        = 6
	// spareLineH is the deliberately empty last row: kitty overlays place
	// images absolutely, and a frame that fills the terminal's last row
	// makes the terminal scroll under the renderer, shifting every
	// absolute placement each frame (observed: covers vanish).
	footerH = 1
)

const chromeH = headerH + tabBarH + playerBarH + footerH

const bodyStartRow1Based = headerH + tabBarH + 1

const minLeftW = 18
const minRightW = 28

const (
	iconPlay            = "▶"
	iconPause           = "⏸"
	iconDevice          = "●"
	iconShuffle         = "⇄"
	iconRepeatContext   = "↻"
	iconRepeatTrack     = "↻¹"
	iconPlayNF          = "\uf04b"
	iconPauseNF         = "\uf04c"
	iconDeviceNF        = "\uf108"
	iconShuffleNF       = "\uf074"
	iconRepeatContextNF = "\uf0b6"
	iconRepeatTrackNF   = "\uf01e"
)

func (m model) View() tea.View {
	if m.ui.width < 40 || m.ui.height < 12 {
		// No kitty overlay here: the error branch must never emit one.
		return tea.View{Content: m.styles.styleError.Render("terminal too small — please resize") + m.kittyOverlay(), AltScreen: true}
	}

	header := m.headerView()

	if m.ui.helpOpen {
		return tea.View{Content: m.helpModalView() + m.kittyOverlay(), AltScreen: true}
	}
	if m.ui.settings.open {
		return tea.View{Content: m.settingsModalView() + m.kittyOverlay(), AltScreen: true}
	}
	if m.ui.trackPopupOpen {
		return tea.View{Content: m.trackPopupView() + m.kittyOverlay(), AltScreen: true}
	}

	tabBar := m.tabBarView()

	var body string
	switch m.ui.activeTab {
	case tabPlaylists:
		body = m.playlistsTabView()
	case tabAlbums:
		body = m.albumsTabView()
	default:
		body = m.playbackScreenView()
	}

	parts := []string{header, tabBar, body, m.playerBarView()}
	switch {
	case m.styles.transparentFrame():
		// No frame paint at all: the terminal's own background shows
		// through every zone. Selection and modal chrome keep their own
		// backgrounds (they are overlays, not zones).
		return tea.View{Content: lipgloss.JoinVertical(lipgloss.Left, parts...) + m.kittyOverlay(), AltScreen: true}
	default:
		// Solid — and anything unexpected: one uniform surface.
		return tea.View{Content: m.styles.paintPage(lipgloss.JoinVertical(lipgloss.Left, parts...), m.ui.width) + m.kittyOverlay(), AltScreen: true}
	}
}

// colorEnabled reports whether the terminal wants colors: NO_COLOR, dumb
// and non-TTY environments (the Ascii/NoTTY profiles) disable color.
// Everything else renders full-fidelity and lets the v2 renderer
// downsample at output. colorprofile.Env reads the live environment with
// no globals, so tests control it with t.Setenv instead of mutating a
// shared renderer.
func colorEnabled() bool {
	switch colorprofile.Env(os.Environ()) {
	case colorprofile.Ascii, colorprofile.NoTTY:
		return false
	default:
		return true
	}
}

// bgSequence returns the terminal SGR that sets c as the background, or
// "" where a themed background is not representable (no-color
// profiles). Full fidelity is always emitted: the v2 renderer
// downsamples raw SGR in frame content at output, so no per-profile
// quantization happens here.
func bgSequence(c color.Color) string {
	if c == nil || !colorEnabled() {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// reassertBgLines asserts the background at every line start as well as
// after every reset: lipgloss draws box borders with the border-foreground
// style only (no background), so border-ring lines would otherwise print
// on the terminal's default color.
func reassertBgLines(text, seq string) string {
	if seq == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = seq + reassertBg(l, seq)
	}
	return strings.Join(lines, "\n")
}

// reassertBg re-emits the background sequence after every style reset.
// Terminals have no layers: an inner style's trailing reset clears the
// active background for the rest of the line, so a painted band would show
// holes wherever styled fragments, icons or glyphs sit. Re-asserting after
// every reset composes the band UNDER every element instead.
func reassertBg(text, seq string) string {
	if seq == "" {
		return text
	}
	// Match both reset spellings: v1 terminated styles with \x1b[0m,
	// v2 emits the abbreviated \x1b[m. Neither is a substring of the
	// other, so order is irrelevant.
	out := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+seq)
	return strings.ReplaceAll(out, "\x1b[m", "\x1b[m"+seq)
}

// paintBand fills every line of a band with a background tone that
// survives inner resets: the sequence is asserted at the line start,
// re-asserted after each reset, and forced again under the trailing
// padding (which would otherwise inherit an inner element's own bg).
func paintBand(band string, width int, bg color.Color) string {
	seq := bgSequence(bg)
	if seq == "" {
		return band
	}
	lines := strings.Split(band, "\n")
	for i, l := range lines {
		pad := max(0, width-lipgloss.Width(l))
		lines[i] = seq + reassertBg(l, seq) + "\x1b[0m" + seq + strings.Repeat(" ", pad) + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// paintPage fills the whole frame with the theme's page tone: every line
// is padded to the terminal width and drawn on the page background, so a
// theme owns the full canvas instead of the terminal's default color.
func (s *themeStyles) paintPage(frame string, width int) string {
	return paintBand(frame, width, s.colorPage)
}

type bodyLayout struct {
	bodyH         int
	leftW         int
	rightW        int
	coverCols     int
	coverRows     int
	coverStartRow int
	coverStartCol int
}

func (m model) bodyLayout() bodyLayout {
	bodyH := m.ui.height - chromeH
	if m.ui.width <= 0 || m.ui.height <= 0 {
		return bodyLayout{bodyH: bodyH, leftW: minLeftW, rightW: m.ui.width - minLeftW, coverStartRow: bodyStartRow1Based + 2, coverStartCol: 1}
	}
	metaLines := 3
	availH := max(bodyH-2-2-metaLines, 1)
	maxRows := availH
	coverRows := maxRows
	coverCols := 2 * coverRows
	leftW := max(coverCols+2, minLeftW)
	maxLeftW := max(m.ui.width-minRightW, minLeftW)
	if leftW > maxLeftW {
		leftW = maxLeftW
	}
	innerW := leftW - 2
	innerH := max(bodyH-2-metaLines, 1)
	coverCols, coverRows = squareDims(innerW, innerH)
	if coverCols < 2 {
		coverCols = 2
	}
	if coverRows < 1 {
		coverRows = 1
	}
	rightW := max(m.ui.width-leftW, 0)
	return bodyLayout{
		bodyH:         bodyH,
		leftW:         leftW,
		rightW:        rightW,
		coverCols:     coverCols,
		coverRows:     coverRows,
		coverStartRow: bodyStartRow1Based + 2,
		coverStartCol: 1,
	}
}

func (m model) currentCoverSizes() [][2]int {
	if m.ui.width <= 0 || m.ui.height <= 0 {
		return nil
	}
	layout := m.bodyLayout()
	if layout.coverCols <= 0 || layout.coverRows <= 0 {
		return nil
	}
	return [][2]int{{layout.coverCols, layout.coverRows}}
}

func (m model) icon(unicode, nerd string) string {
	if m.ui.nerdFonts {
		return nerd
	}
	return unicode
}

// playPauseGlyphs returns the theme's transport pair, upgraded to the
// nerd-font glyphs when the terminal advertises them.
func (m model) playPauseGlyphs() (play, pause string) {
	if m.ui.nerdFonts {
		return iconPlayNF, iconPauseNF
	}
	return m.styles.themePlayPauseGlyphs()
}

func (m model) selectedPlaylist() (playlistItem, bool) {
	sel, ok := m.browse.playlistList.SelectedItem().(playlistItem)
	return sel, ok
}

func (m model) selectedAlbum() (playlistItem, bool) {
	sel, ok := m.browse.albumList.SelectedItem().(playlistItem)
	return sel, ok
}

// gradientBar renders a filled/empty bar through bubbles/progress so the
// color ramp and width handling follow the framework; colors resolve to the
// live theme on every render.
func (s *themeStyles) gradientBar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	frac = max(0, min(1, frac))
	p := progress.New(
		progress.WithWidth(width),
		progress.WithoutPercentage(),
		progress.WithColors(s.colorBlue, s.colorBlueLight),
	)
	full, empty := s.themeBarRunes()
	p.Full, p.Empty = full, empty
	// bubbles' defaults are hardcoded hexes (#606060 empty); the theme's
	// own gray keeps the empty track inside the palette.
	p.EmptyColor = s.colorGray
	return p.ViewAs(frac)
}

func fmtDuration(ms int) string {
	s := ms / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, (s/60)%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return ansi.Truncate(s, max-1, "") + "…"
}

// padCell pads s with spaces to width display cells (not bytes).
func padCell(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// fitCell truncates s to width display cells with an ellipsis, escape-aware.
func fitCell(s string, width int) string {
	return truncate(s, width)
}
