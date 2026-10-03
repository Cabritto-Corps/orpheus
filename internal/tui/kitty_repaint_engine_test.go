package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"orpheus/internal/spotify"
)

// v2 silently drops graphics bytes from View() content (zero-width cells,
// mid-text escapes), however correct the bytes are; the overlay travels via
// tea.Raw.
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

// Save/restore keeps the renderer's cursor model exact (DECRC puts the CUP
// move back); the emission stays SGR-free so its delta-tracked pen does.
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

// Nil when the frame is empty: the byte builders already suppress no-op frames.
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
	first := fmt.Sprint(msg.Msg)
	if !strings.Contains(first, "a=T") {
		t.Fatalf("expected the first command to transmit, got %q", tail(first, 120))
	}
	// Unchanged intent: re-placing churns erase/put loops the user sees as flicker.
	if cmd := m.kittyOverlayCmd(); cmd != nil {
		t.Fatal("expected no overlay command for the unchanged intent")
	}
	m.ui.imgs.protocol = imageProtocolNone
	// No pending purge here (the slot was never reset for a switch); a real
	// style switch carries one: TestKittyStyleSwitchToPixelatedPurgesShownImage.
	if cmd := m.kittyOverlayCmd(); cmd != nil {
		t.Fatal("expected no overlay command when the protocol is off")
	}
}

// tea.Raw executes post-frame: a pre-modal emission would resurrect the
// image over the scrim and strand silently; the guard drops it at delivery.
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
	// A content emission for a NEW subject built pre-modal…
	m.ui.imgs.encoded["u2"] = "ZmFnZQ=="
	m.transport.status.AlbumImageURL = "u2"
	cmd := m.kittyOverlayCmd()
	if cmd == nil {
		t.Fatal("expected the changed-subject command to build")
	}
	// The Update wrapper mirrors modal state onto the slot; simulate the
	// modal-open Update before the queued cmd executes.
	m.ui.imgs.setOverlaySuppressed(true)
	if msg := cmd(); msg != nil {
		t.Fatalf("stale pre-modal emission delivered after modal open: %v", msg)
	}
	m.ui.imgs.setOverlaySuppressed(false)
	m.ui.helpOpen = true
	if hide := m.kittyOverlay(); hide != "" && !strings.Contains(hide, "a=d,d=i") {
		t.Fatalf("expected the modal hide to be built for the new subject, got %q", hide)
	}
	m.ui.helpOpen = false
	if cmd := m.kittyOverlayCmd(); cmd == nil {
		t.Fatal("expected the modal-close re-place command for the new subject")
	}
	// Pure-delete emissions bypass suppression: the modal hide must always deliver.
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

// Without this sync the delivery guard would lag the model by a full Update
// and stale emissions would slip through.
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
