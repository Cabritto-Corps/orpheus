package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"orpheus/internal/spotify"
)

const (
	headerH    = 2
	footerH    = 1
	playerBarH = 1
	gapH       = 0
	gapFooterH = 0
)

const chromeH = headerH + footerH + playerBarH + 1

const (
	iconPlay            = "▶"
	iconPause           = ""
	iconDevice          = "●"
	iconVolume          = "▪"
	iconPlaceholder     = "♪"
	iconShuffle         = "⇄"
	iconRepeatContext   = "󰑖"
	iconRepeatTrack     = "󰑘"
	iconPlayNF          = "\uf04b"
	iconPauseNF         = "\uf04c"
	iconDeviceNF        = "\ue30c"
	iconVolumeNF        = "\uf028"
	iconPlaceholderNF   = "\uf001"
	iconShuffleNF       = "\uf074"
	iconRepeatContextNF = "󰑖"
	iconRepeatTrackNF   = "󰑘"
)

func (m model) View() string {
	if m.width < 40 || m.height < 12 {
		return styleError.Render("terminal too small — please resize")
	}

	header := m.headerView()

	if m.modal {
		return header + "\n" + m.modalView()
	}

	var body string
	if m.screen == screenPlaylist {
		body = m.playlistScreenView()
	} else {
		body = m.playbackScreenView()
	}

	bar := m.playerBarView()
	footer := m.footerView()

	return header + "\n" + body + "\n" + bar + "\n" + footer
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
		elapsed := fmtDuration(m.status.ProgressMS)
		total := "--:--"
		if m.status.DurationMS > 0 {
			total = fmtDuration(m.status.DurationMS)
		}
		statusStr += "  " + styleHeaderStatus.Render(elapsed+" / "+total)

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
		centerL1 = styleHeaderCenter.Render(truncate(trackName, min(40, availCenterW)))
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
			parts += "  •  " + truncate(album, 30)
		}
		centerL2 = styleHeaderSub.Render(truncate(parts, 55))
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

func (m model) footerView() string {
	return m.help.View(m.keys)
}

