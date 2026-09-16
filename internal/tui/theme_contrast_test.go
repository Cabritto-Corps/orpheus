package tui

import (
	"fmt"
	"math"
	"testing"
)

func relativeLuminance(hex string) (float64, bool) {
	var r, g, b uint32
	n, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	if err != nil || n != 3 {
		return 0, false
	}
	lin := func(v uint32) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b), true
}

func contrastRatio(a, b float64) float64 {
	lighter, darker := a, b
	if a < b {
		lighter, darker = b, a
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func TestThemeSelectionContrast(t *testing.T) {
	for _, entry := range themeRegistry {
		name, c := entry.name, entry.colors
		l1, ok1 := relativeLuminance(c.SelectionFg)
		l2, ok2 := relativeLuminance(c.SelectionBg)
		if !ok1 || !ok2 {
			continue // ANSI-name presets (minimal) are inverted by design
		}
		ratio := contrastRatio(l1, l2)
		if ratio < 4.5 {
			t.Errorf("theme %s: selection contrast %.2f:1 (< 4.5), fg=%s bg=%s", name, ratio, c.SelectionFg, c.SelectionBg)
		}
	}
}
