package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"orpheus/internal/spotify"
)

// ── Layout constants ──────────────────────────────────────────────────────────

const (
	headerH    = 1 // single header line
	footerH    = 1 // single footer (help) line
	playerBarH = 3 // separator + 2 content rows (track, progress)
	gapH       = 1 // gap between header and panels
	gapFooterH = 1 // gap above footer
)

// chromeH is total vertical space consumed by chrome (header + gaps + footer).
// Panels fill height - chromeH (- playerBarH on the playback screen).
const chromeH = headerH + gapH + gapFooterH + footerH

const (
	iconPlay          = "▶"
	iconPause         = ""
	iconDevice        = "●"
	iconVolume        = "▪"
	iconPlaceholder   = "♪"
	iconShuffle       = "⇄"
	iconPlayNF        = "\uf04b"
	iconPauseNF       = "\uf04c"
	iconDeviceNF      = "\ue30c"
	iconVolumeNF      = "\uf028"
	iconPlaceholderNF = "\uf001"
	iconShuffleNF     = "\uf074"
)

// ── Top-level dispatch ────────────────────────────────────────────────────────

func (m model) View() string {
	if m.width < 40 || m.height < 12 {
		return styleError.Render("terminal too small — please resize")
	}

	header := m.headerView()
	var body string

	if m.modal {
		// modalView renders its own footer (needed for lipgloss.Place sizing),
		// so we must not append the footer again here.
		return header + "\n" + m.modalView()
	} else if m.screen == screenPlaylist {
		body = m.playlistScreenView()
	} else {
		body = m.playbackScreenView()
	}

	footer := m.footerView()

	return header + "\n" + body + "\n" + footer
}

// ── Header ────────────────────────────────────────────────────────────────────

func (m model) headerView() string {
	devIcon := m.icon(iconDevice, iconDeviceNF)
	var right string
	if m.screen == screenPlayback && m.status != nil {
		playIcon := m.icon(iconPlay, iconPlayNF)
		pauseIcon := m.icon(iconPause, iconPauseNF)
		icon := styleHeaderPaused.Render(pauseIcon)
		if m.status.Playing {
			icon = styleHeaderPlaying.Render(playIcon)
		}
		trackName := m.status.TrackName
		artistName := m.status.ArtistName
		if trackName == "" {
			trackName = "Unknown track"
		}
		if artistName == "" {
			artistName = "-"
		}
		track := styleHeaderNowPlaying.Render(
			truncate(trackName, 30) + "  •  " + truncate(artistName, 20),
		)
		device := styleHeaderDevice.Render(devIcon + " " + m.deviceName)
		right = icon + "  " + track + "    " + device
	} else {
		right = styleHeaderDevice.Render(devIcon + " " + m.deviceName)
	}

	gap := max(0, m.width-lipgloss.Width(right)-2)
	return strings.Repeat(" ", gap) + right
}

// ── Footer ────────────────────────────────────────────────────────────────────

func (m model) footerView() string {
	return m.help.View(m.keys)
}

// ── Playlist screen ───────────────────────────────────────────────────────────