func (m model) playlistScreenView() string {
	bodyH := m.height - chromeH - 1
	leftW, rightW := m.splitWidths()

	left := m.coverPreviewPanel(leftW-1, bodyH)
	divider := verticalDivider(bodyH)
	right := m.playlistBrowserPanel(rightW, bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) playlistBrowserPanel(w, h int) string {
	label := styleSectionLabel.Render("Playlists")
	labelLine := label + "\n" + sectionDivider(w-1)
	innerH := h - 2

	var inner string
	if m.playlistsErr != nil && len(m.playlistList.Items()) == 0 {
		errStr := "failed to load: " + m.playlistsErr.Error()
		rateHint := ""
		if strings.Contains(m.playlistsErr.Error(), "429") || strings.Contains(strings.ToLower(m.playlistsErr.Error()), "rate limit") {
			rateHint = "\n" + styleDimmed.Render("Run 'orpheus auth login' to use your own API quota.")
		}
		inner = styleError.Render(truncate(errStr, w-2)) + rateHint + "\n" + styleDimmed.Render("r to retry")
	} else if m.playlistsLoading && len(m.playlistList.Items()) == 0 {
		inner = styleDimmed.Render("loading playlists...")
	} else {
		m.playlistList.SetSize(w-1, innerH)
		inner = m.playlistList.View()
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

func (m model) coverPreviewPanel(w, h int) string {
	label := styleSectionLabel.Render("Preview")
	labelLine := label + "\n" + sectionDivider(w)
	innerH := h - 2
	innerW := w - 2

	coverCols, coverRows := squareDims(innerW, innerH-3)
	var coverStr string
	if pl, ok := m.selectedPlaylist(); ok && pl.summary.ImageURL != "" {
		if s, cached := m.imgs.cover(pl.summary.ImageURL, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = styleCoverPlaceholder.Render(centerText("loading...", coverCols))
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	meta := ""
	if pl, ok := m.selectedPlaylist(); ok {
		meta = "\n" +
			stylePlaylistName.Render(truncate(pl.summary.Name, innerW)) + "\n" +
			stylePlaylistOwner.Render("by "+truncate(pl.summary.Owner, innerW))
	} else {
		meta = "\n" + styleDimmed.Render("select a playlist")
	}

	content := labelLine + "\n " + strings.ReplaceAll(coverStr+meta, "\n", "\n ")
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) playbackScreenView() string {
	bodyH := m.height - chromeH - 1
	leftW, rightW := m.splitWidths()

	left := m.albumCoverPanel(leftW, bodyH)
	divider := verticalDivider(bodyH)
	right := m.queuePanel(rightW-1, bodyH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, divider, right)
}

func (m model) albumCoverPanel(w, h int) string {
	label := styleSectionLabel.Render("Now Playing")
	labelLine := label + "\n" + sectionDivider(w-1)
	innerH := h - 2
	innerW := w - 2

	metaLines := 7
	coverCols, coverRows := squareDims(innerW, innerH-metaLines)

	var coverStr string
	if m.status != nil && m.status.AlbumImageURL != "" {
		if s, cached := m.imgs.cover(m.status.AlbumImageURL, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = styleCoverPlaceholder.Render(centerText("loading...", coverCols))
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

	content := labelLine + "\n " + strings.ReplaceAll(coverStr+meta, "\n", "\n ")
	return lipgloss.NewStyle().Width(w).Height(h).Render(content)
}

func (m model) queuePanel(w, h int) string {
	label := styleSectionLabel.Render("Up Next")
	divLine := sectionDivider(w)
	contentLines := h - 4

	idxW := 3
	artistW := min(22, w/3)
	titleW := max(8, w-idxW-artistW-3)

	colHeader := styleQueueHeader.Render(
		strings.Repeat(" ", 1+idxW+1) + fmt.Sprintf("%-*s  %-*s", titleW, "Title", artistW, "Artist"),
	)
	colDivider := sectionDivider(w)

	lines := []string{label, divLine, colHeader, colDivider}

	displayQueue := m.queue
	var currentEntry *spotify.QueueItem

	if m.status != nil && len(m.queue) > 0 {
		if normalizeQueueID(m.queue[0].ID) == normalizeQueueID(m.status.TrackID) {
			entry := m.queue[0]
			currentEntry = &entry
			displayQueue = m.queue[1:]
		}
	}

	if currentEntry != nil {
		playIcon := m.icon(iconPlay, iconPlayNF)
		idx := styleQueueCurrentTrack.Render(fmt.Sprintf("%-*s", idxW, playIcon))
		title := styleQueueCurrentTrack.Render(truncate(currentEntry.Name, titleW))
		artist := styleQueueCurrentArtist.Render(truncate(currentEntry.Artist, artistW))
		titlePad := titleW - lipgloss.Width(title)
		artistPad := artistW - lipgloss.Width(artist)
		if titlePad < 0 {
			titlePad = 0
		}
		if artistPad < 0 {
			artistPad = 0
		}
		lines = append(lines, " "+idx+" "+title+strings.Repeat(" ", titlePad)+"  "+artist+strings.Repeat(" ", artistPad))
	} else if m.status == nil {
		lines = append(lines, styleDimmed.Render("  nothing playing"))
	}

	if len(displayQueue) == 0 && currentEntry == nil {
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

		notVisible := max(0, m.stableQueueLen-n)
		if currentEntry != nil {
			notVisible = max(0, notVisible-1)
		}
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

func (m model) modalView() string {
	if m.modalKind == modalKindHelp {
		return m.helpModalView()
	}
	modalW := min(m.width-8, 64)
	listH := m.height - 12

	m.modalList.SetSize(modalW-4, listH)

	title := styleModalTitle.Render("Switch Playlist")
	hint := styleModalHint.Render("enter play  •  esc close  •  / search")
	sep := styleModalHint.Render(strings.Repeat("─", modalW-2))

	boxContent := title + "  " + hint + "\n" + sep + "\n" + m.modalList.View()

	box := styleModalBox.
		Width(modalW).
		Render(boxContent)

	placed := lipgloss.Place(
		m.width,
		m.height-footerH-gapFooterH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("#1a1a2a")),
	)
	return placed + "\n" + m.footerView()
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
		m.height-footerH-gapFooterH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("#1a1a2a")),
	)
	return placed + "\n" + m.footerView()
}

func (m model) splitWidths() (leftW, rightW int) {
	leftW = m.width/3 + 2
	rightW = m.width - leftW
	return
}

func (m model) currentCoverSizes() [][2]int {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	bodyH := m.height - chromeH - 1
	leftW, _ := m.splitWidths()

	innerW := leftW - 2
	innerH := bodyH - 2
	metaLines := 7
	if innerW > 0 && innerH > metaLines {
		cols, rows := squareDims(innerW, innerH-metaLines)
		if cols > 0 && rows > 0 {
			return [][2]int{{cols, rows}}
		}
	}
	return nil
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

func (m model) volumeBar(vol int) string {
	const w = 8
	volChar := m.icon(iconVolume, iconVolumeNF)
	filled := int(float64(vol) / 100.0 * float64(w))
	if filled > w {
		filled = w
	}
	return lipgloss.NewStyle().Foreground(colorBlue).Render(strings.Repeat(volChar, filled)) +
		lipgloss.NewStyle().Foreground(colorMutedBlue).Render(strings.Repeat(volChar, w-filled))
}

func fmtDuration(ms int) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func queueDuration(ms int) string {
	if ms <= 0 {
		return "--:--"
	}
	return fmtDuration(ms)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
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

func (m model) placeholderArt(cols, rows int) string {
	if cols <= 2 || rows <= 2 {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(colorDivider)
	note := m.icon(iconPlaceholder, iconPlaceholderNF)
	top := style.Render("╭" + strings.Repeat("─", cols-2) + "╮")
	mid := style.Render("│" + strings.Repeat(" ", cols-2) + "│")
	inner := strings.TrimSpace(centerText(note, cols))
	if inner == "" {
		inner = " "
	}
	noteRow := style.Render("│" + inner + "│")
	bot := style.Render("╰" + strings.Repeat("─", cols-2) + "╯")

	midRows := rows - 2
	noteAt := midRows / 2
	var sb strings.Builder
	sb.WriteString(top)
	for i := 0; i < midRows; i++ {
		sb.WriteByte('\n')
		if i == noteAt {
			sb.WriteString(noteRow)
		} else {
			sb.WriteString(mid)
		}
	}
	sb.WriteByte('\n')
	sb.WriteString(bot)
	return sb.String()
}
