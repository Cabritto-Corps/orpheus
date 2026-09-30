package tui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	golibrespot "github.com/elxgy/go-librespot"
)

func (m model) headerView() string {
	w := m.ui.width

	var statusStr, centerL1, rightL1 string

	if m.transport.status != nil {
		playIcon, pauseIcon := m.playPauseGlyphs()
		if m.transport.status.Playing {
			statusStr = m.styles.styleHeaderPlaying.Render("[" + playIcon + " Playing]")
		} else {
			statusStr = m.styles.styleHeaderPaused.Render("[" + pauseIcon + " Paused]")
		}

		volBar := m.headerVolumeBar(m.transport.status.Volume)
		volText := m.styles.styleHeaderVolume.Render(fmt.Sprintf("%3d%%", m.transport.status.Volume))
		rightL1 = volBar + " " + volText
		if m.transport.status.ShuffleState {
			rightL1 += "  " + m.styles.styleDimmed.Render(m.icon(iconShuffle, iconShuffleNF))
		}
		if m.transport.status.RepeatTrack {
			rightL1 += "  " + m.styles.styleDimmed.Render(m.icon(iconRepeatTrack, iconRepeatTrackNF))
		} else if m.transport.status.RepeatContext {
			rightL1 += "  " + m.styles.styleDimmed.Render(m.icon(iconRepeatContext, iconRepeatContextNF))
		}

		availCenterW := max(10, w-lipgloss.Width(statusStr)-lipgloss.Width(rightL1)-2)
		trackName := m.transport.status.TrackName
		if trackName == "" {
			trackName = "Unknown track"
		}
		if m.transport.transition.Pending() {
			// Pushed status still describes the outgoing track; mark
			// it so it never reads as "paused".
			trackName = truncate(trackName, max(availCenterW-1, 1)) + "…"
		}
		centerL1 = m.styles.styleHeaderCenter.Render(truncate(trackName, availCenterW))
	} else {
		statusStr = m.styles.styleHeaderPaused.Render("Orpheus")
		centerL1 = m.styles.styleHeaderSub.Render("no active playback")
		device := m.icon(iconDevice, iconDeviceNF) + " " + m.deviceName
		// Must fit the full width here or it wraps and corrupts the
		// chrome height (no truncation side exists in this state).
		rightL1 = m.styles.styleHeaderStatus.Render(truncate(device, max(1, w-lipgloss.Width(statusStr)-2)))
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
		centerL2 = m.styles.styleHeaderSub.Render(truncate(parts, max(1, w-2)))
	}
	line2 := layoutThreeZone(w, "", centerL2, "")

	sep := m.styles.sectionDivider(w)
	return line1 + "\n" + line2 + "\n" + sep
}

func layoutThreeZone(w int, left, center, right string) string {
	leftW := lipgloss.Width(left)

	// The right zone must always fit.
	right = fitCell(right, max(0, w-leftW))
	rightW := lipgloss.Width(right)

	// Truncate BEFORE joining: a style would wrap instead of clip. The
	// title is positioned absolutely on the terminal, so it must clear
	// both halves or the row overflows by the overlap.
	centerBudget := min(max(0, w-leftW-rightW-2), max(0, w-2*leftW), max(0, w-2*rightW))
	center = fitCell(center, centerBudget)
	centerW := lipgloss.Width(center)

	// Centered on the terminal, not between the side zones: a growing
	// side zone eats its own gap instead of pushing the title aside.
	leftGap := max(0, (w-centerW)/2-leftW)
	rightGap := max(0, w-leftW-leftGap-centerW-rightW)

	return left +
		strings.Repeat(" ", leftGap) +
		center +
		strings.Repeat(" ", rightGap) +
		right
}

func (m model) headerVolumeBar(vol int) string {
	return m.styles.gradientBar(float64(vol)/100.0, volumeBarW)
}

