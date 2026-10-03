package tui

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"orpheus/internal/spotify"
)

// pumpListKeys feeds keypresses to a bubbles list the way the app does,
// executing returned commands (notably the async filterItems matcher) and
// feeding resulting messages back until quiescent.
func pumpListKeys(t *testing.T, l list.Model, keys ...tea.KeyPressMsg) list.Model {
	t.Helper()
	for _, k := range keys {
		var cmd tea.Cmd
		l, cmd = l.Update(k)
		l = drainListCmd(t, l, cmd)
	}
	return l
}

// drainListCmd feeds a list Update's resulting filter match back into the
// list. Only FilterMatchesMsg is followed: tick/debounce/spinner commands
// would block for their durations and belong to the app layer, not the
// list's filter mechanics under test.
func drainListCmd(t *testing.T, l list.Model, cmd tea.Cmd) list.Model {
	t.Helper()
	if cmd == nil {
		return l
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			l = drainListCmd(t, l, sub)
		}
		return l
	}
	if _, ok := msg.(list.FilterMatchesMsg); ok {
		var nextCmd tea.Cmd
		l, nextCmd = l.Update(msg)
		return drainListCmd(t, l, nextCmd)
	}
	return l
}

func filterTestItems() []list.Item {
	return []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "alpha one"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "alpha two"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "3", Name: "alpha three"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "4", Name: "beta one"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "5", Name: "gamma"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "6", Name: "delta"}},
	}
}

// markedSelectedRow scans a rendered list view for the selected-row marker
// (the left border gutter) and returns the title lines that carry it.
func markedTitleLines(t *testing.T, view string) []string {
	t.Helper()
	var out []string
	for line := range strings.SplitSeq(ansi.Strip(view), "\n") {
		if strings.HasPrefix(line, "│") {
			out = append(out, line)
		}
	}
	return out
}

// After filter-accept, moving down into the results must mark exactly the
// cursor row — the first result must not render unmarked while selected.
//
// OBSERVATION (Bug B harness below): geometry snapshots across filter exit.
func TestFilterAcceptDownMarksCursorRow(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems(filterTestItems())
	m.browse.playlistList.Select(3)

	l := pumpListKeys(t, m.browse.playlistList, pressRune('/'))
	if l.FilterState() != list.Filtering {
		t.Fatalf("expected Filtering state, got %v", l.FilterState())
	}
	for _, r := range "alpha" {
		l = pumpListKeys(t, l, pressRune(r))
	}
	if got := len(l.VisibleItems()); got != 3 {
		t.Fatalf("expected 3 filtered items, got %d", got)
	}
	l = pumpListKeys(t, l, tea.KeyPressMsg{Code: tea.KeyEnter})
	if l.FilterState() != list.FilterApplied {
		t.Fatalf("expected FilterApplied state, got %v", l.FilterState())
	}
	l = pumpListKeys(t, l, tea.KeyPressMsg{Code: tea.KeyDown})

	idx := l.Index()
	sel, ok := l.VisibleItems()[idx].(playlistItem)
	if !ok {
		t.Fatalf("cursor %d out of %d visible items", idx, len(l.VisibleItems()))
	}
	marks := markedTitleLines(t, l.View())
	if len(marks) != 2 {
		t.Fatalf("expected exactly the 2 selected-row lines marked, got %d: %q", len(marks), marks)
	}
	if !strings.Contains(marks[0], sel.summary.Name) {
		t.Fatalf("marked row %q is not the cursor row %q", marks[0], sel.summary.Name)
	}
}

func filterGeomModel(t *testing.T, protocol imageProtocol) model {
	t.Helper()
	m := NewLoaderModel()
	m.ui.width = 100
	m.ui.height = 40
	m.ui.activeTab = tabPlaylists
	items := []list.Item{
		playlistItem{summary: spotify.PlaylistSummary{ID: "1", Name: "alpha one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "2", Name: "alpha two", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "3", Name: "alpha three", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "4", Name: "beta one", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "5", Name: "gamma", ImageURL: "u1"}},
		playlistItem{summary: spotify.PlaylistSummary{ID: "6", Name: "delta", ImageURL: "u1"}},
	}
	m.browse.playlistList.SetItems(items)
	m.browse.playlistList.Select(0)
	m.browse.playlistList.SetSize(60, 30)
	m.ui.imgs.protocol = protocol
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.NRGBA{R: 200, G: 30, B: 90, A: 255})
		}
	}
	m.ui.imgs.setImage("u1", img, 0, 0)
	return m
}

func previewGeometry(m model) (panel string, overlay string) {
	layout := m.bodyLayout()
	panel = m.coverPreviewPanel(layout.leftW-1, layout.bodyH, layout.coverCols, layout.coverRows)
	overlay, _ = m.kittyOverlayBytes()
	return panel, overlay
}

// Filter exit must not move the art: same cover URL throughout, so the
// preview panel must be byte-identical and the overlay placement must
// stay at the same cell (re-place churn across calls is by design; drift
// is not).
func TestFilterExitArtGeometryStable(t *testing.T) {
	for _, protocol := range []imageProtocol{imageProtocolNone, imageProtocolKitty} {
		m := filterGeomModel(t, protocol)
		before, beforeOverlay := previewGeometry(m)
		l := pumpListKeys(t, m.browse.playlistList, pressRune('/'))
		for _, r := range "alpha" {
			l = pumpListKeys(t, l, pressRune(r))
		}
		l = pumpListKeys(t, l, tea.KeyPressMsg{Code: tea.KeyEscape})
		m.browse.playlistList = l
		after, afterOverlay := previewGeometry(m)
		if before != after {
			bo, ao := strings.Split(before, "\n"), strings.Split(after, "\n")
			t.Fatalf("protocol=%v: preview panel changed across filter exit (%d -> %d lines)", protocol, len(bo), len(ao))
		}
		// Unchanged intent emits nothing (staying silent keeps the placed
		// image live); any emission that still lands must anchor identically.
		if afterOverlay != "" && overlayCursor(beforeOverlay) != overlayCursor(afterOverlay) {
			t.Fatalf("protocol=%v: overlay placement moved across filter exit: %q -> %q", protocol, beforeOverlay, afterOverlay)
		}
	}
}

// overlayCursor extracts the leading cursor-positioning of an overlay
// emission (the cell the terminal places the image at).
func overlayCursor(emit string) string {
	if i := strings.Index(emit, "H"); i > 0 && strings.HasPrefix(emit, "\x1b[") {
		return emit[:i+1]
	}
	return emit
}

func TestFilterTypingMarksCursorRow(t *testing.T) {
	m := NewLoaderModel()
	m.browse.playlistList.SetItems(filterTestItems())
	m.browse.playlistList.Select(3)

	l := pumpListKeys(t, m.browse.playlistList, pressRune('/'))
	for _, r := range "alpha" {
		l = pumpListKeys(t, l, pressRune(r))
	}
	if l.FilterState() != list.Filtering {
		t.Fatalf("expected Filtering state, got %v", l.FilterState())
	}
	if got := l.Index(); got != 0 {
		t.Fatalf("expected cursor reset to first result, got %d", got)
	}
	marks := markedTitleLines(t, l.View())
	if len(marks) != 2 {
		t.Fatalf("expected exactly the 2 cursor-row lines marked while typing, got %d: %q", len(marks), marks)
	}
	if !strings.Contains(marks[0], "alpha one") {
		t.Fatalf("marked row %q is not the first result", marks[0])
	}
}
