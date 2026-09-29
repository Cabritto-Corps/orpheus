package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

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

// Hour-plus durations ("1:02:01") overflow the shrunk duration column: the
// row wrapped and the panel clipped the queue bottom. The duration cell is
// truncated to its column like every other cell.
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