func (m model) tabBarView() string {
	key := tabBarCacheKey{m.ui.width, m.ui.activeTab}
	if cached, ok := m.styles.tabBar.get(key); ok {
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
			parts = append(parts, m.styles.styleTabActive.Render(" "+entry.label+" "))
		} else {
			parts = append(parts, m.styles.styleTabInactive.Render(" "+entry.label+" "))
		}
	}
	sep := m.styles.styleDivider.Render("\u2502")
	bar := strings.Join(parts, sep)
	underline := m.styles.sectionDivider(m.ui.width)
	out := bar + "\n" + underline
	m.styles.tabBar.put(key, out)
	return out
}
func (m model) playerBarView() string {
	barW := m.ui.width

	sep := m.styles.sectionDivider(barW)

	if m.transport.status == nil {
		// Full bar height even idle, or the bottom gutter shifts
		// between idle and playing frames.
		return sep + "\n"
	}
	playIcon, pauseIcon := m.playPauseGlyphs()
	stateIcon := m.styles.styleHeaderPaused.Render(pauseIcon)
	if m.transport.status.Playing {
		stateIcon = m.styles.styleHeaderPlaying.Render(playIcon)
	}

	elapsedMs := m.transport.status.ProgressMS
	if m.transport.status.DurationMS > 0 && elapsedMs > m.transport.status.DurationMS {
		elapsedMs = m.transport.status.DurationMS
	}
	if elapsedMs < 0 {
		elapsedMs = 0
	}

	pct := 0.0
	if m.transport.status.DurationMS > 0 {
		pct = float64(elapsedMs) / float64(m.transport.status.DurationMS)
		pct = max(0, min(1, pct))
	}

	elapsed := m.styles.stylePlayerTime.Render(fmtDuration(elapsedMs))
	total := m.styles.stylePlayerTime.Render("--:--")
	if m.transport.status.DurationMS > 0 {
		total = m.styles.stylePlayerTime.Render(fmtDuration(m.transport.status.DurationMS))
	}

	elapsedW := lipgloss.Width(elapsed)
	totalW := lipgloss.Width(total)
	iconW := lipgloss.Width(stateIcon)
	progressW := barW - elapsedW - totalW - iconW - playerBarGaps*playerBarGap
	// A negative width must render empty, never panic Repeat (narrow
	// terminals, pre-resize startup frame).
	progressW = max(0, progressW)
	var progressStr string
	if m.transport.status.DurationMS <= 0 {
		_, empty := m.styles.themeBarRunes()
		progressStr = m.styles.styleProgressBarEmpty.Render(strings.Repeat(string(empty), progressW))
	} else {
		progressStr = m.styles.gradientBar(pct, progressW)
	}

	bar := "  " + stateIcon + "  " + elapsed + "  " + progressStr + "  " + total
	return sep + "\n" + bar
}

func (m model) trackPopupView() string {
	modalW, _, listH := popupModalSize(m.ui.width, m.ui.height)
	innerH := listH + 2

	title := m.styles.styleTrackPopupTitle.Render("  " + m.ui.trackPopupName)

	var body string
	if m.ui.trackPopupItems == nil {
		body = m.styles.styleTrackPopupLoading.Render("\n  " + m.ui.spinner.View() + " Loading...")
	} else if len(m.ui.trackPopupItems) == 0 {
		body = m.styles.styleTrackPopupLoading.Render("\n  No tracks found")
	} else {
		body = m.ui.trackPopupList.View()
	}
	var hint string
	if m.ui.trackPopupItems != nil {
		hint = m.styles.styleTrackPopupHint.Render(m.styles.hintLine([]key.Binding{m.ui.keys.Select, m.ui.keys.Filter, m.ui.keys.CloseModal}, modalW-modalContentInset))
	}

	return m.styles.modalFrame(m.ui.width, m.ui.height, title, hint, body, modalW, innerH)
}

// One source for both Update-side rebuild and View-side render, so
// the two can never drift.
func helpModalSize(termW, termH int) (modalW, innerH, contentW int) {
	wantedH := max(6, termH-headerH-2)
	var boxH int
	modalW, boxH, contentW = modalRect(termW, termH, termW-4, wantedH)
	innerH = boxH
	return modalW, innerH, contentW
}

// Runs from Update paths (open, resize): View cannot persist state.
func (m *model) ensureHelpViewport() {
	_, innerH, contentW := helpModalSize(m.ui.width, m.ui.height)
	body := m.helpGroupedBody(contentW, innerH-4)
	if lipgloss.Height(body) > innerH-2 {
		v := viewport.New(viewport.WithWidth(contentW), viewport.WithHeight(innerH-2))
		v.SetContent(body)
		m.ui.helpViewport = &v
	} else {
		m.ui.helpViewport = nil
	}
}

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

	hint := m.ui.keys.QueueUp.Help().Key + "/" + m.ui.keys.QueueDown.Help().Key + " scroll   " + m.ui.keys.ToggleHelp.Help().Key + " or " + m.ui.keys.CloseModal.Help().Key + " close"
	body := m.helpGroupedBody(contentW, innerH-4)
	if vp := m.ui.helpViewport; vp != nil {
		body = vp.View()
		if vp.AtTop() {
			hint = m.ui.keys.QueueUp.Help().Key + "/" + m.ui.keys.QueueDown.Help().Key + " scroll   " + m.ui.keys.CloseModal.Help().Key + " close"
		}
	}

	return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Help"),
		m.styles.styleModalHint.Render(hint), body, modalW, innerH)
}

