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
