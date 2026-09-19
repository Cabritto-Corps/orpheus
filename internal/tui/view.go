package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
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

func (m model) View() string {
	if m.ui.width < 40 || m.ui.height < 12 {
		return styleError.Render("terminal too small — please resize") + m.kittyOverlay()
	}

	header := m.headerView()

	if m.ui.helpOpen {
		return m.helpModalView() + m.kittyOverlay()
	}
	if m.ui.settings.open {
		return m.settingsModalView() + m.kittyOverlay()
	}
	if m.ui.trackPopupOpen {
		return m.trackPopupView() + m.kittyOverlay()
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

	// Three panels on the existing line divisions: the header band ends
	// exactly at the tab underline, the middle and the footer share the
	// page tone. Solid mode drops the band so the frame is one surface.
	// paintPage assigns each line its background in a single pass.
	parts := []string{header, tabBar, body, m.playerBarView()}
	return paintPage(lipgloss.JoinVertical(lipgloss.Left, parts...), m.ui.width) + m.kittyOverlay()
}

// bgSequence returns the terminal SGR that sets c as the background
// (profile-aware, so ANSI palettes quantize correctly), or "" on ASCII
// profiles where a themed background is not representable.
func bgSequence(c lipgloss.Color) string {
	if c == "" {
		return ""
	}
	profile := lipgloss.DefaultRenderer().ColorProfile()
	if profile == termenv.Ascii {
		return ""
	}
	st := termenv.Style{}.Background(profile.Color(string(c)))
	return strings.TrimSuffix(st.Styled(""), termenv.CSI+termenv.ResetSeq+"m")
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
	return strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+seq)
}

// paintBand fills every line of a band with a background tone that
// survives inner resets: the sequence is asserted at the line start,
// re-asserted after each reset, and forced again under the trailing
// padding (which would otherwise inherit an inner element's own bg).
func paintBand(band string, width int, bg lipgloss.Color) string {
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
func paintPage(frame string, width int) string {
	return paintBand(frame, width, colorPage)
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
	return themePlayPauseGlyphs()
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
func gradientBar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	frac = max(0, min(1, frac))
	p := progress.New(
		progress.WithWidth(width),
		progress.WithoutPercentage(),
		progress.WithGradient(string(colorBlue), string(colorBlueLight)),
	)
	full, empty := themeBarRunes()
	p.Full, p.Empty = full, empty
	// bubbles' defaults are hardcoded hexes (#606060 empty); the theme's
	// own gray keeps the empty track inside the palette.
	p.EmptyColor = string(colorGray)
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