// View content CANNOT carry overlay bytes (v2 parks non-SGR escapes in
// zero-width cells the repaint engine never writes, and drops mid-text
// ones outright). Save/restore keeps the renderer's cursor model exact
// (CUP moves the cursor, DECRC puts it back); SGR-free bytes keep the
// delta-tracked pen exact too.
func frameKittyBytes(out string) string {
	if out == "" {
		return ""
	}
	framed := "\x1b7" + out + "\x1b8"
	dumpKittyOverlay(framed)
	return framed
}

func (m model) kittyOverlay() string {
	out, _ := m.kittyOverlayBytes()
	return frameKittyBytes(out)
}

// Suppression is checked at delivery, not at build: a stale re-place
// built pre-modal is dropped instead of resurrecting the image over
// the scrim. Pure deletes bypass the guard (stray deletes self-heal
// via re-place/restore; stray placements corrupt).
func (m model) kittyOverlayCmd() tea.Cmd {
	out, content := m.kittyOverlayBytes()
	if out == "" {
		return nil
	}
	framed := frameKittyBytes(out)
	if !content {
		return tea.Raw(framed)
	}
	imgs := m.ui.imgs
	inner := tea.Raw(framed)
	return func() tea.Msg {
		if imgs != nil && imgs.overlaySuppressed() {
			return nil
		}
		return inner()
	}
}

// ORPHEUS_KITTY_DUMP captures the exact overlay bytes for a `cat`
// bisect (app bytes vs terminal/tmux handling). Best-effort: diagnostics
// must never break rendering.
func dumpKittyOverlay(out string) {
	path := strings.TrimSpace(os.Getenv("ORPHEUS_KITTY_DUMP"))
	if path == "" || out == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(out)
}

// The bool marks image-data emissions: false (pure deletes) bypasses
// delivery suppression; true (transmit, re-place) is guarded by
// kittyOverlayCmd.
func (m model) kittyOverlayBytes() (string, bool) {
	if m.ui.imgs == nil {
		return "", false
	}
	if m.ui.imgs.protocolForRender() != imageProtocolKitty {
		// The switch resets the slot without naming the shown image;
		// its pending purge goes out once here, then silence.
		if id := m.ui.imgs.takePendingKittyPurge(); id != 0 {
			return deleteKittyImageData(id), false
		}
		return "", false
	}
	if m.modalKind() != modalNone {
		// Image-scoped delete every live frame — never once-and-silent,
		// so a missed delete or resurrected placement self-heals next
		// frame. z=-1 sits under text as a bonus only; default-background
		// cells may still show through.
		return m.ui.imgs.hideOverlayWhileModal(), false
	}
	layout := m.bodyLayout()
	rect := m.coverArt(layout.coverCols, layout.coverRows)
	if rect.empty() {
		return deleteKittyImage(m.ui.imgs.clearOverlayIntent()), false
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
		return deleteKittyImage(m.ui.imgs.clearOverlayIntent()), false
	}

	encoded := m.ui.imgs.encodedFor(url)
	if encoded == "" {
		// Same surface still loading: hold the old cover — never blank.
		// A tab switch is a different surface, so its stale art clears.
		if m.ui.imgs.kittyShownTab() == m.ui.activeTab {
			return "", false
		}
		return deleteKittyImage(m.ui.imgs.clearOverlayIntent()), false
	}

	revision := uint64(0)
	if m.ui.activeTab == tabPlayer {
		revision = m.transport.playerCoverEpoch
	}
	intent := overlayIntent{
		art:      rect,
		tab:      m.ui.activeTab,
		subject:  subjectID,
		url:      url,
		revision: revision,
	}
	return m.kittyTransmitNewCover(intent, rect, encoded)
}

// A pre-transmit pending purge rides along front: no stranded image
// survives a rapid switch round trip.
func (m model) kittyTransmitNewCover(intent overlayIntent, rect artRect, encoded string) (string, bool) {
	emit, transmitID, displacedID := m.ui.imgs.commitOverlayIntent(intent)
	purgePrefix := ""
	if id := m.ui.imgs.takePendingKittyPurge(); id != 0 {
		purgePrefix = deleteKittyImageData(id)
	}
	if !emit {
		// Repaints erase placements (the art rect is blank text) and the
		// frame diff swallows byte-identical emissions: re-place with a
		// fresh placement ID every frame instead of emitting nothing.
		return purgePrefix + m.ui.imgs.frameOverlayPlacement(rect), true
	}
	payload := buildKittyPayload(encoded, rect.cols, rect.rows, transmitID)
	if payload == "" {
		return purgePrefix + deleteKittyImage(displacedID), false
	}
	// The replacement lands fully placed before the displaced image
	// drops: terminals can present mid-transmission, and a purge-first
	// order would flash the gap. C=1 keeps the cursor where CUP put it;
	// the hidden alt-screen cursor makes C=1-ignoring terminals harmless.
	return purgePrefix + fmt.Sprintf("\x1b[%d;%dH%s%s", rect.row, rect.col, payload, deleteKittyImageData(displacedID)), true
}
