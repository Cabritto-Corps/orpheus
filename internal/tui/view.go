package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	headerH    = 3
	tabBarH    = 2
	playerBarH = 2
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
	iconVolume          = "▪"
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

	bar := m.playerBarView()
	parts := []string{header, tabBar, body, bar}
	return strings.Join(parts, "\n") + m.kittyOverlay()
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

func (m model) selectedPlaylist() (playlistItem, bool) {
	sel, ok := m.browse.playlistList.SelectedItem().(playlistItem)
	return sel, ok
}

func (m model) selectedAlbum() (playlistItem, bool) {
	sel, ok := m.browse.albumList.SelectedItem().(playlistItem)
	return sel, ok
}

func (m model) renderProgressBar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	filled := min(int(pct*float64(width)), width)
	empty := width - filled
	return styleProgressBarFilled.Render(strings.Repeat("█", filled)) +
		styleProgressBarEmpty.Render(strings.Repeat("░", empty))
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

func centerText(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return s
	}
	pad := (w - sw) / 2
	return strings.Repeat(" ", pad) + s + strings.Repeat(" ", w-sw-pad)
}

func (m *model) getBodyLayout() bodyLayout {
	if m.ui.cachedBodyLayoutValid {
		return m.ui.cachedBodyLayout
	}
	layout := m.bodyLayout()
	m.ui.cachedBodyLayout = layout
	m.ui.cachedBodyLayoutValid = true
	return layout
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