func (m model) playlistScreenView() string {
	panelH := m.height - chromeH - 2 // 2 for panel borders
	leftW, rightW := m.splitWidths()

	left := m.playlistBrowserPanel(leftW, panelH)
	right := m.coverPreviewPanel(rightW, panelH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m model) playlistBrowserPanel(w, h int) string {
	inner := m.playlistList.View()
	if m.playlistsErr != nil && len(m.playlistList.Items()) == 0 {
		errStr := "failed to load playlists: " + m.playlistsErr.Error()
		rateHint := ""
		if strings.Contains(m.playlistsErr.Error(), "429") || strings.Contains(strings.ToLower(m.playlistsErr.Error()), "rate limit") {
			rateHint = "\n\n" + styleDimmed.Render("Run 'orpheus auth login' then restart to use your own API quota.")
		}
		inner = styleError.Render(errStr) + rateHint + "\n\n" + styleDimmed.Render("press r to retry")
	} else if m.playlistsLoading && len(m.playlistList.Items()) == 0 {
		inner = styleDimmed.Render("loading playlists...")
	}
	if m.playbackErr != nil {
		diag := spotify.DiagnoseError(m.playbackErr)
		errLine := m.playbackErr.Error()
		if diag.Category != "" && diag.Category != "unknown" {
			errLine = diag.Category + ": " + errLine
		}
		if diag.NextStep != "" {
			errLine += " — " + diag.NextStep
			low := strings.ToLower(diag.NextStep)
			if strings.Contains(low, "retry") || strings.Contains(low, "wait") {
				errLine += " (r to refresh)"
			}
		}
		inner = inner + "\n\n" + styleError.Render(truncate(errLine, max(12, w-6)))
	}
	return activePanelStyle(w-2, h).Render(inner)
}

func (m model) coverPreviewPanel(w, h int) string {
	innerW := w - 4 // border (2) + padding (2)
	innerH := h - 2 // border

	coverCols, coverRows := squareDims(innerW, innerH-4) // leave 4 rows for metadata

	var coverStr string
	if pl, ok := m.selectedPlaylist(); ok && pl.summary.ImageURL != "" {
		if s, cached := m.imgs.cover(pl.summary.ImageURL, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = styleCoverPlaceholder.Render(
				centerText("loading cover...", coverCols),
			)
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

	content := coverStr + meta
	return panelStyle(w-2, h).Padding(1, 1).Render(content)
}

// ── Playback screen ───────────────────────────────────────────────────────────

func (m model) playbackScreenView() string {
	topH := m.height - chromeH - playerBarH - 2 // 2 for panel borders
	leftW, rightW := m.splitWidths()

	left := m.albumCoverPanel(leftW, topH)
	right := m.queuePanel(rightW, topH)
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	bar := m.playerBarView()

	return top + "\n" + bar
}

func (m model) albumCoverPanel(w, h int) string {
	innerW := w - 4 // border (2) + padding (2)
	innerH := h - 2 // border

	metaLines := 3 // track + artist + blank gap
	coverCols, coverRows := squareDims(innerW, innerH-metaLines)

	var coverStr string
	if m.status != nil && m.status.AlbumImageURL != "" {
		if s, cached := m.imgs.cover(m.status.AlbumImageURL, coverCols, coverRows); cached {
			coverStr = s
		} else {
			coverStr = styleCoverPlaceholder.Render(
				centerText("loading...", coverCols),
			)
		}
	} else {
		coverStr = m.placeholderArt(coverCols, coverRows)
	}

	var meta string
	if m.status != nil {
		trackName := m.status.TrackName
		artistName := m.status.ArtistName
		if trackName == "" {
			trackName = "Unknown track"
		}
		if artistName == "" {
			artistName = "-"
		}
		meta = "\n" +
			styleTrackName.Render(truncate(trackName, innerW)) + "\n" +
			styleArtistName.Render(truncate(artistName, innerW))
	} else {
		meta = "\n" + styleDimmed.Render("nothing playing")
	}

	content := coverStr + meta
	return panelStyle(w-2, h).Padding(1, 1).Render(content)
}

func (m model) queuePanel(w, h int) string {
	innerW := w - 4
	contentLines := h - 4
	maxVisible := max(1, contentLines-3)

	lines := []string{styleSectionTitle.Render("Up Next")}
	lines = append(lines, styleQueueIndex.Render(strings.Repeat("─", min(innerW, 30))))

	displayQueue := m.queue
	if m.status != nil && len(m.queue) > 0 && normalizeQueueID(m.queue[0].ID) == normalizeQueueID(m.status.TrackID) {
		displayQueue = m.queue[1:]
	}

	if len(displayQueue) == 0 {
		lines = append(lines, styleDimmed.Render("queue is empty"))
	} else {
		n := min(len(displayQueue), maxVisible)
		for i := 0; i < n; i++ {
			q := displayQueue[i]
			idx := styleQueueIndex.Render(fmt.Sprintf("%2d.", i+1))
			track := styleQueueTrack.Render(truncate(q.Name, max(8, innerW-20)))
			artist := styleQueueArtist.Render(truncate(q.Artist, 14))
			trackW := lipgloss.Width(track)
			artistW := lipgloss.Width(artist)
			gap := max(1, innerW-5-trackW-artistW)
			lines = append(lines, idx+" "+track+strings.Repeat(" ", gap)+artist)
		}
		notVisible := max(0, m.stableQueueLen-n)
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
			low := strings.ToLower(diag.NextStep)
			if strings.Contains(low, "retry") || strings.Contains(low, "wait") {
				errLine += " (r to refresh)"
			}
		}
		lines = append(lines, "", styleError.Render(truncate(errLine, max(12, innerW))))
	}

	if len(lines) > contentLines {
		lines = lines[:contentLines]
		if len(displayQueue) > 0 {
			visible := contentLines - 3 // header + separator = 2, last line = tail indicator
			notVisible := max(0, m.stableQueueLen-visible)
			if len(lines) > 0 {
				if notVisible > 0 {
					if m.queueHasMore {
						lines[len(lines)-1] = styleDimmed.Render("  + more")
					} else {
						lines[len(lines)-1] = styleDimmed.Render(fmt.Sprintf("  + %d more", notVisible))
					}
				} else if m.queueHasMore {
					lines[len(lines)-1] = styleDimmed.Render("  + more")
				}
			}
		}
	}

	content := strings.Join(lines, "\n")
	return panelStyle(w-2, h).Padding(1, 1).Render(content)
}

// ── Player bar ────────────────────────────────────────────────────────────────

func (m model) playerBarView() string {
	barW := m.width

	// Separator line
	sep := stylePlayerSeparator.Render(strings.Repeat("─", barW))

	if m.status == nil {
		empty := styleDimmed.Render("  no active playback")
		return sep + "\n" + empty + "\n"
	}

	// Row 1: track info (left) + volume (right)
	playIcon := m.icon(iconPlay, iconPlayNF)
	pauseIcon := m.icon(iconPause, iconPauseNF)
	stateIcon := styleHeaderPaused.Render(pauseIcon)
	if m.status.Playing {
		stateIcon = styleHeaderPlaying.Render(playIcon)
	}
	trackName := m.status.TrackName
	artistName := m.status.ArtistName
	if trackName == "" {
		trackName = "Unknown track"
	}
	if artistName == "" {
		artistName = "-"
	}
	trackInfo := stateIcon + "  " +
		stylePlayerTrack.Render(truncate(trackName, 35)) + "  " +
		stylePlayerArtist.Render(truncate(artistName, 25))

	volIcon := m.icon(iconVolume, iconVolumeNF)
	volBar := m.volumeBar(m.status.Volume)
	volText := stylePlayerVolume.Render(fmt.Sprintf("%3d%%", m.status.Volume))
	volDisplay := stylePlayerVolIcon.Render(volIcon) + " " + volBar + " " + volText

	infoW := lipgloss.Width(trackInfo)
	volW := lipgloss.Width(volDisplay)
	infoGap := max(1, barW-infoW-volW-2)
	row1 := "  " + trackInfo + strings.Repeat(" ", infoGap) + volDisplay

	// Row 2: progress bar
	pct := 0.0
	if m.status.DurationMS > 0 {
		pct = float64(m.status.ProgressMS) / float64(m.status.DurationMS)
		if pct > 1 {
			pct = 1
		}
	}
	elapsed := fmtDuration(m.status.ProgressMS)
	totalStr := "--:--"
	if m.status.DurationMS > 0 {
		totalStr = fmtDuration(m.status.DurationMS)
	}
	timeLeft := stylePlayerTime.Render(elapsed)
	timeRight := stylePlayerTime.Render(totalStr)
	shuffleIndicator := ""
	if m.status.ShuffleState {
		shuffleIndicator = "  " + styleDimmed.Render(m.icon(iconShuffle, iconShuffleNF))
	}
	progressInnerW := barW - lipgloss.Width(timeLeft) - lipgloss.Width(timeRight) - lipgloss.Width(shuffleIndicator) - 6
	progressStr := m.renderProgressBar(pct, progressInnerW)
	row2 := "  " + timeLeft + "  " + progressStr + "  " + timeRight + shuffleIndicator

	return strings.Join([]string{sep, row1, row2}, "\n")
}

// ── Modal overlay ─────────────────────────────────────────────────────────────

func (m model) modalView() string {
	// Modal box: 60% of terminal width, up to 50 cols, most of the height
	modalW := min(m.width-8, 64)
	listH := m.height - 12 // leave room for modal chrome and backdrop context

	m.modalList.SetSize(modalW-4, listH)

	title := styleModalTitle.Render("Switch Playlist")
	hint := styleModalHint.Render("enter play  •  esc close  •  / search")
	sep := styleModalHint.Render(strings.Repeat("─", modalW-2))

	boxContent := title + "  " + hint + "\n" + sep + "\n" + m.modalList.View()

	box := styleModalBox.
		Width(modalW).
		Render(boxContent)

	// Place the box centred over the screen; fill backdrop with ░ characters.
	// Place the modal box centred over the screen (excluding the footer rows).
	// The footer is appended by the caller (View) so we only produce the backdrop here.
	placed := lipgloss.Place(
		m.width,
		m.height-footerH-gapFooterH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("#2a2a2a")),
	)
	return placed + "\n" + m.footerView()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// splitWidths divides the terminal width into left and right panel widths,
// accounting for borders. The left panel gets just under half.
func (m model) splitWidths() (leftW, rightW int) {
	leftW = m.width/2 + 1
	rightW = m.width - leftW
	return
}

func (m model) icon(unicode, nerd string) string {
	if m.nerdFonts {
		return nerd
	}
	return unicode
}

// selectedPlaylist returns the playlist currently highlighted in the browser list.
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
	bar := lipgloss.NewStyle().Foreground(colorBlue).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(colorMutedBlue).Render(strings.Repeat("░", empty))
	return bar
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

// fmtDuration formats milliseconds as m:ss.
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

// truncate returns s truncated to max runes, appending "…" if truncated.
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

// centerText centers s within a field of width w using spaces.
func centerText(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return s
	}
	pad := (w - sw) / 2
	return strings.Repeat(" ", pad) + s + strings.Repeat(" ", w-sw-pad)
}

func (m model) placeholderArt(cols, rows int) string {
	if cols <= 2 || rows <= 2 {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(colorMutedBlue)
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
