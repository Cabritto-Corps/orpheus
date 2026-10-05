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

	if m.startupPending() {
		statusStr = m.styles.styleHeaderPaused.Render("Orpheus")
		if m.ui.authLoginURL != "" && !m.ui.authLoginOpen {
			centerL1 = m.styles.styleHeaderSub.Render("Spotify sign-in required · press L to open")
		} else {
			centerL1 = m.styles.styleHeaderSub.Render("connecting to Spotify…")
		}
		device := m.icon(iconDevice, iconDeviceNF) + " " + m.deviceName
		rightL1 = m.styles.styleHeaderStatus.Render(truncate(device, max(1, w-lipgloss.Width(statusStr)-2)))
	} else if m.transport.status != nil {
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

	row1 := layoutThreeZoneParts(w, statusStr, centerL1, rightL1)
	line1 := row1.row

	var centerL2 string
	if m.transport.status != nil && !m.startupPending() {
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
	// One center column for title and subtitle: expanding them through
	// separate formulas drifts the two lines a cell or two apart.
	var line2 string
	if centerW2 := lipgloss.Width(centerL2); centerW2 > 0 {
		centerW1 := lipgloss.Width(centerL1)
		start2 := row1.centerStart + (centerW1-centerW2)/2
		if centerW2 > centerW1 {
			start2 = (w - centerW2) / 2
		}
		line2 = strings.Repeat(" ", max(0, start2)) + centerL2
		if pad := w - lipgloss.Width(line2); pad > 0 {
			line2 += strings.Repeat(" ", pad)
		}
	}

	sep := m.styles.sectionDivider(w)
	return line1 + "\n" + line2 + "\n" + sep
}

func layoutThreeZone(w int, left, center, right string) string {
	return layoutThreeZoneParts(w, left, center, right).row
}

// layoutThreeZoneParts also reports the center content's start column, so a
// following line can share the same center column (the subtitle drops under
// the title's center; separate centering formulas never quite agree).
func layoutThreeZoneParts(w int, left, center, right string) (out struct {
	row         string
	centerStart int
}) {
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
	out.centerStart = leftW + leftGap

	out.row = left +
		strings.Repeat(" ", leftGap) +
		center +
		strings.Repeat(" ", rightGap) +
		right
	return out
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
		{"Recents", tabRecents},
		{"Search", tabSearch},
		{"Player", tabPlayer},
	}
	var parts []string
	for _, entry := range tabs {
		label := entry.label
		if m.ui.width < 54 {
			label = map[string]string{"Recents": "Recent", "Playlists": "List", "Albums": "Alb", "Search": "Find", "Player": "Play"}[label]
		}
		if m.ui.activeTab == entry.t {
			parts = append(parts, m.styles.styleTabActive.Render(" "+label+" "))
		} else {
			parts = append(parts, m.styles.styleTabInactive.Render(" "+label+" "))
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

	// Always in place: the empty scaffold fills via the normal push flow
	// (user-directed — no gate, no shape change).
	elapsedMS, durationMS, playing := 0, 0, false
	if m.transport.status != nil {
		elapsedMS = m.transport.status.ProgressMS
		durationMS = m.transport.status.DurationMS
		playing = m.transport.status.Playing
	}

	playIcon, pauseIcon := m.playPauseGlyphs()
	stateIcon := m.styles.styleHeaderPaused.Render(pauseIcon)
	if playing {
		stateIcon = m.styles.styleHeaderPlaying.Render(playIcon)
	}

	if durationMS > 0 && elapsedMS > durationMS {
		elapsedMS = durationMS
	}
	if elapsedMS < 0 {
		elapsedMS = 0
	}

	pct := 0.0
	if durationMS > 0 {
		pct = max(0, min(1, float64(elapsedMS)/float64(durationMS)))
	}

	elapsed := m.styles.stylePlayerTime.Render(fmtDuration(elapsedMS))
	total := m.styles.stylePlayerTime.Render("--:--")
	if durationMS > 0 {
		total = m.styles.stylePlayerTime.Render(fmtDuration(durationMS))
	}

	elapsedW := lipgloss.Width(elapsed)
	totalW := lipgloss.Width(total)
	iconW := lipgloss.Width(stateIcon)
	progressW := barW - elapsedW - totalW - iconW - playerBarGaps*playerBarGap
	// A negative width must render empty, never panic Repeat (narrow
	// terminals, pre-resize startup frame).
	progressW = max(0, progressW)
	var progressStr string
	if durationMS <= 0 {
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
	// The viewport already owns the scrolled content; skip the full layout.
	var body string
	if vp := m.ui.helpViewport; vp != nil {
		body = vp.View()
		if vp.AtTop() {
			hint = m.ui.keys.QueueUp.Help().Key + "/" + m.ui.keys.QueueDown.Help().Key + " scroll   " + m.ui.keys.CloseModal.Help().Key + " close"
		}
	} else {
		body = m.helpGroupedBody(contentW, innerH-4)
	}

	return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Help"),
		m.styles.styleModalHint.Render(hint), body, modalW, innerH)
}

// View content cannot carry overlay bytes: v2 parks non-SGR escapes in
// zero-width cells the repaint engine never writes and drops mid-text ones.
// Save/restore framing keeps the renderer's cursor model exact; the bytes
// stay SGR-free so its delta-tracked pen stays exact too.
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

// Suppression checks at delivery, not build: an emission built before a
// modal opened would restore the image over the scrim. Pure deletes bypass
// the guard — a stray delete self-heals, a stray placement corrupts.
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

// ORPHEUS_KITTY_DUMP captures raw overlay bytes for a `cat` bisect;
// diagnostics must never break rendering.
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

// The bool marks content emissions: false (pure deletes) bypass delivery
// suppression, true is guarded by kittyOverlayCmd.
func (m model) kittyOverlayBytes() (string, bool) {
	cover, coverContent := m.kittyCoverOverlayBytes()
	thumbs, thumbContent := m.kittyThumbnailOverlayBytes()
	return cover + thumbs, coverContent || thumbContent
}

func (m model) kittyCoverOverlayBytes() (string, bool) {
	if m.ui.imgs == nil {
		return "", false
	}
	if tooSmallFrame(m.ui.width, m.ui.height) {
		// The frame never laid out: clear any placement.
		if id := m.ui.imgs.takePendingKittyPurge(); id != 0 {
			return deleteKittyImageData(id), false
		}
		return clearKittyPreview(m.ui.imgs), false
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
	if m.startupPending() {
		// Startup reveal: panels hold the connecting spinner, so no art may
		// land yet. Pure deletes keep flowing so a stranded image self-heals.
		if id := m.ui.imgs.takePendingKittyPurge(); id != 0 {
			return deleteKittyImageData(id), false
		}
		return clearKittyPreview(m.ui.imgs), false
	}
	if m.ui.width < 64 && (m.ui.activeTab == tabSearch || m.ui.activeTab == tabRecents) {
		// Narrow layouts contain only the list, not a large preview panel.
		return clearKittyPreview(m.ui.imgs), false
	}
	layout := m.bodyLayout()
	rect := m.coverArt(layout.coverCols, layout.coverRows)
	if rect.empty() {
		return clearKittyPreview(m.ui.imgs), false
	}

	var url, subjectID string
	switch m.ui.activeTab {
	case tabPlaylists:
		if pl, ok := m.stablePlaylistSelection(); ok {
			url = pl.summary.ImageURL
			subjectID = strings.TrimSpace(pl.summary.ID)
		}
	case tabAlbums:
		if al, ok := m.stableAlbumSelection(); ok {
			url = al.summary.ImageURL
			subjectID = strings.TrimSpace(al.summary.ID)
		}
	case tabSearch:
		if item, ok := m.browse.search.list.SelectedItem().(searchResultItem); ok {
			url = item.result.ImageURL
			subjectID = strings.TrimSpace(item.result.ID)
		}
	case tabRecents:
		if item, ok := m.browse.recentsList.SelectedItem().(trackItem); ok {
			url = item.item.ImageURL
			subjectID = strings.TrimSpace(item.item.ID)
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
		return clearKittyPreview(m.ui.imgs), false
	}

	encoded := m.ui.imgs.encodedFor(url)
	if encoded == "" {
		// Same surface still loading: hold the old cover — never blank.
		// A tab switch is a different surface, so its stale art clears.
		if m.ui.imgs.kittyShownTab() == m.ui.activeTab {
			return "", false
		}
		return clearKittyPreview(m.ui.imgs), false
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

// Empty previews keep the cached image available for a fast restore, but
// repeatedly erase its placement so stale art cannot linger over placeholders.
func clearKittyPreview(imgs *imgCache) string {
	if imgs == nil {
		return ""
	}
	return imgs.hideOverlayForEmptyPreview()
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
		// Unchanged intent: the placement in the terminal is untouched (the
		// renderer only rewrites changed cells), so re-placing would be pure
		// churn — a placement erase before every put that the steady flow
		// shows as the art blinking at tick cadence. The one exception is a
		// modal-close restore, which reneeds its erased placement.
		if !m.ui.imgs.takePendingOverlayRestore() {
			return "", false
		}
		return m.ui.imgs.frameOverlayPlacement(rect), true
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
