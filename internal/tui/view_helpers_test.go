package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncateBoundaries(t *testing.T) {
	if got := truncate("abcdef", 0); got != "" {
		t.Fatalf("expected empty output for non-positive width, got %q", got)
	}
	if got := truncate("abcdef", 1); got != "…" {
		t.Fatalf("expected ellipsis for max=1, got %q", got)
	}
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("expected ascii truncation with ellipsis, got %q", got)
	}
}

func TestTruncateWideRunesRespectsDisplayWidth(t *testing.T) {
	got := truncate("你你你", 5)
	if got != "你你…" {
		t.Fatalf("expected wide-rune truncation to keep full runes, got %q", got)
	}
	if w := lipgloss.Width(got); w > 5 {
		t.Fatalf("expected truncated string width <= 5, got %d for %q", w, got)
	}
}
