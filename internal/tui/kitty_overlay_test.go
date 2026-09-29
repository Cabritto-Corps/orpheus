package tui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"orpheus/internal/spotify"
)

func framedTestModel() model {
	m := NewLoaderModel()
	st := themePresetState("default")
	st.cover.Frame = "rounded"
	m.styles = buildThemeStyles(st)
	return m
}

func TestCoverArtDerivesSingleRect(t *testing.T) {
	m := NewLoaderModel()

	// Framed cell: art insets one cell inside the ring on every side.
	rect := framedTestModel().coverArt(30, 15)
	if !rect.framed {
		t.Fatal("expected 30x15 cell to take the frame")
	}
	if rect.cols != 28 || rect.rows != 13 {
		t.Fatalf("expected inset dims 28x13, got %dx%d", rect.cols, rect.rows)
	}
	if rect.row != bodyStartRow1Based+2+1 || rect.col != 2 {
		t.Fatalf("expected framed anchor one cell inside the cell origin, got (%d,%d)", rect.row, rect.col)
	}

	// Unframed cell: identity.
	rect = m.coverArt(4, 2)
	if rect.framed {
		t.Fatal("expected 4x2 cell to skip the frame")
	}
	if rect.cols != 4 || rect.rows != 2 || rect.row != bodyStartRow1Based+2 || rect.col != 1 {
		t.Fatalf("expected unframed identity rect, got %+v", rect)
	}

	// Degenerate cell: empty, so neither renderer emits.
	if rect := m.coverArt(0, 0); !rect.empty() {
		t.Fatalf("expected empty rect for zero cell, got %+v", rect)
	}
}

func TestKittyOverlayFramedPlacementMatchesPanelAnchor(t *testing.T) {
	m := framedTestModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="

	// The overlay must place the image at the derived rect — CUP anchor
	// plus display extents — so the ANSI cell and the kitty placement
	// agree by construction (both consume coverArt).
	rect := m.coverArt(m.bodyLayout().coverCols, m.bodyLayout().coverRows)
	if !rect.framed {
		t.Fatal("expected the 120x40 layout to take the frame")
	}
	out := m.kittyOverlay()
	if out == "" {
		t.Fatal("expected overlay emission")
	}
	if want := fmt.Sprintf("\x1b[%d;%dH", rect.row, rect.col); !strings.Contains(out, want) {
		t.Fatalf("expected CUP at derived anchor %q, got %q", want, out)
	}
	if want := fmt.Sprintf("c=%d,r=%d", rect.cols, rect.rows); !strings.Contains(out, want) {
		t.Fatalf("expected display extents %q, got %q", want, out)
	}
}

func TestOverlayDisplacedImageDeletedByID(t *testing.T) {
	c := newImgCache()
	a := overlayIntent{tab: tabPlayer, subject: "track-1", url: "u1"}
	emit, tx, disp := c.commitOverlayIntent(a)
	if !emit || tx == 0 || disp != 0 {
		t.Fatalf("expected first commit to emit with fresh ID and no delete, got emit=%v tx=%d disp=%d", emit, tx, disp)
	}
	if emit, _, _ := c.commitOverlayIntent(a); emit {
		t.Fatal("expected unchanged intent to suppress output")
	}
	b := overlayIntent{tab: tabPlayer, subject: "track-2", url: "u2"}
	emit, tx2, disp2 := c.commitOverlayIntent(b)
	if !emit {
		t.Fatal("expected changed intent to emit")
	}
	if disp2 != tx {
		t.Fatalf("expected displaced ID %d (the shown image), got %d", tx, disp2)
	}
	if tx2 == tx {
		t.Fatal("expected a fresh transmission ID so renderer diff cannot swallow the emission")
	}

	// End to end: the old image ID is explicitly deleted, never globally.
	m := NewLoaderModel()
	m.ui.width = 120
	m.ui.height = 40
	m.ui.activeTab = tabPlayer
	m.transport.status = &spotify.PlaybackStatus{TrackID: "track-1", AlbumImageURL: "u1"}
	m.ui.imgs.protocol = imageProtocolKitty
	m.ui.imgs.encoded["u1"] = "ZmFrZQ=="
	m.ui.imgs.encoded["u2"] = "ZmFrZQ=="
	if first := m.kittyOverlay(); first == "" {
		t.Fatal("expected initial kitty render")
	}
	shownID := m.ui.imgs.overlayShownID()
	m.transport.status.TrackID = "track-2"
	m.transport.status.AlbumImageURL = "u2"
	second := m.kittyOverlay()
	if second == "" {
		t.Fatal("expected redraw when player cover URL changes")
	}
	if want := fmt.Sprintf("d=i,i=%d", shownID); !strings.Contains(second, want) {
		t.Fatalf("expected old image explicitly deleted by ID (%q), got %q", want, second)
	}
	if strings.Contains(second, "a=d,d=A") {
		t.Fatalf("expected no global delete-all on content swap, got %q", second)
	}
	if !strings.Contains(second, "ZmFrZQ==") {
		t.Fatalf("expected redraw to include the new cover payload, got %q", second)
	}
}

