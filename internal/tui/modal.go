package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	modalLabelWidth = 16
	modalValueWidth = 22
)

// modalFrame renders a modal: title row with a right-aligned hint, a
// separator, the body, and the shared box + centered placement on a dim
// backdrop. termW/termH are the outer placement dimensions.
func modalFrame(termW, termH int, title, hint, body string, width, height int) string {
	header := title
	if hint != "" {
		hintGap := max(2, width-lipgloss.Width(title)-lipgloss.Width(hint)-4)
		header = title + strings.Repeat(" ", hintGap) + hint
	}
	sep := styleModalHint.Render(strings.Repeat("─", max(4, width-4)))

	box := styleModalBox.
		Width(width).
		Height(height).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, sep, body))

	return lipgloss.Place(
		termW,
		termH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("#1a1a2a")),
	)
}

// modalRow renders one settings/menu row: label in a fixed column, value
// right-aligned in its own fixed column, highlight on the selected row.
// Below a minimum width the classic "> " marker is used instead of the
// highlight (small-terminal fallback).
func modalRow(label, value string, selected bool, width int) string {
	row := padTo(label, modalLabelWidth) + alignRight(value, modalValueWidth)
	if selected {
		if width >= 40 {
			return styleModalSelectedRow.Render(strings.TrimRight(row, " "))
		}
		return "> " + row
	}
	return "  " + row
}

// alignRight left-pads s with spaces so it occupies width cells.
func alignRight(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad < 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// miniGauge renders a bar of filled/empty blocks proportional to frac, in
// the same visual language as the header volume bar.
func miniGauge(frac float64, width int) string {
	frac = max(0, min(1, frac))
	filled := int(float64(width) * frac)
	if filled > width {
		filled = width
	}
	return styleVolumeBarFilled.Render(strings.Repeat("█", filled)) +
		styleVolumeBarEmpty.Render(strings.Repeat("░", width-filled))
}

// swatchBar renders adjacent color squares representing a palette.
func swatchBar(colors []lipgloss.Color) string {
	var b strings.Builder
	for _, c := range colors {
		b.WriteString(lipgloss.NewStyle().Background(c).Render("  "))
	}
	return b.String()
}

// themeSwatches picks the four representative colors of a palette: accent,
// foreground, dim, error.
func themeSwatches(c themeColors) []lipgloss.Color {
	return []lipgloss.Color{
		lipgloss.Color(c.Blue),
		lipgloss.Color(c.OffWhite),
		lipgloss.Color(c.Gray),
		lipgloss.Color(c.Error),
	}
}

// keyConflictActions returns the set of actions whose effective key lists
// collide with another action's. ctrl+c is exempt everywhere (quit's
// guaranteed key), and enter/return are exempt too: they are contextual
// keys shared across panels by design (select vs queue jump).
func keyConflictActions(k keyMap) map[string]bool {
	keysOf := make(map[string][]string, len(settingsKeyActions))
	for _, entry := range settingsKeyActions {
		keys, ok := defaultKeysForAction(k, entry.action)
		if !ok {
			continue
		}
		filtered := make([]string, 0, len(keys))
		for _, kk := range keys {
			if kk == "ctrl+c" || kk == "enter" || kk == "return" {
				continue
			}
			filtered = append(filtered, kk)
		}
		if len(filtered) > 0 {
			keysOf[entry.action] = filtered
		}
	}

	conflicts := make(map[string]bool)
	for a, aKeys := range keysOf {
		for b, bKeys := range keysOf {
			if a >= b {
				continue
			}
			if keyListOverlap(aKeys, bKeys) {
				conflicts[a] = true
				conflicts[b] = true
			}
		}
	}
	return conflicts
}

func keyListOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
