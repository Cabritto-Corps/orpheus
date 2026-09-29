package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// artRect is the exact cell rectangle an image occupies, plus whether it
// sits inside a frame ring: the single source both the ANSI cover renderer
// and the kitty overlay derive from, so the two can never disagree about
// where the art sits. The anchor is layout-invariant (panel label +
// divider above the cell, panel flush left), so the rectangle derives from
// the cell dims alone; the layout struct carries no art fields.
type artRect struct {
	row, col int
	cols     int
	rows     int
	framed   bool
}

func (r artRect) empty() bool {
	return r.cols <= 0 || r.rows <= 0
}

// coverArt derives the art rectangle from a laid-out cover cell: the cover
// always starts two lines below the body (panel label + divider) at the
// panel's first column, and a framed cover insets one cell inside the
// border ring on every side.
func (m model) coverArt(cellCols, cellRows int) artRect {
	cols, rows := cellCols, cellRows
	framed := m.styles.coverFrameFits(cols, rows)
	row, col := bodyStartRow1Based+2, 1
	if framed {
		cols, rows = cols-2, rows-2
		row, col = row+1, col+1
	}
	return artRect{row: row, col: col, cols: cols, rows: rows, framed: framed}
}

// overlayIntent is what the frame wants displayed: a typed replacement for
// the old colon-joined key string (which extracted placement by scanning
// for the 4th colon and broke if any earlier field ever gained one).
type overlayIntent struct {
	art      artRect
	tab      tab
	subject  string
	url      string
	revision uint64
}

// overlayState is the single displayed-image slot. At most one cover is
// ever on screen, so one slot plus a monotonic ID counter is the whole
// bookkeeping: no chunk caches, no string keys, no parallel counters.
type overlayState struct {
	shown   overlayIntent
	shownID uint64
	visible bool
	force   bool
	nextID  uint64
}

// commitOverlayIntent records the frame's overlay intent and reports what
// to emit. An unchanged visible intent suppresses output: the terminal
// already shows this image, and re-emitting would only churn bytes.
//
// Every emission mints a fresh image ID — not because the terminal dedupes
// (it doesn't), but because bubbletea's renderer diffs whole frames: an
// emission byte-identical to the previous frame would be swallowed and a
// needed retransmit lost. IDs mint only on intent transitions, never per
// tick. A displaced previous image is always deleted by ID first, so
// terminal image memory stays bounded by the one image on screen.
func (c *imgCache) commitOverlayIntent(intent overlayIntent) (emit bool, transmitID, displacedID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.force && intent == c.overlay.shown && c.overlay.visible {
		return false, 0, 0
	}
	c.overlay.nextID++
	if c.overlay.visible {
		displacedID = c.overlay.shownID
	}
	c.overlay.shown = intent
	c.overlay.shownID = c.overlay.nextID
	c.overlay.visible = true
	c.overlay.force = false
	return true, c.overlay.shownID, displacedID
}

// clearOverlayIntent forgets the displayed image and reports the ID to
// delete. The remembered intent is kept: when content returns for the same
// intent the commit path retransmits (the terminal holds nothing).
// Repeated clears report 0 — deletion happens exactly once.
func (c *imgCache) clearOverlayIntent() (deletedID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.overlay.visible {
		return 0
	}
	deletedID = c.overlay.shownID
	c.overlay.visible = false
	c.overlay.shownID = 0
	return deletedID
}

// overlayShownID reports the currently displayed image ID, or 0 when the
// terminal holds nothing of ours (used by the modal branch, which deletes
// without committing).
func (c *imgCache) overlayShownID() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.overlay.shownID
}

func (c *imgCache) forceKittyRedraw() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overlay.force = true
}

// hideOverlayForModal deletes the displayed image for a modal frame and
// reports the deletion bytes. Unlike clearOverlayIntent it keeps the slot
// (shown intent + ID) intact but marks the terminal as holding nothing:
// repeated modal frames keep deleting (deleting an unknown ID is a
// terminal no-op) while the unblocking frame retransmits without a
// redundant delete.
func (c *imgCache) hideOverlayForModal() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overlay.force = true
	id := c.overlay.shownID
	c.overlay.visible = false
	return deleteKittyImage(id)
}

func (c *imgCache) resetKittyOverlayState() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetKittyOverlayStateLocked()
}

func (c *imgCache) resetKittyOverlayStateLocked() {
	// nextID is deliberately preserved across resets: reusing an ID could
	// resurrect a deleted image the terminal still holds.
	nextID := c.overlay.nextID
	c.overlay = overlayState{force: true, nextID: nextID}
}

func (c *imgCache) kittyDisplayedURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return strings.TrimSpace(c.overlay.shown.url)
}

// deleteKittyImage emits a targeted delete for one displayed image ID.
// Deleting an ID the terminal does not know is a no-op, so repeating a
// delete (modal frames, reordered events) is safe.
func deleteKittyImage(id uint64) string {
	if id == 0 {
		return ""
	}
	return ansi.KittyGraphics(nil, fmt.Sprintf("a=d,d=i,i=%d", id))
}

func chunkBase64(encoded string, size int) []string {
	if size <= 0 {
		return nil
	}
	var parts []string
	for off := 0; off < len(encoded); off += size {
		end := min(off+size, len(encoded))
		parts = append(parts, encoded[off:end])
	}
	return parts
}

// encodeKittyChunks frames base64 PNG bytes for transmit-and-display using
// the kitty wire encoding from x/ansi: chunking at the protocol's
// MaxChunkSize, canonical first-chunk options, quiet mode throughout.
// A single chunk carries no m= flag (the library's canonical form).
func encodeKittyChunks(chunks []string, cols, rows int, imageID uint64) string {
	var sb strings.Builder
	for i, part := range chunks {
		last := i == len(chunks)-1
		var seq string
		if i == 0 {
			o := kitty.Options{
				Action:          kitty.TransmitAndPut,
				Format:          kitty.PNG,
				ID:              int(imageID),
				Columns:         cols,
				Rows:            rows,
				Quiet:           2,
				DoNotMoveCursor: true,
			}
			opts := o.Options()
			if !last {
				opts = append(opts, "m=1")
			}
			seq = ansi.KittyGraphics([]byte(part), opts...)
		} else {
			opts := []string{"q=2"}
			if last {
				opts = append(opts, "m=0")
			} else {
				opts = append(opts, "m=1")
			}
			seq = ansi.KittyGraphics([]byte(part), opts...)
		}
		sb.WriteString(seq)
	}
	return sb.String()
}

// buildKittyPayload frames one already-encoded cover for transmission. The
// expensive PNG encode happens once per URL at load time; this only slices
// the cached base64 and frames it, so it runs cheaply on every overlay
// emission (which itself happens only on intent transitions, never per
// tick) — no chunk cache needed.
func buildKittyPayload(encoded string, cols, rows int, imageID uint64) string {
	if encoded == "" || cols <= 0 || rows <= 0 {
		return ""
	}
	return encodeKittyChunks(chunkBase64(encoded, kitty.MaxChunkSize), cols, rows, imageID)
}
