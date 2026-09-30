package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/spotify"
)

// View content cannot carry kitty bytes: the v2 pipeline parks non-SGR
// escapes in zero-width cells the repaint engine never writes (and drops
// mid-text ones outright), so anything graphics-flavored in Content dies
// silently no matter how correct the bytes are. The overlay travels via
// tea.Raw instead; this pins the separation on both sides.
func TestViewContentCarriesNoGraphicsBytes(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 100
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1", Playing: true, ProgressMS: 37000, DurationMS: 200000}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	if content := m.View().Content; strings.Contains(content, "\x1b_G") {
		t.Fatalf("graphics bytes in View content never reach the wire — emit via tea.Raw: %q", tail(content, 200))
	}
}

// The Raw emission brackets the placement with save/restore so the
// renderer's cursor model stays exact (CUP moves the physical cursor;
// DECRC puts it back), and stays SGR-free so its delta-tracked pen does.
func TestKittyOverlayRawFramesWithSaveRestore(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 100
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1", Playing: true, ProgressMS: 37000, DurationMS: 200000}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	out := m.kittyOverlay()
	if !strings.HasPrefix(out, "\x1b7") || !strings.HasSuffix(out, "\x1b8") {
		t.Fatalf("expected save/restore framing around the emission, got %q", tail(out, 120))
	}
	if !strings.Contains(out, "a=T") {
		t.Fatalf("expected the transmit inside the framing, got %q", tail(out, 120))
	}
}

// The overlay Cmd hands the exact frame bytes to tea.Raw (which the
// program serializes with frame flushes), and stays nil when the frame
// carries nothing — the byte builders already suppress no-op frames.
func TestKittyOverlayCmdEmitsRaw(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 100
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1", Playing: true, ProgressMS: 37000, DurationMS: 200000}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	cmd := m.kittyOverlayCmd()
	if cmd == nil {
		t.Fatal("expected an overlay command on a fresh intent")
	}
	msg, ok := cmd().(tea.RawMsg)
	if !ok {
		t.Fatalf("expected tea.RawMsg, got %T", cmd())
	}
	// Each frame emission mints a fresh placement ID (diff-dedupe
	// defeat), so consecutive commands must differ while naming the
	// same stored image.
	first := fmt.Sprint(msg.Msg)
	second, ok := m.kittyOverlayCmd()().(tea.RawMsg)
	if !ok {
		t.Fatal("expected a second overlay command on the unchanged intent")
	}
	secondBytes := fmt.Sprint(second.Msg)
	if first == secondBytes {
		t.Fatal("consecutive overlay commands are byte-identical")
	}
	if !strings.Contains(first, "a=T") {
		t.Fatalf("expected the first command to transmit, got %q", tail(first, 120))
	}
	if !strings.Contains(secondBytes, "a=p") || strings.Contains(secondBytes, "a=T") {
		t.Fatalf("expected the second command to re-place, got %q", tail(secondBytes, 120))
	}
	m.ui.imgs.protocol = imageProtocolNone
	// No pending purge here (the slot was never reset for a switch), so
	// the off-protocol path stays silent. A real style switch carries a
	// pending purge instead: TestKittyStyleSwitchToPixelatedPurgesShownImage.
	if cmd := m.kittyOverlayCmd(); cmd != nil {
		t.Fatal("expected no overlay command when the protocol is off")
	}
}

// A re-place built before a modal opens must not deliver after it:
// tea.Raw cmds execute post-frame, so an emission built pre-modal would
// resurrect the image over the scrim — and the modal branch would stay
// silent, stranding it permanently. The emission-time guard drops stale
// content at delivery instead.
func TestKittyOverlayStaleReplaceDroppedAfterModalOpens(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	m.ui.width = 100
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1", Playing: true, ProgressMS: 37000, DurationMS: 200000}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	if out := m.kittyOverlay(); !strings.Contains(out, "a=T") {
		t.Fatalf("expected initial transmit, got %q", out)
	}
	cmd := m.kittyOverlayCmd()
	if cmd == nil {
		t.Fatal("expected a re-place command on the unchanged intent")
	}
	// The Update wrapper mirrors modal state onto the slot; simulate
	// the modal-open Update running before the queued cmd executes.
	m.ui.imgs.setOverlaySuppressed(true)
	if msg := cmd(); msg != nil {
		t.Fatalf("stale pre-modal emission delivered after modal open: %v", msg)
	}
	// While un-suppressed the same shape of emission still delivers.
	m.ui.imgs.setOverlaySuppressed(false)
	live := m.kittyOverlayCmd()
	if live == nil {
		t.Fatal("expected a live emission while un-suppressed")
	}
	raw, ok := live().(tea.RawMsg)
	if !ok || !strings.Contains(fmt.Sprint(raw.Msg), "a=p") {
		t.Fatalf("expected the live emission to re-place, got %v", live())
	}
	// Pure-delete emissions bypass suppression entirely: the modal
	// hide itself must always deliver.
	m.ui.helpOpen = true
	m.ui.imgs.setOverlaySuppressed(true)
	hideCmd := m.kittyOverlayCmd()
	if hideCmd == nil {
		t.Fatal("expected the modal hide to bypass suppression")
	}
	hideMsg, ok := hideCmd().(tea.RawMsg)
	if !ok || !strings.Contains(fmt.Sprint(hideMsg.Msg), "a=d,d=i") {
		t.Fatalf("expected the hide to deliver under suppression, got %v", hideCmd())
	}
}

// The Update wrapper keeps the delivery guard in sync with the live
// modal state after every message: without it the guard would lag the
// model by a full Update and stale emissions would slip through.
func TestUpdateMirrorsModalStateOntoOverlaySlot(t *testing.T) {
	t.Setenv("TMUX", "")
	m := framedTestModel()
	if m.ui.imgs == nil {
		t.Fatal("expected test model to carry an image cache")
	}
	m.ui.helpOpen = true
	nm, _ := m.Update(tickMsg{})
	mm, ok := nm.(model)
	if !ok {
		t.Fatalf("expected model back from Update, got %T", nm)
	}
	if !mm.ui.imgs.overlaySuppressed() {
		t.Fatal("expected suppression set while a modal is open")
	}
	mm.ui.helpOpen = false
	nm2, _ := mm.Update(tickMsg{})
	if nm2.(model).ui.imgs.overlaySuppressed() {
		t.Fatal("expected suppression cleared once the modal closes")
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
