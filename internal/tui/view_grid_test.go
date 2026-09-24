package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestQueueRowNeverExceedsPaneWidth(t *testing.T) {
	long := strings.Repeat("abcdefghij", 8)
	for _, w := range []int{28, 30, 39, 40, 80} {
		grid := queueGridFor(w)
		for _, selected := range []bool{false, true} {
			row := grid.row(w, 1, long, long, 225000, selected)
			if got := lipgloss.Width(row); got > w {
				t.Fatalf("w=%d selected=%v: queue row width %d exceeds pane", w, selected, got)
			}
		}
	}
}
