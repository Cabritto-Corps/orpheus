package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const (
	modalLabelWidth = 16

	// modalContentInset is the box's horizontal padding: header, separator,
	// rows and the selected-row highlight all share this content width so
	// their right edges line up instead of ragged.
	modalContentInset = 2
)

// modalGeometry clamps a modal's inner dimensions to the placement budget:
// the framed box (content + 2 border rows/cols) must fit inside the
// terminal with a margin, because Style.Width wraps and Style.Height is
// only a minimum — nothing else bounds an oversized modal.
func modalGeometry(termW, termH, wantedW, wantedH int) (width, height int) {
	width = min(wantedW, max(16, termW-4))
	height = min(wantedH, max(4, termH-2))
	return width, height
}

// modalFrame renders a modal covering the full terminal frame: title row
// with a right-aligned (truncated) hint, a separator, the body clipped to
// the remaining height, all inside the shared box on the themed scrim.
// Everything below the header dims — modals own the whole frame.
func modalFrame(termW, termH int, title, hint, body string, wantedW, wantedH int) string {
	width, height := modalGeometry(termW, termH, wantedW, wantedH)
	innerW := max(8, width-modalContentInset)

	if hint != "" {
		hint = fitCell(hint, max(8, innerW-lipgloss.Width(fitCell(title, innerW))-2))
	}
	title = fitCell(title, max(8, innerW-lipgloss.Width(hint)))

	gap := max(2, innerW-lipgloss.Width(title)-lipgloss.Width(hint))
	header := title + strings.Repeat(" ", gap) + hint
	sep := styleModalHint.Render(strings.Repeat("─", innerW))
	body = lipgloss.NewStyle().MaxHeight(max(2, height-2)).Render(body)

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
		lipgloss.WithWhitespaceForeground(colorScrim),
	)
}

// modalRow renders one settings/menu row: a fixed marker gutter on every
// row (so selection never shifts content), label in a fixed column with the
// value left-aligned after it (right-alignment left a dead band inside
// full-width modals), all truncated/padded to the shared content width, then
// exactly one style over the whole row. An empty value lets the label span
// the row (picker/list entries). Below a minimum width the ">" marker
// replaces the highlight (small-terminal fallback).
func modalRow(label, value string, selected bool, width int) string {
	inner := max(12, width-modalContentInset)

	marker := " "
	if selected && width < 40 {
		marker = ">"
	}
	var row string
	if value == "" {
		row = marker + padCell(fitCell(label, inner-1), inner-1)
	} else {
		labelW := min(modalLabelWidth, max(1, (inner-1)/2))
		valueW := inner - 1 - labelW
		row = marker + padCell(fitCell(label, labelW), labelW) + padCell(fitCell(value, valueW), valueW)
	}
	if pad := inner - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	if selected && width >= 40 {
		return styleModalSelectedRow.Render(row)
	}
	return row
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
	filled := min(int(float64(width)*frac), width)
	return styleVolumeBarFilled.Render(strings.Repeat("█", filled)) +
		styleVolumeBarEmpty.Render(strings.Repeat("░", width-filled))
}

// themeSwatch is one preview cell: either a background-filled pair of spaces
// or a foreground-colored full block.
type themeSwatch struct {
	color        lipgloss.Color
	asBackground bool
}

// themeSwatches picks the seven representative roles of a palette:
// scrim and selection as background cells, text/dim/accent roles as
// foreground blocks.
func themeSwatches(c themeColors) []themeSwatch {
	return []themeSwatch{
		{lipgloss.Color(c.Scrim), true},
		{lipgloss.Color(c.SelectionBg), true},
		{lipgloss.Color(c.OffWhite), false},
		{lipgloss.Color(c.Gray), false},
		{lipgloss.Color(c.Blue), false},
		{lipgloss.Color(c.BlueLight), false},
		{lipgloss.Color(c.Error), false},
	}
}

func swatchBar(swatches []themeSwatch) string {
	if lipgloss.DefaultRenderer().ColorProfile() == termenv.Ascii {
		return ""
	}
	var b strings.Builder
	for i, sw := range swatches {
		if i > 0 {
			b.WriteString(" ")
		}
		if sw.asBackground {
			b.WriteString(lipgloss.NewStyle().Background(sw.color).Render("  "))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(sw.color).Render("██"))
		}
	}
	return b.String()
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
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
