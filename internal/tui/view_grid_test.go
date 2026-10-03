package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestQueueRowCursorMarkerAtAllWidths(t *testing.T) {
	// Selected rows need a non-color marker; unselected keep the lead space.
	st := buildThemeStyles(themePresetState("default"))
	for _, w := range []int{28, 39, 40, 80} {
		grid := queueGridFor(w)
		sel := grid.row(st, w, 1, "Title", "Artist", 225000, true)
		plain := grid.row(st, w, 1, "Title", "Artist", 225000, false)
		if lipgloss.Width(sel) != lipgloss.Width(plain) {
			t.Fatalf("w=%d: marker must not change row width", w)
		}
		if !strings.Contains(sel, ">") {
			t.Fatalf("w=%d: selected row carries no marker: %q", w, sel)
		}
		if strings.HasPrefix(plain, ">") {
			t.Fatalf("w=%d: unselected row must not carry a marker: %q", w, plain)
		}
	}
}

func TestQueueRowNeverExceedsPaneWidth(t *testing.T) {
	st := buildThemeStyles(themePresetState("default"))
	long := strings.Repeat("abcdefghij", 8)
	for _, w := range []int{28, 30, 39, 40, 80} {
		grid := queueGridFor(w)
		for _, selected := range []bool{false, true} {
			row := grid.row(st, w, 1, long, long, 225000, selected)
			if got := lipgloss.Width(row); got > w {
				t.Fatalf("w=%d selected=%v: queue row width %d exceeds pane", w, selected, got)
			}
		}
	}
}

// Hour-plus durations overflowed the shrunk duration column and the panel
// clipped the queue bottom; the cell truncates like every other.
func TestQueueRowLongDurationsFitPaneWidth(t *testing.T) {
	st := buildThemeStyles(themePresetState("default"))
	long := strings.Repeat("abcdefghij", 8)
	for _, w := range []int{28, 30, 39, 40, 50} {
		for _, durMS := range []int{3721000, 360000000} {
			grid := queueGridFor(w)
			for _, selected := range []bool{false, true} {
				row := grid.row(st, w, 1, long, long, durMS, selected)
				if got := lipgloss.Width(row); got != w {
					t.Errorf("w=%d dur=%d selected=%v: row width %d, want %d", w, durMS, selected, got, w)
				}
				if strings.Count(row, "\n") != 0 {
					t.Errorf("w=%d dur=%d selected=%v: row spans lines", w, durMS, selected)
				}
			}
		}
	}
}

func TestQueueRowCursorGlyphOptions(t *testing.T) {
	// plain is the accepted color-only trade (no lead mark); no option may
	// shift row width.
	want := map[string]string{
		"arrow": ">",
		"note":  "♪",
		"dot":   "●",
		"play":  "▶",
		"plain": "",
	}
	for _, glyph := range glyphQueueCursorChoices {
		mark, ok := want[glyph]
		if !ok {
			t.Fatalf("untested cursor glyph %q: extend the pin", glyph)
		}
		base := themePresetState("default")
		base.glyphs.QueueCursor = glyph
		st := buildThemeStyles(base)
		for _, w := range []int{39, 40, 80} {
			grid := queueGridFor(w)
			sel := ansi.Strip(grid.row(st, w, 1, "Title", "Artist", 225000, true))
			plain := ansi.Strip(grid.row(st, w, 1, "Title", "Artist", 225000, false))
			if lipgloss.Width(sel) != lipgloss.Width(plain) {
				t.Fatalf("glyph %q w=%d: cursor must not change row width", glyph, w)
			}
			if mark == "" {
				if strings.HasPrefix(sel, ">") || strings.Contains(sel, "♪●▶") {
					t.Fatalf("glyph %q w=%d: plain must carry no lead mark: %q", glyph, w, sel)
				}
				continue
			}
			if !strings.HasPrefix(sel, mark) {
				t.Fatalf("glyph %q w=%d: selected row must lead with %q: %q", glyph, w, mark, sel)
			}
		}
	}
}
