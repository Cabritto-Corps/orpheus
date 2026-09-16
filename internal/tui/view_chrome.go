package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	golibrespot "github.com/elxgy/go-librespot"
)

func (m model) headerView() string {
	w := m.ui.width

	var statusStr, centerL1, rightL1 string

	if m.transport.status != nil {
		playIcon, pauseIcon := m.playPauseGlyphs()
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
		if m.transport.transition.Pending() {
			// The pushed status still describes the outgoing track while a
			// transition is pending; mark it so it never reads as "paused".
			trackName = truncate(trackName, max(availCenterW-1, 1)) + "…"
		}
		centerL1 = styleHeaderCenter.Render(truncate(trackName, availCenterW))
	} else {
		statusStr = styleHeaderPaused.Render("Orpheus")
		centerL1 = styleHeaderSub.Render("no active playback")
		device := m.icon(iconDevice, iconDeviceNF) + " " + m.deviceName
		// Line 1 also carries the volume/status side only in the playing
		// state; here the device string must fit the full width or it wraps
		// and corrupts the chrome height.
		rightL1 = styleHeaderStatus.Render(truncate(device, max(1, w-lipgloss.Width(statusStr)-2)))
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
	rightW := lipgloss.Width(right)

	// The centre zone owns exactly the space left of the fixed-size side
	// zones; it is truncated BEFORE joining so the row can never exceed the
	// terminal (a style would wrap instead of clip).
	centerBudget := max(0, w-leftW-rightW-2)
	center = fitCell(center, centerBudget)
	centerW := lipgloss.Width(center)

	gap := max(0, w-leftW-centerW-rightW)
	leftGap := min(max(gap/2, 1), gap)
	rightGap := gap - leftGap

	return left +
		strings.Repeat(" ", leftGap) +
		center +
		strings.Repeat(" ", rightGap) +
		right
}

func (m model) headerVolumeBar(vol int) string {
	return gradientBar(float64(vol)/100.0, volumeBarW)
}

func (m model) tabBarView() string {
	key := tabBarCacheKey{m.ui.width, m.ui.activeTab, themeEpoch}
	if cached, ok := tabBarCache.get(key); ok {
		return cached
	}
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
	sep := styleDivider.Render("\u2502")
	bar := strings.Join(parts, sep)
	underline := sectionDivider(m.ui.width)
	out := bar + "\n" + underline
	tabBarCache.put(key, out)
	return out
}
func (m model) playerBarView() string {
	barW := m.ui.width

	sep := sectionDivider(barW)

	if m.transport.status == nil {
		// Always render the full bar height: the idle placeholder must
		// occupy the same number of lines as the playing bar or the bottom
		// gutter shifts between idle and playing frames.
		return sep + "\n"
	}
	playIcon, pauseIcon := m.playPauseGlyphs()
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
	progressW := barW - elapsedW - totalW - iconW - playerBarGaps*playerBarGap
	var progressStr string
	if m.transport.status.DurationMS <= 0 {
		_, empty := themeBarRunes()
		progressStr = styleProgressBarEmpty.Render(strings.Repeat(string(empty), progressW))
	} else {
		progressStr = gradientBar(pct, progressW)
	}

	bar := "  " + stateIcon + "  " + elapsed + "  " + progressStr + "  " + total
	return sep + "\n" + bar
}

func (m model) trackPopupView() string {
	modalW, _, listH := popupModalSize(m.ui.width, m.ui.height)
	innerH := listH + 2

	title := styleTrackPopupTitle.Render("  " + m.ui.trackPopupName)

	var body string
	if m.ui.trackPopupItems == nil {
		body = styleTrackPopupLoading.Render("\n  " + m.ui.spinner.View() + " Loading...")
	} else if len(m.ui.trackPopupItems) == 0 {
		body = styleTrackPopupLoading.Render("\n  No tracks found")
	} else {
		body = m.ui.trackPopupList.View()
	}
	var hint string
	if m.ui.trackPopupItems != nil {
		hint = styleTrackPopupHint.Render(hintLine([]key.Binding{m.ui.keys.Select, m.ui.keys.Filter, m.ui.keys.CloseModal}, modalW-modalContentInset))
	}

	return modalFrame(m.ui.width, m.ui.height, title, hint, body, modalW, innerH)
}

// helpModalSize is the single source for the help modal's dimensions so the
// Update-side viewport rebuild and the View-side render can never drift.
func helpModalSize(termW, termH int) (modalW, innerH, contentW int) {
	innerH = max(6, termH-headerH-2)
	modalW, boxH := modalGeometry(termW, termH, termW-4, innerH)
	contentW = max(12, modalW-4)
	innerH = boxH
	return modalW, innerH, contentW
}

// ensureHelpViewport builds (or rebuilds) the help modal's viewport when the
// grouped help body overflows the modal. It runs from Update paths (open,
// resize) because View cannot persist state.
func (m *model) ensureHelpViewport() {
	_, innerH, contentW := helpModalSize(m.ui.width, m.ui.height)
	body := m.helpGroupedBody(contentW, innerH-4)
	if lipgloss.Height(body) > innerH-2 {
		v := viewport.New(contentW, innerH-2)
		v.SetContent(body)
		m.ui.helpViewport = &v
	} else {
		m.ui.helpViewport = nil
	}
}

// scrollHelp scrolls the help modal's viewport when the content overflows;
// a no-op otherwise.
func (m model) scrollHelp(dy int) model {
	if m.ui.helpViewport == nil {
		return m
	}
	vp := *m.ui.helpViewport
	if dy < 0 {
		vp.ScrollUp(-dy)
	} else {
		vp.ScrollDown(dy)
	}
	m.ui.helpViewport = &vp
	return m
}

func (m model) helpModalView() string {
	modalW, innerH, contentW := helpModalSize(m.ui.width, m.ui.height)

	hint := "↑/↓ scroll   " + m.ui.keys.ToggleHelp.Help().Key + " or " + m.ui.keys.CloseModal.Help().Key + " close"
	body := m.helpGroupedBody(contentW, innerH-4)
	if vp := m.ui.helpViewport; vp != nil {
		body = vp.View()
		if vp.AtTop() {
			hint = "↑/↓ scroll   " + m.ui.keys.CloseModal.Help().Key + " close"
		}
	}

	return modalFrame(m.ui.width, m.ui.height, styleModalTitle.Render("Help"),
		styleModalHint.Render(hint), body, modalW, innerH)
}

func (m model) overlayBlocked() bool {
	return m.ui.helpOpen || m.ui.settings.open || m.ui.trackPopupOpen
}

func (m model) kittyOverlay() string {
	if m.ui.imgs == nil || m.ui.imgs.protocolForRender() != imageProtocolKitty {
		return ""
	}
	// Kitty graphics sit on a terminal layer above text and persist until
	// deleted, so any popup would render beneath them. Hide the overlay for
	// the whole time a modal is open and retransmit on the first unblocked
	// frame via forceKittyRedraw.
	if m.overlayBlocked() {
		m.ui.imgs.forceKittyRedraw()
		return kittyDeleteAll
	}
	layout := m.bodyLayout()
	if layout.coverCols <= 0 || layout.coverRows <= 0 {
		_, shouldDelete, _, _ := m.ui.imgs.beginKittyOverlayState("", "")
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
		_, shouldDelete, _, _ := m.ui.imgs.beginKittyOverlayState("", "")
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
			_, shouldDelete, _, _ := m.ui.imgs.beginKittyOverlayState("", "")
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
	// With a cover frame the art insets inside the frame's inner ring: the
	// panel text draws the border, the image lands one cell in.
	artCols, artRows := layout.coverCols, layout.coverRows
	startRow, startCol := layout.coverStartRow, layout.coverStartCol
	if coverFrameFits(artCols, artRows) {
		artCols, artRows = artCols-2, artRows-2
		startRow, startCol = startRow+1, startCol+1
	}
	key := fmt.Sprintf("%d:%d:%d:%d:%s:%s:%s:%d", startRow, startCol, artCols, artRows, m.ui.activeTab, subjectID, url, playerEpoch)
	changed, shouldDelete, placementChanged, urlChanged := m.ui.imgs.beginKittyOverlayState(key, url)
	if !changed {
		return ""
	}
	payload := m.ui.imgs.buildKittyPayload(url, encoded, artCols, artRows, m.ui.imgs.nextKittyImageID())
	if payload == "" {
		return kittyDeleteAll
	}
	out := fmt.Sprintf("\x1b7\x1b[%d;%dH%s\x1b8", startRow, startCol, payload)
	if shouldDelete && (placementChanged || urlChanged) {
		return kittyDeleteAll + out
	}
	return out
}