func TestHideOverlayForModalDeletesOnceKeepsIntent(t *testing.T) {
	c := newImgCache()
	intent := overlayIntent{tab: tabPlayer, subject: "track-1", url: "u1"}
	emit, tx, _ := c.commitOverlayIntent(intent)
	if !emit || tx == 0 {
		t.Fatal("expected initial overlay commit to emit")
	}
	// Repeated modal frames keep deleting the same ID (deleting an unknown
	// ID is a terminal no-op) while the slot is retained.
	first := c.hideOverlayForModal()
	if want := fmt.Sprintf("a=d,d=i,i=%d", tx); !strings.Contains(first, want) {
		t.Fatalf("expected modal delete of shown ID (%q), got %q", want, first)
	}
	if again := c.hideOverlayForModal(); again != first {
		t.Fatalf("expected repeated modal frames to keep deleting, got %q vs %q", again, first)
	}
	// The unblocking frame retransmits the kept intent without a redundant
	// delete: the modal frames already removed it.
	emit, tx2, disp := c.commitOverlayIntent(intent)
	if !emit {
		t.Fatal("expected retransmit after modal closes")
	}
	if disp != 0 {
		t.Fatalf("expected no redundant delete on modal resume, got %d", disp)
	}
	if tx2 == tx {
		t.Fatal("expected a fresh transmission ID on resume")
	}
}

func TestCoverWaitsForInflightRenderInsteadOfSpinning(t *testing.T) {
	c := newImgCache()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	c.setImage("u-wait", img, 0, 0)
	key := coverKey{url: "u-wait", cols: 8, rows: 8}

	// Simulate an in-flight render owned elsewhere: the waiter must block
	// on the completion channel, not spin and not render a duplicate.
	ch := make(chan struct{})
	c.mu.Lock()
	c.rendering[key] = ch
	c.mu.Unlock()

	done := make(chan string, 1)
	go func() {
		s, _ := c.cover(key.url, key.cols, key.rows)
		done <- s
	}()

	// Complete the render the way renderAndCache does: cache the result,
	// drop the in-flight marker, then close (broadcast to all waiters).
	c.mu.Lock()
	c.covers.Set(key, "rendered")
	delete(c.rendering, key)
	c.mu.Unlock()
	close(ch)

	select {
	case got := <-done:
		if got != "rendered" {
			t.Fatalf("expected waiter to receive the completed render, got %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cover() did not return after render completion")
	}
}

func TestKittyOverlayPayloadSurvivesFrameworkDraw(t *testing.T) {
	m := guardModel(t, frameVariant{width: 100, height: 40, tab: tabPlaylists})
	m.ui.imgs.setProtocol(imageProtocolKitty)
	big := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	var seed uint32 = 0xabcdef01
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			seed = seed*1664525 + 1013904223
			big.Set(x, y, color.NRGBA{R: uint8(seed >> 16), G: uint8(seed >> 8), B: uint8(seed), A: 255})
		}
	}
	const curl = "probe://cover-draw"
	m.ui.imgs.setImage(curl, big, 40, 20)
	if err := m.ui.imgs.ensureKittyEncoding(curl, big); err != nil {
		t.Fatal(err)
	}
	enc := m.ui.imgs.encodedFor(curl)
	if len(enc) <= 4096 {
		t.Fatalf("probe image too compressible for a multi-chunk test: %d bytes", len(enc))
	}
	items := m.browse.playlistList.Items()
	if pi, ok := items[0].(playlistItem); ok {
		pi.summary.ImageURL = curl
		items[0] = pi
		m.browse.playlistList.SetItems(items)
	}
	m.browse.playlistList.Select(0)
	content := m.View().Content
	if !strings.Contains(content, "\x1b_G") {
		t.Fatal("no kitty payload in View content")
	}
	// drive the REAL framework draw path, exactly like the v2 flush()
	sb := uv.NewScreenBuffer(100, 40)
	uv.NewStyledString(content).Draw(sb, sb.Bounds())
	var joined strings.Builder
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			if c := sb.CellAt(x, y); c != nil {
				joined.WriteString(c.Content)
			}
		}
	}
	var parts []string
	// NOTE: the ST terminator is ESC + ONE backslash: "\x1b\\" in Go source.
	for _, seg := range strings.Split(joined.String(), "\x1b\\") {
		rest := seg
		// strip a leading CUP ("\x1b[ROW;COLH") before looking for the
		// payload separator: both contain ";", and base64 itself may
		// contain "H", so order matters.
		if i := strings.Index(rest, "\x1b["); i >= 0 {
			if j := strings.Index(rest[i:], "H"); j >= 0 {
				rest = rest[i+j+1:]
			}
		}
		if i := strings.LastIndex(rest, ";"); i >= 0 {
			parts = append(parts, rest[i+1:])
		}
	}
	if got := strings.Join(parts, ""); got != enc {
		t.Fatalf("payload corrupted by framework draw: reassembled %d of %d base64 bytes", len(got), len(enc))
	}
}
