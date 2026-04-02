package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	golibrespot "github.com/elxgy/go-librespot"
	"github.com/mattn/go-runewidth"

	"orpheus/internal/spotify"
)

const (
	headerH    = 2
	tabBarH    = 2
	playerBarH = 1
	gapFooterH = 0
)

const chromeH = headerH + tabBarH + playerBarH + 1

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
	if m.width < 40 || m.height < 12 {
		return styleError.Render("terminal too small — please resize") + m.kittyOverlay()
	}

	header := m.headerView()

	if m.helpOpen {
		return header + "\n" + m.helpModalView() + m.kittyOverlay()
	}

	if m.trackPopupOpen {
		return header + "\n" + m.trackPopupView() + m.kittyOverlay()
	}

	tabBar := m.tabBarView()

	var body string
	switch m.activeTab {
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

func (m model) headerView() string {
	w := m.width

	var statusStr, centerL1, rightL1 string

	if m.status != nil {
		playIcon := m.icon(iconPlay, iconPlayNF)
		pauseIcon := m.icon(iconPause, iconPauseNF)
		if m.status.Playing {
			statusStr = styleHeaderPlaying.Render("[" + playIcon + " Playing]")
		} else {
			statusStr = styleHeaderPaused.Render("[" + pauseIcon + " Paused]")
		}

		volBar := m.headerVolumeBar(m.status.Volume)
		volText := styleHeaderVolume.Render(fmt.Sprintf("%3d%%", m.status.Volume))
		rightL1 = volBar + " " + volText
		if m.status.ShuffleState {
			rightL1 += "  " + styleDimmed.Render(m.icon(iconShuffle, iconShuffleNF))
		}
		if m.status.RepeatTrack {
			rightL1 += "  " + styleDimmed.Render(m.icon(iconRepeatTrack, iconRepeatTrackNF))
		} else if m.status.RepeatContext {
			rightL1 += "  " + styleDimmed.Render(m.icon(iconRepeatContext, iconRepeatContextNF))
		}

		availCenterW := max(10, w-lipgloss.Width(statusStr)-lipgloss.Width(rightL1)-2)
		trackName := m.status.TrackName
		if trackName == "" {
			trackName = "Unknown track"
		}
		centerL1 = styleHeaderCenter.Render(truncate(trackName, availCenterW))
	} else {
		statusStr = styleHeaderPaused.Render("Orpheus")
		centerL1 = styleHeaderSub.Render("no active playback")
		rightL1 = styleHeaderStatus.Render(m.icon(iconDevice, iconDeviceNF) + " " + m.deviceName)
	}

	line1 := layoutThreeZone(w, statusStr, centerL1, rightL1)

	var centerL2 string
	if m.status != nil {
		artist := m.status.ArtistName
		album := m.status.AlbumName
		if artist == "" {
			artist = "-"
		}
		parts := artist
		if album != "" {
			parts += "  •  " + album
		}
		centerL2 = styleHeaderSub.Render(truncate(parts, max(1, w-2)))
	}
	line2 := layoutThreeZone(w, "", centerL2, "")

	sep := styleDivider.Render(strings.Repeat("─", w))
	return line1 + "\n" + line2 + "\n" + sep
}

func layoutThreeZone(w int, left, center, right string) string {
	leftW := lipgloss.Width(left)
	centerW := lipgloss.Width(center)
	rightW := lipgloss.Width(right)

	centerPad := (w - centerW) / 2
	if centerPad < leftW+1 {
		centerPad = leftW + 1
	}
	leftPad := centerPad - leftW
	if leftPad < 0 {
		leftPad = 0
	}
	afterCenter := centerPad + centerW
	rightPad := w - afterCenter - rightW
	if rightPad < 1 {
		rightPad = 1
	}

	return left +
		strings.Repeat(" ", leftPad) +
		center +
		strings.Repeat(" ", rightPad) +
		right
}

func (m model) headerVolumeBar(vol int) string {
	const w = 6
	volChar := m.icon(iconVolume, iconVolumeNF)
	filled := int(float64(vol) / 100.0 * float64(w))
	if filled > w {
		filled = w
	}
	return lipgloss.NewStyle().Foreground(colorBlue).Render(strings.Repeat(volChar, filled)) +
		lipgloss.NewStyle().Foreground(colorDivider).Render(strings.Repeat(volChar, w-filled))
}

func (m model) tabBarView() string {
	tabs := []struct {
		label string
		t     tab
	}{
		{"Playlists", tabPlaylists},
		{"Albums", tabAlbums},
		{"Player", tabPlayer},
	}
	var parts []string
	for _, entry := range tabs {
		if m.activeTab == entry.t {
			parts = append(parts, styleTabActive.Render(" "+entry.label+" "))
		} else {
			parts = append(parts, styleTabInactive.Render(" "+entry.label+" "))
		}
	}
	sep := styleDivider.Render("│")
	bar := strings.Join(parts, sep)
	underline := styleDivider.Render(strings.Repeat("─", m.width))
	return bar + "\n" + underline
}

func (m model) playlistsTabView() string {
	layout := m.bodyLayout()
	left := m.coverPreviewPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := verticalDivider(layout.bodyH)
	right := m.playlistBrowserPanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) playlistBrowserPanel(w, h int) string {
	count := len(m.playlistList.Items())
	label := styleSectionLabel.Render("Playlists")
	countStr := styleDimmed.Render(fmt.Sprintf("%d playlists", count))
	labelLine := label + "\n" + countStr + "\n" + sectionDivider(w-1)
	innerH := h - 3

	var inner string
	if m.playlistsErr != nil && len(m.playlistList.Items()) == 0 {
		errStr := "failed to load: " + m.playlistsErr.Error()
		rateHint := ""
		if strings.Contains(m.playlistsErr.Error(), "429") || strings.Contains(strings.ToLower(m.playlistsErr.Error()), "rate limit") {
			rateHint = "\n" + styleDimmed.Render("Run 'orpheus auth login' to use your own API quota.")
		}
		inner = styleError.Render(truncate(errStr, w-2)) + rateHint + "\n" + styleDimmed.Render("r to retry")
	} else if m.playlistsLoading && len(m.playlistList.Items()) == 0 {
		inner = styleDimmed.Render("loading library...")
	} else {
		m.playlistList.SetSize(w-1, innerH)
		inner = m.playlistList.View()
	}
	if m.albumsForbidden {
		inner += "\n" + styleDimmed.Render("saved albums unavailable: re-run 'orpheus auth login' (needs user-library-read)")
	}

	if m.playbackErr != nil {
		diag := spotify.DiagnoseError(m.playbackErr)
		errLine := m.playbackErr.Error()
		if diag.Category != "" && diag.Category != "unknown" {
			errLine = diag.Category + ": " + errLine
		}
		if diag.NextStep != "" {
			errLine += " — " + diag.NextStep
		}
		inner = inner + "\n" + styleError.Render(truncate(errLine, max(12, w-2)))
	}

	content := labelLine + "\n" + inner
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) coverPreviewPanel(w, h, coverCols, coverRows int) string {
	label := styleSectionLabel.Render("Preview")
	labelLine := label + "\n" + sectionDivider(w)
	innerW := w - 2

	var coverStr string
	if pl, ok := m.selectedPlaylist(); ok && pl.summary.ImageURL != "" {
		url := pl.summary.ImageURL
		if m.imgs != nil && m.imgs.protocol == imageProtocolKitty {
			if m.imgs.hasImage(url) {
				coverStr = m.blankArt(coverCols, coverRows)
			} else {
				coverStr = m.placeholderArt(coverCols, coverRows)
			}
		} else if s, cached := m.imgs.cover(url, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = m.placeholderArt(coverCols, coverRows)
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	meta := ""
	if pl, ok := m.selectedPlaylist(); ok {
		ownerLine := "playlist by " + truncate(pl.summary.Owner, innerW)
		if pl.summary.Kind == spotify.ContextKindAlbum {
			ownerLine = "album by " + truncate(pl.summary.Owner, innerW)
		}
		meta = "\n" +
			stylePlaylistName.Render(truncate(pl.summary.Name, innerW)) + "\n" +
			stylePlaylistOwner.Render(ownerLine)
	} else {
		meta = "\n" + styleDimmed.Render("select an item")
	}

	content := labelLine + "\n" + m.composeCoverSection(coverStr, meta)
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) playbackScreenView() string {
	layout := m.bodyLayout()
	left := m.albumCoverPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := verticalDivider(layout.bodyH)
	right := m.queuePanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) albumsTabView() string {
	layout := m.bodyLayout()
	left := m.albumPreviewPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	divider := verticalDivider(layout.bodyH)
	right := m.albumBrowserPanel(layout.rightW, layout.bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) albumBrowserPanel(w, h int) string {
	count := len(m.albumList.Items())
	label := styleSectionLabel.Render("Albums")
	countStr := styleDimmed.Render(fmt.Sprintf("%d albums", count))
	labelLine := label + "\n" + countStr + "\n" + sectionDivider(w-1)
	innerH := h - 3

	var inner string
	if m.playlistsErr != nil && len(m.albumList.Items()) == 0 {
		errStr := "failed to load: " + m.playlistsErr.Error()
		inner = styleError.Render(truncate(errStr, w-2)) + "\n" + styleDimmed.Render("r to retry")
	} else if m.playlistsLoading && len(m.albumList.Items()) == 0 {
		inner = styleDimmed.Render("loading albums...")
	} else if m.albumsForbidden && len(m.albumList.Items()) == 0 {
		inner = styleDimmed.Render("saved albums unavailable — re-run 'orpheus auth login' (needs user-library-read)")
	} else {
		m.albumList.SetSize(w-1, innerH)
		inner = m.albumList.View()
	}

	content := labelLine + "\n" + inner
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) albumPreviewPanel(w, h, coverCols, coverRows int) string {
	label := styleSectionLabel.Render("Preview")
	labelLine := label + "\n" + sectionDivider(w)
	innerW := w - 2

	var coverStr string
	if pl, ok := m.selectedAlbum(); ok && pl.summary.ImageURL != "" {
		url := pl.summary.ImageURL
		if m.imgs != nil && m.imgs.protocol == imageProtocolKitty {
			if m.imgs.hasImage(url) {
				coverStr = m.blankArt(coverCols, coverRows)
			} else {
				coverStr = m.placeholderArt(coverCols, coverRows)
			}
		} else if s, cached := m.imgs.cover(url, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = m.placeholderArt(coverCols, coverRows)
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	meta := ""
	if pl, ok := m.selectedAlbum(); ok {
		meta = "\n" +
			stylePlaylistName.Render(truncate(pl.summary.Name, innerW)) + "\n" +
			stylePlaylistOwner.Render("album by "+truncate(pl.summary.Owner, innerW))
	} else {
		meta = "\n" + styleDimmed.Render("select an album")
	}

	content := labelLine + "\n" + m.composeCoverSection(coverStr, meta)
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) albumCoverPanel(w, h, coverCols, coverRows int) string {
	label := styleSectionLabel.Render("Now Playing")
	labelLine := label + "\n" + sectionDivider(w-1)
	innerW := w - 2

	var coverStr string
	if m.status != nil && m.status.AlbumImageURL != "" {
		url := m.status.AlbumImageURL
		if m.imgs != nil && m.imgs.protocol == imageProtocolKitty {
			if m.imgs.hasImage(url) {
				coverStr = m.blankArt(coverCols, coverRows)
			} else {
				coverStr = m.placeholderArt(coverCols, coverRows)
			}
		} else if s, cached := m.imgs.cover(url, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = m.placeholderArt(coverCols, coverRows)
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	var meta string
	if m.status != nil {
		trackName := m.status.TrackName
		artistName := m.status.ArtistName
		albumName := m.status.AlbumName
		if trackName == "" {
			trackName = "Unknown track"
		}
		if artistName == "" {
			artistName = "-"
		}
		meta = "\n" +
			styleTrackName.Render(truncate(trackName, innerW)) + "\n" +
			styleArtistName.Render(truncate(artistName, innerW))
		if albumName != "" {
			meta += "\n" + styleAlbumName.Render(truncate(albumName, innerW))
		}
	} else {
		meta = "\n" + styleDimmed.Render("nothing playing")
	}

	content := labelLine + "\n" + m.composeCoverSection(coverStr, meta)
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) queuePanel(w, h int) string {
	label := styleSectionLabel.Render("Up Next")
	divLine := sectionDivider(w)
	contentLines := h - 4

	idxW := 3
	contentW := max(12, w-idxW-4)
	artistW := min(26, max(8, contentW*2/5))
	titleW := max(4, contentW-artistW)

	colHeader := styleQueueHeader.Render(
		strings.Repeat(" ", 1+idxW+1) + fmt.Sprintf("%-*s  %-*s", titleW, "Title", artistW, "Artist"),
	)
	colDivider := sectionDivider(w)

	lines := []string{label, divLine, colHeader, colDivider}

	displayQueue := m.queue
	hidCurrentFromQueue := false
	if m.status != nil && len(displayQueue) > 0 {
		currentID := golibrespot.NormalizeSpotifyId(m.status.TrackID)
		if currentID != "" && golibrespot.NormalizeSpotifyId(displayQueue[0].ID) == currentID {
			displayQueue = displayQueue[1:]
			hidCurrentFromQueue = true
		}
	}
	if m.status == nil {
		lines = append(lines, styleDimmed.Render("  nothing playing"))
	}

	if len(displayQueue) == 0 {
		lines = append(lines, styleDimmed.Render("  queue is empty"))
	} else {
		maxRows := contentLines - 2
		n := min(len(displayQueue), maxRows)
		for i := 0; i < n; i++ {
			q := displayQueue[i]
			idx := styleQueueIndex.Render(fmt.Sprintf("%*d.", idxW-1, i+1))
			title := styleQueueTrack.Render(truncate(q.Name, titleW))
			artist := styleQueueArtist.Render(truncate(q.Artist, artistW))
			titlePad := titleW - lipgloss.Width(title)
			artistPad := artistW - lipgloss.Width(artist)
			if titlePad < 0 {
				titlePad = 0
			}
			if artistPad < 0 {
				artistPad = 0
			}
			lines = append(lines, " "+idx+" "+title+strings.Repeat(" ", titlePad)+"  "+artist+strings.Repeat(" ", artistPad))
		}

		stableVisibleQueueLen := m.stableQueueLen
		if hidCurrentFromQueue && stableVisibleQueueLen > 0 {
			stableVisibleQueueLen--
		}
		notVisible := max(0, stableVisibleQueueLen-n)
		if notVisible > 0 {
			if m.queueHasMore {
				lines = append(lines, styleDimmed.Render("  + more"))
			} else {
				lines = append(lines, styleDimmed.Render(fmt.Sprintf("  + %d more", notVisible)))
			}
		} else if m.queueHasMore {
			lines = append(lines, styleDimmed.Render("  + more"))
		}
	}

	if m.playbackErr != nil {
		diag := spotify.DiagnoseError(m.playbackErr)
		errLine := m.playbackErr.Error()
		if diag.Category != "" && diag.Category != "unknown" {
			errLine = diag.Category + ": " + errLine
		}
		if diag.NextStep != "" {
			errLine += " — " + diag.NextStep
		}
		lines = append(lines, "", styleError.Render(truncate(errLine, max(12, w-2))))
	}

	content := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) playerBarView() string {
	barW := m.width

	sep := styleDivider.Render(strings.Repeat("─", barW))

	if m.status == nil {
		return sep
	}

	playIcon := m.icon(iconPlay, iconPlayNF)
	pauseIcon := m.icon(iconPause, iconPauseNF)
	stateIcon := styleHeaderPaused.Render(pauseIcon)
	if m.status.Playing {
		stateIcon = styleHeaderPlaying.Render(playIcon)
	}

	pct := 0.0
	if m.status.DurationMS > 0 {
		pct = float64(m.status.ProgressMS) / float64(m.status.DurationMS)
		if pct > 1 {
			pct = 1
		}
	}

	elapsed := stylePlayerTime.Render(fmtDuration(m.status.ProgressMS))
	total := stylePlayerTime.Render("--:--")
	if m.status.DurationMS > 0 {
		total = stylePlayerTime.Render(fmtDuration(m.status.DurationMS))
	}

	elapsedW := lipgloss.Width(elapsed)
	totalW := lipgloss.Width(total)
	iconW := lipgloss.Width(stateIcon)
	progressW := barW - elapsedW - totalW - iconW - 8
	progressStr := m.renderProgressBar(pct, progressW)

	bar := "  " + stateIcon + "  " + elapsed + "  " + progressStr + "  " + total
	return sep + "\n" + bar
}

func (m model) trackPopupView() string {
	modalW := min(m.width-8, 60)
	bodyH := m.height - tabBarH - 1
	innerH := bodyH - 4
	if innerH < 10 {
		innerH = 10
	}

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorBlue).
		Render(fmt.Sprintf("  %s", m.trackPopupName))

	var body string
	var hint string
	if m.trackPopupItems == nil {
		body = lipgloss.NewStyle().
			Foreground(colorMutedBlue).
			Render("\n  Loading...")
		hint = ""
	} else if len(m.trackPopupItems) == 0 {
		body = lipgloss.NewStyle().
			Foreground(colorMutedBlue).
			Render("\n  No tracks found")
		hint = lipgloss.NewStyle().
			Foreground(colorMutedBlue).
			Render("  esc: close")
	} else {
		m.trackPopupList.SetSize(modalW-2, innerH-4)
		body = m.trackPopupList.View()
		hint = lipgloss.NewStyle().
			Foreground(colorMutedBlue).
			Render("  enter: play  /: search  esc: close")
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		body,
		hint,
	)

	box := styleModalBox.
		Width(modalW).
		Height(innerH).
		Render(content)

	return lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}

func (m model) helpModalView() string {
	modalW := min(m.width-8, 80)
	innerH := m.height - 14
	if innerH < 6 {
		innerH = 6
	}
	contentW := max(12, modalW-4)
	title := styleModalTitle.Render("Help")
	hint := styleModalHint.Render("? or esc close")
	header := lipgloss.PlaceHorizontal(contentW, lipgloss.Center, title+"  "+hint)
	sep := styleModalHint.Render(strings.Repeat("─", contentW))

	h := m.help
	h.ShowAll = true
	helpText := centerBlockLines(h.View(m.keys), contentW)
	helpAreaH := max(3, innerH-4)
	helpBody := lipgloss.Place(contentW, helpAreaH, lipgloss.Center, lipgloss.Center, helpText)

	boxContent := header + "\n" + sep + "\n\n" + helpBody
	box := styleModalBox.Width(modalW).Height(innerH).Render(boxContent)
	placed := lipgloss.Place(
		m.width,
		m.height-tabBarH-gapFooterH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("#1a1a2a")),
	)
	return placed
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
	bodyH := m.height - chromeH - 1
	if m.width <= 0 || m.height <= 0 {
		return bodyLayout{bodyH: bodyH, leftW: minLeftW, rightW: m.width - minLeftW, coverStartRow: bodyStartRow1Based + 3, coverStartCol: 1}
	}
	metaLines := 3
	availH := bodyH - 2 - 2 - metaLines
	if availH < 1 {
		availH = 1
	}
	maxRows := availH
	coverRows := maxRows
	coverCols := 2 * coverRows
	leftW := coverCols + 2
	if leftW < minLeftW {
		leftW = minLeftW
	}
	maxLeftW := m.width - minRightW
	if maxLeftW < minLeftW {
		maxLeftW = minLeftW
	}
	if leftW > maxLeftW {
		leftW = maxLeftW
	}
	innerW := leftW - 2
	innerH := bodyH - 2 - metaLines
	if innerH < 1 {
		innerH = 1
	}
	coverCols, coverRows = squareDims(innerW, innerH)
	if coverCols < 2 {
		coverCols = 2
	}
	if coverRows < 1 {
		coverRows = 1
	}
	rightW := m.width - leftW
	if rightW < 0 {
		rightW = 0
	}
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
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	layout := m.bodyLayout()
	if layout.coverCols <= 0 || layout.coverRows <= 0 {
		return nil
	}
	return [][2]int{{layout.coverCols, layout.coverRows}}
}

func (m model) icon(unicode, nerd string) string {
	if m.nerdFonts {
		return nerd
	}
	return unicode
}

func (m model) selectedPlaylist() (playlistItem, bool) {
	sel, ok := m.playlistList.SelectedItem().(playlistItem)
	return sel, ok
}

func (m model) selectedAlbum() (playlistItem, bool) {
	sel, ok := m.albumList.SelectedItem().(playlistItem)
	return sel, ok
}

func (m model) renderProgressBar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	return lipgloss.NewStyle().Foreground(colorBlue).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(colorDivider).Render(strings.Repeat("░", empty))
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

func (m model) kittyOverlay() string {
	if m.imgs == nil || m.imgs.protocol != imageProtocolKitty {
		return ""
	}
	if m.helpOpen || m.trackPopupOpen {
		m.imgs.beginKittyOverlayState("", "")
		return kittyDeleteAll
	}

	layout := m.bodyLayout()
	if layout.coverCols <= 0 || layout.coverRows <= 0 {
		_, shouldDelete, _ := m.imgs.beginKittyOverlayState("", "")
		if shouldDelete {
			return kittyDeleteAll
		}
		return ""
	}

	var url, subjectID string
	switch m.activeTab {
	case tabPlaylists:
		if pl, ok := m.selectedPlaylist(); ok {
			url = pl.summary.ImageURL
			subjectID = strings.TrimSpace(pl.summary.ID)
		}
	case tabAlbums:
		if al, ok := m.selectedAlbum(); ok {
			url = al.summary.ImageURL
			subjectID = strings.TrimSpace(al.summary.ID)
		}
	case tabPlayer:
		if m.status != nil {
			url = m.status.AlbumImageURL
			subjectID = golibrespot.NormalizeSpotifyId(m.status.TrackID)
			if subjectID == "" {
				subjectID = strings.TrimSpace(m.status.TrackName) + "|" + strings.TrimSpace(m.status.ArtistName) + "|" + fmt.Sprintf("%d", m.status.DurationMS)
			}
		}
	}
	if url == "" {
		_, shouldDelete, _ := m.imgs.beginKittyOverlayState("", "")
		if shouldDelete {
			return kittyDeleteAll
		}
		return ""
	}

	encoded := m.imgs.encodedFor(url)
	if encoded == "" {
		displayed := strings.TrimSpace(m.imgs.kittyDisplayedURL())
		target := strings.TrimSpace(url)
		shouldClear := displayed != "" && displayed != target
		if shouldClear {
			_, shouldDelete, _ := m.imgs.beginKittyOverlayState("", "")
			if shouldDelete {
				return kittyDeleteAll
			}
		}
		return ""
	}

	if m.activeTab == tabPlayer && m.status != nil && url != "" {
		displayed := strings.TrimSpace(m.imgs.kittyDisplayedURL())
		target := strings.TrimSpace(url)
		if displayed != "" && displayed != target {
			m.imgs.forceKittyRedraw()
		}
	}
	playerEpoch := uint64(0)
	if m.activeTab == tabPlayer {
		playerEpoch = m.playerCoverEpoch
	}
	key := fmt.Sprintf("%d:%d:%d:%d:%s:%s:%s:%d", layout.coverStartRow, layout.coverStartCol, layout.coverCols, layout.coverRows, m.activeTab, subjectID, url, playerEpoch)
	changed, shouldDelete, placementChanged := m.imgs.beginKittyOverlayState(key, url)
	if !changed {
		return ""
	}
	payload := m.imgs.buildKittyPayload(url, encoded, layout.coverCols, layout.coverRows, m.imgs.nextKittyImageID())
	if payload == "" {
		return kittyDeleteAll
	}
	out := fmt.Sprintf("\x1b7\x1b[%d;%dH%s\x1b8", layout.coverStartRow, layout.coverStartCol, payload)
	if shouldDelete && placementChanged {
		return kittyDeleteAll + out
	}
	return out
}

func (m model) composeCoverSection(coverStr, meta string) string {
	return coverStr + meta
}

func (m model) blankArt(cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	line := strings.Repeat(" ", cols)
	var sb strings.Builder
	sb.WriteString(line)
	for i := 1; i < rows; i++ {
		sb.WriteByte('\n')
		sb.WriteString(line)
	}
	return sb.String()
}

func (m model) placeholderArt(cols, rows int) string {
	if cols <= 2 || rows <= 2 {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(colorDivider)
	top := style.Render("╭" + strings.Repeat("─", cols-2) + "╮")
	mid := style.Render("│" + strings.Repeat(" ", cols-2) + "│")
	bot := style.Render("╰" + strings.Repeat("─", cols-2) + "╯")

	midRows := rows - 2
	var sb strings.Builder
	sb.WriteString(top)
	for i := 0; i < midRows; i++ {
		sb.WriteByte('\n')
		sb.WriteString(mid)
	}
	sb.WriteByte('\n')
	sb.WriteString(bot)
	return sb.String()
}
