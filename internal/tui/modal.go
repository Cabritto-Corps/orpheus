package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

const (
	modalLabelWidth = 20

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

// modalHeader composes the title row: title left, hint truncated into the
// remaining width and pushed right. The fits guarantee the joined header
// never exceeds innerW — Style.Width wraps, and a wrapped header pushed the
// body off the height budget (the old max() gap floors could overflow).
func modalHeader(title, hint string, innerW int) string {
	title = fitCell(title, innerW)
	if hint != "" {
		hint = fitCell(hint, max(0, innerW-lipgloss.Width(title)-2))
	}
	gap := max(0, innerW-lipgloss.Width(title)-lipgloss.Width(hint))
	return title + strings.Repeat(" ", gap) + hint
}

// popupModalSize is the single source for the track popup's box and list
// dimensions: open, resize and view must derive them here or any
// WindowSizeMsg permanently reshapes the popup (open and resize used to
// size the list differently, shrinking it 2 cols / 4 rows per resize).
func popupModalSize(termW, termH int) (modalW, listW, listH int) {
	bodyH := max(8, termH-headerH-2)
	modalW, boxH := modalGeometry(termW, termH, termW-4, bodyH)
	return modalW, max(12, modalW-modalContentInset), max(2, boxH-2)
}

// modalFrame renders a modal covering the full terminal frame: title row
// with a right-aligned (truncated) hint, a separator, the body clipped to
// the remaining height, all inside the shared box on the themed scrim.
// Everything below the header dims — modals own the whole frame.
func modalFrame(termW, termH int, title, hint, body string, wantedW, wantedH int) string {
	width, height := modalGeometry(termW, termH, wantedW, wantedH)
	innerW := max(8, width-modalContentInset)

	header := modalHeader(title, hint, innerW)
	sep := styleModalHint.Render(strings.Repeat("─", innerW))
	body = lipgloss.NewStyle().MaxHeight(max(2, height-2)).Render(body)

	box := styleModalBox.
		Width(width).
		Height(height).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, sep, body))
	// The box style paints its interior per line, but inner styled runs
	// (rows, hints, headers) end with a reset and the border ring is
	// drawn foreground-only — both would punch holes in the box tone.
	// Re-assert at line starts and after every reset.
	if seq := bgSequence(modalBoxBackground()); seq != "" {
		box = reassertBgLines(box, seq)
	}

	return lipgloss.Place(
		termW,
		termH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceForeground(colorScrim),
		lipgloss.WithWhitespaceBackground(colorPage),
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
		// Values right-align at the content edge: left-aligned values left a
		// dead band the width of the row inside full-size modals.
		row = marker + padCell(fitCell(label, labelW), labelW) + alignRight(fitCell(value, valueW), valueW)
	}
	if pad := inner - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	if selected && width >= 40 {
		rendered := styleModalSelectedRow.Render(row)
		// Fragments inside the row (gauges, swatches) end with a reset that
		// would let the box's background re-assertion split the highlight;
		// re-assert the selection bg after each so it wins inside the row,
		// then close the span with the box's own background so the line
		// never ends with the selection active — a line that ends on the
		// selection bg spills it onto whatever is drawn after it.
		return reassertBg(rendered, bgSequence(colorSelectionBg)) +
			bgSequence(modalBoxBackground())
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

// hintLine renders "key desc" pairs through bubbles/help so separators,
// ellipsis truncation at narrow widths and style handling follow the
// framework instead of being re-invented per surface.
func hintLine(bindings []key.Binding, width int) string {
	h := help.New()
	h.Width = width
	h.Styles.ShortKey = styleTrackPopupTitle
	h.Styles.ShortDesc = styleModalHint
	h.Styles.ShortSeparator = styleModalHint
	return h.ShortHelpView(bindings)
}

// withDesc copies a binding keeping its live key strings but speaking a
// context-specific description ("enter play" means "enter save" on the
// theme picker).
func withDesc(b key.Binding, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(b.Keys()...), key.WithHelp(b.Help().Key, desc))
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

// swatchBar renders the palette preview. On a selected row the
// background-role cells are left out: they would sit as dark notches in
// the highlight, and their colors are already on display as the row's own
// background.
func swatchBar(swatches []themeSwatch, selected bool) string {
	if lipgloss.DefaultRenderer().ColorProfile() == termenv.Ascii {
		return ""
	}
	var b strings.Builder
	first := true
	for _, sw := range swatches {
		if sw.asBackground && selected {
			continue
		}
		if !first {
			b.WriteString(" ")
		}
		first = false
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

// sortedConflictActions returns the conflicting actions in registry order
// so the rendered hints stop reshuffling on every frame.
func sortedConflictActions(conflicts map[string]bool) []string {
	out := make([]string, 0, len(conflicts))
	for _, entry := range settingsKeyActions {
		if conflicts[entry.action] {
			out = append(out, entry.action)
		}
	}
	return out
}

func keyListOverlap(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
