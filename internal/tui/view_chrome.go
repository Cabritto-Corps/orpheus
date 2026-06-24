package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	golibrespot "github.com/elxgy/go-librespot"
)

func (m model) headerView() string {
	w := m.ui.width

	var statusStr, centerL1, rightL1 string

	if m.transport.status != nil {
		playIcon := m.icon(iconPlay, iconPlayNF)
		pauseIcon := m.icon(iconPause, iconPauseNF)
		if m.transport.status.Playing {
			statusStr = styleHeaderPlaying.Render("[" + playIcon + " Playing]")
		} else {
			statusStr = styleHeaderPaused.Render("[" + pauseIcon + " Paused]")
		}

		volBar := m.headerVolumeBar(m.transport.status.Volume)
		volText := styleHeaderVolume.Render(fmt.Sprintf("%3d%%", m.transport.status.Volume))
		rightL1 = volBar + " " + volText
		if m.transport.status.ShuffleState {
			rightL1 += "  " + styleDimmed.Render(m.icon(iconShuffle, iconShuffleNF))
		}
		if m.transport.status.RepeatTrack {
			rightL1 += "  " + styleDimmed.Render(m.icon(iconRepeatTrack, iconRepeatTrackNF))
		} else if m.transport.status.RepeatContext {
			rightL1 += "  " + styleDimmed.Render(m.icon(iconRepeatContext, iconRepeatContextNF))
		}

		availCenterW := max(10, w-lipgloss.Width(statusStr)-lipgloss.Width(rightL1)-2)
		trackName := m.transport.status.TrackName
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
	if m.transport.status != nil {
		artist := m.transport.status.ArtistName
		album := m.transport.status.AlbumName
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

	sep := sectionDivider(w)
	return line1 + "\n" + line2 + "\n" + sep
}

func layoutThreeZone(w int, left, center, right string) string {
	leftW := lipgloss.Width(left)
	centerW := lipgloss.Width(center)
	rightW := lipgloss.Width(right)

	centerPad := max((w-centerW)/2, leftW+1)
	leftPad := max(centerPad-leftW, 0)
	afterCenter := centerPad + centerW
	rightPad := max(w-afterCenter-rightW, 1)

	return left +
		strings.Repeat(" ", leftPad) +
		center +
		strings.Repeat(" ", rightPad) +
		right
}

func (m model) headerVolumeBar(vol int) string {
	const w = 6
	volChar := m.icon(iconVolume, iconVolumeNF)
	filled := min(int(float64(vol)/100.0*float64(w)), w)
	return styleVolumeBarFilled.Render(strings.Repeat(volChar, filled)) +
		styleVolumeBarEmpty.Render(strings.Repeat(volChar, w-filled))
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
		if m.ui.activeTab == entry.t {
			parts = append(parts, styleTabActive.Render(" "+entry.label+" "))
		} else {
			parts = append(parts, styleTabInactive.Render(" "+entry.label+" "))
		}
	}
	sep := styleDivider.Render("│")
	bar := strings.Join(parts, sep)
	underline := sectionDivider(m.ui.width)
	return bar + "\n" + underline
}

func (m model) playerBarView() string {
	barW := m.ui.width

	sep := sectionDivider(barW)

	if m.transport.status == nil {
		return sep
	}

	playIcon := m.icon(iconPlay, iconPlayNF)
	pauseIcon := m.icon(iconPause, iconPauseNF)
	stateIcon := styleHeaderPaused.Render(pauseIcon)
	if m.transport.status.Playing {
		stateIcon = styleHeaderPlaying.Render(playIcon)
	}

	elapsedMs := m.transport.status.ProgressMS
	if m.transport.status.DurationMS > 0 && elapsedMs > m.transport.status.DurationMS {
		elapsedMs = m.transport.status.DurationMS
	}

	pct := 0.0
	if m.transport.status.DurationMS > 0 {
		pct = float64(elapsedMs) / float64(m.transport.status.DurationMS)
		if pct > 1 {
			pct = 1
		}
	}

	elapsed := stylePlayerTime.Render(fmtDuration(elapsedMs))
	total := stylePlayerTime.Render("--:--")
	if m.transport.status.DurationMS > 0 {
		total = stylePlayerTime.Render(fmtDuration(m.transport.status.DurationMS))
	}

	elapsedW := lipgloss.Width(elapsed)
	totalW := lipgloss.Width(total)
	iconW := lipgloss.Width(stateIcon)
	progressW := barW - elapsedW - totalW - iconW - 8
	var progressStr string
	if m.transport.status.DurationMS <= 0 {
		progressStr = styleProgressBarEmpty.Render(strings.Repeat("░", progressW))
	} else {
		progressStr = m.renderProgressBar(pct, progressW)
	}

	bar := "  " + stateIcon + "  " + elapsed + "  " + progressStr + "  " + total
	return sep + "\n" + bar
}

func (m model) trackPopupView() string {
	modalW := min(m.ui.width-8, 60)
	bodyH := m.ui.height - headerH - tabBarH - 2
	innerH := max(bodyH-4, 10)

	title := styleTrackPopupTitle.Render(fmt.Sprintf("  %s", m.ui.trackPopupName))

	var body string
	var hint string
	if m.ui.trackPopupItems == nil {
		body = styleTrackPopupLoading.Render("\n  Loading...")
		hint = ""
	} else if len(m.ui.trackPopupItems) == 0 {
		body = styleTrackPopupLoading.Render("\n  No tracks found")
		hint = styleTrackPopupHint.Render("  esc: close")
	} else {
		body = m.ui.trackPopupList.View()
		hint = styleTrackPopupHint.Render("  enter: play  /: search  esc: close")
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

	return lipgloss.Place(m.ui.width, bodyH, lipgloss.Center, lipgloss.Center, box)
}

func (m model) helpModalView() string {
	modalW := min(m.ui.width-8, 80)
	innerH := max(m.ui.height-14, 6)
	contentW := max(12, modalW-4)
	title := styleModalTitle.Render("Help")
	hint := styleModalHint.Render("? or esc close")
	header := lipgloss.PlaceHorizontal(contentW, lipgloss.Center, title+"  "+hint)
	sep := styleModalHint.Render(strings.Repeat("─", contentW))

	h := m.ui.help
	h.ShowAll = true
	helpText := centerBlockLines(h.View(m.ui.keys), contentW)
	helpAreaH := max(3, innerH-4)
	helpBody := lipgloss.Place(contentW, helpAreaH, lipgloss.Center, lipgloss.Center, helpText)

	boxContent := header + "\n" + sep + "\n\n" + helpBody
	box := styleModalBox.Width(modalW).Height(innerH).Render(boxContent)
	placed := lipgloss.Place(
		m.ui.width,
		m.ui.height-headerH-tabBarH-gapFooterH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("#1a1a2a")),
	)
	return placed
}

func (m model) kittyOverlay() string {
	if m.ui.imgs == nil || m.ui.imgs.protocol != imageProtocolKitty {
		return ""
	}
	if m.ui.helpOpen || m.ui.trackPopupOpen {
		m.ui.imgs.beginKittyOverlayState("", "")
		return kittyDeleteAll
	}

	layout := m.getBodyLayout()
	if layout.coverCols <= 0 || layout.coverRows <= 0 {
		_, shouldDelete, _ := m.ui.imgs.beginKittyOverlayState("", "")
		if shouldDelete {
			return kittyDeleteAll
		}
		return ""
	}

	var url, subjectID string
	switch m.ui.activeTab {
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
		if m.transport.status != nil {
			url = m.transport.status.AlbumImageURL
			subjectID = golibrespot.NormalizeSpotifyId(m.transport.status.TrackID)
			if subjectID == "" {
				subjectID = strings.TrimSpace(m.transport.status.TrackName) + "|" + strings.TrimSpace(m.transport.status.ArtistName) + "|" + fmt.Sprintf("%d", m.transport.status.DurationMS)
			}
		}
	}
	if url == "" {
		_, shouldDelete, _ := m.ui.imgs.beginKittyOverlayState("", "")
		if shouldDelete {
			return kittyDeleteAll
		}
		return ""
	}

	encoded := m.ui.imgs.encodedFor(url)
	if encoded == "" {
		displayed := strings.TrimSpace(m.ui.imgs.kittyDisplayedURL())
		target := strings.TrimSpace(url)
		shouldClear := displayed != "" && displayed != target
		if shouldClear {
			_, shouldDelete, _ := m.ui.imgs.beginKittyOverlayState("", "")
			if shouldDelete {
				return kittyDeleteAll
			}
		}
		return ""
	}

	if m.ui.activeTab == tabPlayer && m.transport.status != nil && url != "" {
		displayed := strings.TrimSpace(m.ui.imgs.kittyDisplayedURL())
		target := strings.TrimSpace(url)
		if displayed != "" && displayed != target {
			m.ui.imgs.forceKittyRedraw()
		}
	}
	playerEpoch := uint64(0)
	if m.ui.activeTab == tabPlayer {
		playerEpoch = m.transport.playerCoverEpoch
	}
	key := fmt.Sprintf("%d:%d:%d:%d:%s:%s:%s:%d", layout.coverStartRow, layout.coverStartCol, layout.coverCols, layout.coverRows, m.ui.activeTab, subjectID, url, playerEpoch)
	changed, shouldDelete, placementChanged := m.ui.imgs.beginKittyOverlayState(key, url)
	if !changed {
		return ""
	}
	payload := m.ui.imgs.buildKittyPayload(url, encoded, layout.coverCols, layout.coverRows, m.ui.imgs.nextKittyImageID())
	if payload == "" {
		return kittyDeleteAll
	}
	out := fmt.Sprintf("\x1b7\x1b[%d;%dH%s\x1b8", layout.coverStartRow, layout.coverStartCol, payload)
	if shouldDelete && placementChanged {
		return kittyDeleteAll + out
	}
	return out
}
