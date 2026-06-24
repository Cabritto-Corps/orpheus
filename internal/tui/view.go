package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	headerH    = 2
	tabBarH    = 2
	playerBarH = 1
	gapFooterH = 0
)

const chromeH = headerH + tabBarH + playerBarH + 2

const bodyStartRow1Based = headerH + tabBarH + 1

const minLeftW = 18
const minRightW = 28

const (
	iconPlay            = "▶"
	iconPause           = ""
	iconDevice          = "●"
	iconVolume          = "▪"
	iconShuffle         = ""
	iconRepeatContext   = ""
	iconRepeatTrack     = ""
	iconPlayNF          = "\uf04b"
	iconPauseNF         = "\uf04c"
	iconDeviceNF        = "\ue30c"
	iconVolumeNF        = "\uf028"
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
		return header + "\n" + m.helpModalView() + m.kittyOverlay()
	}

	if m.ui.trackPopupOpen {
		return header + "\n" + m.trackPopupView() + m.kittyOverlay()
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
	return header + "\n" + tabBar + "\n" + body + "\n" + bar + m.kittyOverlay()
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
	bodyH := m.ui.height - chromeH - 1
	if m.ui.width <= 0 || m.ui.height <= 0 {
		return bodyLayout{bodyH: bodyH, leftW: minLeftW, rightW: m.ui.width - minLeftW, coverStartRow: bodyStartRow1Based + 3, coverStartCol: 1}
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
		coverStartRow: bodyStartRow1Based + 3,
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
	limit := max - 1
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > limit {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

func centerText(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return s
	}
	pad := (w - sw) / 2
	return strings.Repeat(" ", pad) + s + strings.Repeat(" ", w-sw-pad)
}

func centerBlockLines(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = centerText(lines[i], w)
	}
	return strings.Join(lines, "\n")
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
