package tui

import (
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

const (
	headerH       = 3
	tabBarH       = 2
	playerBarH    = 2
	playerBarGap  = 2
	playerBarGaps = 4
	volumeBarW    = 6
	gaugeW        = 6
	// The last row stays empty: a frame touching it scrolls the terminal
	// under the renderer, shifting absolute kitty placements (covers vanish).
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
		// No overlay: absolute placements cannot coordinate against a
		// frame that never laid out.
		return tea.View{Content: m.styles.styleError.Render("terminal too small — please resize"), AltScreen: true}
	}
	return tea.View{Content: m.mainView(), AltScreen: true}
}

func (m model) mainView() string {
	if kind := m.modalKind(); kind != modalNone {
		return m.modalView(kind)
	}
	return m.pageView()
}

func (m model) modalView(kind modalKind) string {
	switch kind {
	case modalHelp:
		return m.helpModalView()
	case modalTrackPopup:
		return m.trackPopupView()
	case modalNone:
		return ""
	default:
		return m.settingsModalView()
	}
}

func (m model) pageView() string {
	header := m.headerView()

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
		// Selection and modal chrome keep their own backgrounds (they
		// are overlays, not zones) while every zone shows the terminal.
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	default:
		return m.styles.paintPage(lipgloss.JoinVertical(lipgloss.Left, parts...), m.ui.width)
	}
}

// No per-profile quantization here: the v2 renderer downsamples raw
// SGR in frame content at output, so full fidelity is always emitted.
func (s *themeStyles) bgSequence(c color.Color) string {
	if c == nil || s.colorProfile <= colorprofile.Ascii {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// Lipgloss borders carry the border-foreground only (no background),
// so border-ring lines would otherwise print on the terminal default.
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

// Terminals have no layers: an inner reset clears the background for
// the rest of the line, so a painted band would show holes wherever
// styled fragments, icons or glyphs sit.
func reassertBg(text, seq string) string {
	if seq == "" {
		return text
	}
	// Match both reset spellings (\x1b[0m and \x1b[m); neither is a
	// substring of the other, so order is irrelevant.
	out := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+seq)
	return strings.ReplaceAll(out, "\x1b[m", "\x1b[m"+seq)
}

// Trailing padding is painted too: it would otherwise inherit an
// inner element's own background.
func (s *themeStyles) paintBand(band string, width int, bg color.Color) string {
	seq := s.bgSequence(bg)
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

func (s *themeStyles) paintPage(frame string, width int) string {
	return s.paintBand(frame, width, s.colorPage)
}

type bodyLayout struct {
	bodyH     int
	leftW     int
	rightW    int
	coverCols int
	coverRows int
}

func (m model) bodyLayout() bodyLayout {
	bodyH := m.ui.height - chromeH
	if m.ui.width <= 0 || m.ui.height <= 0 {
		return bodyLayout{bodyH: bodyH, leftW: minLeftW, rightW: m.ui.width - minLeftW}
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
		bodyH:     bodyH,
		leftW:     leftW,
		rightW:    rightW,
		coverCols: coverCols,
		coverRows: coverRows,
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

// Routed through bubbles/progress so the framework owns the color
// ramp and width handling.
func (s *themeStyles) gradientBar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	frac = max(0, min(1, frac))
	p := s.barProgress
	p.SetWidth(width)
	return p.ViewAs(frac)
}

func fmtDuration(ms int) string {
	// Dealer payloads (stale transfers, past-end starts) can carry
	// negative positions; rendering must never show negative time.
	if ms < 0 {
		ms = 0
	}
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
	// The tail counts against the budget (handled by the library).
	return ansi.Truncate(s, max, "…")
}

// Width is display cells, not bytes.
func padCell(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

func fitCell(s string, width int) string {
	return truncate(s, width)
}
