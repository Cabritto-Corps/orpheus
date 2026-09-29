package tui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

const (
	modalLabelWidth = 20

	// modalContentInset is the box chrome every content width budgets
	// for: 1-cell padding per side plus the 1-cell border ring. Rows,
	// hints and tables built to (width - inset) fit the box content
	// exactly. lipgloss v2 Width is the TOTAL block width (content wraps
	// at width - padding - border); v1 treated it as the content width,
	// so this was 2 (border only) before the v2 migration.
	modalContentInset = 4
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

// modalRect is the single geometry engine behind the per-modal sizing
// helpers: clamp the wanted box (modalGeometry), then derive the usable
// content width. popupModalSize and helpModalSize keep their signatures
// (open/resize/view call sites stay put) and delegate here, so the clamp
// and the content-width rule cannot drift apart again.
func modalRect(termW, termH, wantedW, wantedH int) (modalW, boxH, contentW int) {
	modalW, boxH = modalGeometry(termW, termH, wantedW, wantedH)
	contentW = max(12, modalW-modalContentInset)
	return modalW, boxH, contentW
}

// scrollRows returns the visible window of rows around cursor with at most
// budget entries, plus the window's start index for "+ more" accounting.
// It is the single windowing implementation for menu rows: the theme
// picker, the theme options and the up-next panel all computed the same
// offset before (cursor-budget+1 once the cursor runs past the budget,
// else 0). Rows render byte-identically; only the arithmetic is shared.
func scrollRows[T any](rows []T, cursor, budget int) (window []T, start int) {
	if budget <= 0 || len(rows) == 0 {
		return nil, 0
	}
	cursor = min(max(cursor, 0), len(rows)-1)
	if cursor >= budget {
		start = cursor - budget + 1
	}
	end := min(start+budget, len(rows))
	return rows[start:end], start
}

// popupModalSize is the single source for the track popup's box and list
// dimensions: open, resize and view must derive them here or any
// WindowSizeMsg permanently reshapes the popup (open and resize used to
// size the list differently, shrinking it 2 cols / 4 rows per resize).
func popupModalSize(termW, termH int) (modalW, listW, listH int) {
	bodyH := max(8, termH-headerH-2)
	var boxH int
	modalW, boxH, listW = modalRect(termW, termH, termW-4, bodyH)
	return modalW, listW, max(2, boxH-2)
}

// modalFrame renders a modal covering the full terminal frame: title row
// with a right-aligned (truncated) hint, a separator, the body clipped to
// the remaining height, all inside the shared box on the themed scrim.
// Everything below the header dims — modals own the whole frame.
func (s *themeStyles) modalFrame(termW, termH int, title, hint, body string, wantedW, wantedH int) string {
	width, height := modalGeometry(termW, termH, wantedW, wantedH)
	innerW := max(8, width-modalContentInset)

	header := modalHeader(title, hint, innerW)
	sep := s.styleModalHint.Render(strings.Repeat("─", innerW))
	body = lipgloss.NewStyle().MaxHeight(max(2, height-2)).Render(body)

	box := s.styleModalBox.
		Width(width).
		Height(height).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, sep, body))
	// The box style paints its interior per line, but inner styled runs
	// (rows, hints, headers) end with a reset and the border ring is
	// drawn foreground-only — both would punch holes in the box tone.
	// Re-assert at line starts and after every reset.
	if seq := s.bgSequence(s.modalBoxBackground()); seq != "" {
		box = reassertBgLines(box, seq)
	}

	// The ░ scrim dims the frame behind the box. In transparent mode the
	// whitespace keeps no background: the terminal owns the backdrop and
	// painting page would reclaim the whole frame.
	wsBg := color.Color(s.colorPage)
	if s.transparentFrame() {
		wsBg = lipgloss.NoColor{}
	}
	return lipgloss.Place(
		termW,
		termH,
		lipgloss.Center,
		lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars("░"),
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Foreground(s.colorScrim).Background(wsBg)),
	)
}

// modalRow renders one settings/menu row: a fixed marker gutter on every
// row (so selection never shifts content), label in a fixed column with the
// value left-aligned after it (right-alignment left a dead band inside
// full-width modals), all truncated/padded to the shared content width, then
// exactly one style over the whole row. An empty value lets the label span
// the row (picker/list entries). Below a minimum width the ">" marker
// replaces the highlight (small-terminal fallback).
func (s *themeStyles) modalRow(label, value string, selected bool, width int) string {
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
		rendered := s.styleModalSelectedRow.Render(row)
		// Fragments inside the row (gauges, swatches) end with a reset that
		// would let the box's background re-assertion split the highlight;
		// re-assert the selection bg after each so it wins inside the row,
		// then close the span with the box's own background so the line
		// never ends with the selection active — a line that ends on the
		// selection bg spills it onto whatever is drawn after it.
		return reassertBg(rendered, s.bgSequence(s.colorSelectionBg)) +
			s.bgSequence(s.modalBoxBackground())
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
func (s *themeStyles) hintLine(bindings []key.Binding, width int) string {
	h := s.help
	h.SetWidth(width)
	h.Styles.ShortKey = s.styleTrackPopupTitle
	h.Styles.ShortDesc = s.styleModalHint
	h.Styles.ShortSeparator = s.styleModalHint
	return h.ShortHelpView(bindings)
}

// withDesc copies a binding keeping its live key strings but speaking a
// context-specific description ("enter play" means "enter save" on the
// theme picker).
func withDesc(b key.Binding, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(b.Keys()...), key.WithHelp(b.Help().Key, desc))
}

// themeSwatch is one preview cell of the palette bar.
type themeSwatch struct {
	color color.Color
}

// themeSwatches picks the seven representative roles of a palette.
func themeSwatches(c themeColors) []themeSwatch {
	return []themeSwatch{
		{lipgloss.Color(c.Scrim)},
		{lipgloss.Color(c.SelectionBg)},
		{lipgloss.Color(c.OffWhite)},
		{lipgloss.Color(c.Gray)},
		{lipgloss.Color(c.Blue)},
		{lipgloss.Color(c.BlueLight)},
		{lipgloss.Color(c.Error)},
	}
}

// swatchBar renders the palette preview: every role as a foreground full
// block, identical on every row — the bar must never reflow when the
// cursor moves over it.
func (s *themeStyles) swatchBar(swatches []themeSwatch) string {
	if s.colorProfile <= colorprofile.Ascii {
		return ""
	}
	var b strings.Builder
	for i, sw := range swatches {
		if i > 0 {
			b.WriteString(" ")
		}
		st, ok := s.swatchStyles[sw.color]
		if !ok {
			st = lipgloss.NewStyle().Foreground(sw.color)
			if s.swatchStyles == nil {
				s.swatchStyles = map[color.Color]lipgloss.Style{}
			}
			s.swatchStyles[sw.color] = st
		}
		b.WriteString(st.Render("██"))
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
			// Canonical aliases fold here too, so "escape"/"return"
			// skip the scan exactly like "esc"/"enter" do.
			switch canonicalKeySpec(kk) {
			case "ctrl+c", "enter", "esc":
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
	// Compare canonical identities so alias spellings ("esc" vs
	// "escape") still collide.
	for _, x := range a {
		cx := canonicalKeySpec(x)
		for _, y := range b {
			if cx == canonicalKeySpec(y) {
				return true
			}
		}
	}
	return false
}
