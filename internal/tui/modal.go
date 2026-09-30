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

	// 1-cell padding per side plus the 1-cell ring. v2 Width is the TOTAL
	// block width (content wraps at width - padding - border), so the v1
	// value of 2 (border only) no longer suffices.
	modalContentInset = 4
)

// Style.Width wraps and Style.Height is only a minimum, so the box
// must be bounded here — nothing else bounds an oversized modal.
func modalGeometry(termW, termH, wantedW, wantedH int) (width, height int) {
	width = min(wantedW, max(16, termW-4))
	height = min(wantedH, max(4, termH-2))
	return width, height
}

// The fits guarantee innerW: Style.Width wraps, and a wrapped header
// pushes the body off the height budget.
func modalHeader(title, hint string, innerW int) string {
	title = fitCell(title, innerW)
	if hint != "" {
		hint = fitCell(hint, max(0, innerW-lipgloss.Width(title)-2))
	}
	gap := max(0, innerW-lipgloss.Width(title)-lipgloss.Width(hint))
	return title + strings.Repeat(" ", gap) + hint
}

// Single geometry engine: the clamp and the content-width rule
// cannot drift apart.
func modalRect(termW, termH, wantedW, wantedH int) (modalW, boxH, contentW int) {
	modalW, boxH = modalGeometry(termW, termH, wantedW, wantedH)
	contentW = max(12, modalW-modalContentInset)
	return modalW, boxH, contentW
}

// The single windowing implementation (theme picker, theme options,
// up-next all shared the same offset math). Rows render byte-identically;
// only the arithmetic is shared.
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

// Open, resize and view must all derive here: they once sized
// differently and every resize shrank the popup.
func popupModalSize(termW, termH int) (modalW, listW, listH int) {
	bodyH := max(8, termH-headerH-2)
	var boxH int
	modalW, boxH, listW = modalRect(termW, termH, termW-4, bodyH)
	return modalW, listW, max(2, boxH-2)
}

// Modals own the whole frame: box on a full-frame scrim.
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
	// Inner resets and the foreground-only border ring would punch
	// holes in the box tone: re-assert at line starts and after resets.
	if seq := s.bgSequence(s.modalBoxBackground()); seq != "" {
		box = reassertBgLines(box, seq)
	}

	// Transparent mode keeps no whitespace background: painting the page
	// would reclaim the whole frame.
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

// Fixed marker gutter (selection never shifts content), one style
// over the whole row; an empty value lets the label span it. Below the
// minimum width the ">" marker replaces the highlight.
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
		// Right-aligned: left-aligned values left a dead band the width
		// of the row inside full-size modals.
		row = marker + padCell(fitCell(label, labelW), labelW) + alignRight(fitCell(value, valueW), valueW)
	}
	if pad := inner - lipgloss.Width(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	if selected && width >= 40 {
		rendered := s.styleModalSelectedRow.Render(row)
		// Re-assert the selection bg so it wins over fragments' resets,
		// then close with the box bg — a line ending on the selection bg
		// spills it onto whatever is drawn after it.
		return reassertBg(rendered, s.bgSequence(s.colorSelectionBg)) +
			s.bgSequence(s.modalBoxBackground())
	}
	return row
}

func alignRight(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad < 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// Routed through bubbles/help so the framework owns separators,
// truncation and style.
func (s *themeStyles) hintLine(bindings []key.Binding, width int) string {
	h := s.help
	h.SetWidth(width)
	h.Styles.ShortKey = s.styleTrackPopupTitle
	h.Styles.ShortDesc = s.styleModalHint
	h.Styles.ShortSeparator = s.styleModalHint
	return h.ShortHelpView(bindings)
}

// Keeps the live key strings, re-speaks the description for the
// context ("enter play" vs "enter save").
func withDesc(b key.Binding, desc string) key.Binding {
	return key.NewBinding(key.WithKeys(b.Keys()...), key.WithHelp(b.Help().Key, desc))
}

type themeSwatch struct {
	color color.Color
}

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

// Identical on every row: the bar must never reflow when the cursor
// moves over it.
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

// ctrl+c (guaranteed quit) and enter/return (contextual, shared by
// design) are exempt.
func keyConflictActions(k keyMap) map[string]bool {
	keysOf := make(map[string][]string, len(settingsKeyActions))
	for _, entry := range settingsKeyActions {
		keys, ok := defaultKeysForAction(k, entry.action)
		if !ok {
			continue
		}
		filtered := make([]string, 0, len(keys))
		for _, kk := range keys {
			// Aliases fold: "escape"/"return" skip exactly like
			// "esc"/"enter".
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

// Registry order, so hints never reshuffle between frames.
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
	// Canonical identities, so alias spellings still collide.
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
