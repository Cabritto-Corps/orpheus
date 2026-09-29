package tui

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type keyMap struct {
	Tab           key.Binding
	PlayPause     key.Binding
	Next          key.Binding
	Prev          key.Binding
	Shuffle       key.Binding
	Loop          key.Binding
	VolUp         key.Binding
	VolDown       key.Binding
	SeekBack      key.Binding
	SeekFwd       key.Binding
	Refresh       key.Binding
	Filter        key.Binding
	ToggleHelp    key.Binding
	Select        key.Binding
	CloseModal    key.Binding
	Quit          key.Binding
	Settings      key.Binding
	QueueUp       key.Binding
	QueueDown     key.Binding
	QueueJump     key.Binding
	QueueRemove   key.Binding
	QueueMoveUp   key.Binding
	QueueMoveDown key.Binding
}

func newKeys() keyMap {
	return keyMap{
		Tab:           key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch tab")),
		PlayPause:     key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "play/pause")),
		Next:          key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next")),
		Prev:          key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "prev")),
		Shuffle:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shuffle")),
		Loop:          key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "repeat")),
		VolUp:         key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "vol+")),
		VolDown:       key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "vol-")),
		SeekBack:      key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "-5s")),
		SeekFwd:       key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "+5s")),
		Refresh:       key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Filter:        key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		ToggleHelp:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Select:        key.NewBinding(key.WithKeys("enter", "return"), key.WithHelp("enter", "play")),
		CloseModal:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
		Quit:          key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		QueueUp:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "queue up")),
		QueueDown:     key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "queue down")),
		QueueJump:     key.NewBinding(key.WithKeys("enter", "return"), key.WithHelp("enter", "play from queue")),
		QueueRemove:   key.NewBinding(key.WithKeys("x", "d"), key.WithHelp("x", "remove from queue")),
		QueueMoveUp:   key.NewBinding(key.WithKeys("["), key.WithHelp("[", "move queue entry up")),
		QueueMoveDown: key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "move queue entry down")),
		Settings:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "settings")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Tab, k.Select, k.PlayPause, k.Next, k.Prev, k.Shuffle, k.Loop, k.Filter, k.ToggleHelp, k.Settings, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Tab, k.Select, k.CloseModal, k.Refresh, k.Filter},
		{k.PlayPause, k.Next, k.Prev, k.Shuffle, k.Loop, k.VolUp, k.VolDown},
		{k.QueueUp, k.QueueDown, k.QueueJump, k.QueueRemove, k.QueueMoveUp, k.QueueMoveDown},
		{k.SeekBack, k.SeekFwd, k.ToggleHelp, k.Settings, k.Quit},
	}
}

// actionRegistry is the single ordered source for every bindable action:
// keys.json validity, the settings keys menu, the key-conflict scan and the
// help modal groups all derive from it, so the four copies that could drift
// are gone. Order defines the help modal's group order and the settings
// menu row order.
type actionMeta struct {
	action string
	group  string
	label  string
	desc   string
	bind   func(keyMap) key.Binding
	// set applies a keys.json override to the matching field, so the
	// registry — not a parallel switch — decides which actions are rebindable.
	set func(*keyMap, []string)
}

var actionRegistry = []actionMeta{
	{"play_pause", "Playback", "play/pause", "play/pause", func(k keyMap) key.Binding { return k.PlayPause }, func(m *keyMap, keys []string) { m.PlayPause = overrideBinding(m.PlayPause, keys) }},
	{"next", "Playback", "next track", "next track", func(k keyMap) key.Binding { return k.Next }, func(m *keyMap, keys []string) { m.Next = overrideBinding(m.Next, keys) }},
	{"prev", "Playback", "previous track", "previous track", func(k keyMap) key.Binding { return k.Prev }, func(m *keyMap, keys []string) { m.Prev = overrideBinding(m.Prev, keys) }},
	{"shuffle", "Playback", "shuffle", "shuffle", func(k keyMap) key.Binding { return k.Shuffle }, func(m *keyMap, keys []string) { m.Shuffle = overrideBinding(m.Shuffle, keys) }},
	{"loop", "Playback", "repeat", "repeat", func(k keyMap) key.Binding { return k.Loop }, func(m *keyMap, keys []string) { m.Loop = overrideBinding(m.Loop, keys) }},
	{"vol_up", "Playback", "volume up", "volume up", func(k keyMap) key.Binding { return k.VolUp }, func(m *keyMap, keys []string) { m.VolUp = overrideBinding(m.VolUp, keys) }},
	{"vol_down", "Playback", "volume down", "volume down", func(k keyMap) key.Binding { return k.VolDown }, func(m *keyMap, keys []string) { m.VolDown = overrideBinding(m.VolDown, keys) }},
	{"seek_back", "Playback", "seek back", "seek back", func(k keyMap) key.Binding { return k.SeekBack }, func(m *keyMap, keys []string) { m.SeekBack = overrideBinding(m.SeekBack, keys) }},
	{"seek_fwd", "Playback", "seek forward", "seek forward", func(k keyMap) key.Binding { return k.SeekFwd }, func(m *keyMap, keys []string) { m.SeekFwd = overrideBinding(m.SeekFwd, keys) }},
	{"tab", "Navigation", "switch tab", "switch tab", func(k keyMap) key.Binding { return k.Tab }, func(m *keyMap, keys []string) { m.Tab = overrideBinding(m.Tab, keys) }},
	{"refresh", "Navigation", "refresh library", "refresh library", func(k keyMap) key.Binding { return k.Refresh }, func(m *keyMap, keys []string) { m.Refresh = overrideBinding(m.Refresh, keys) }},
	{"filter", "Navigation", "search filter", "search filter", func(k keyMap) key.Binding { return k.Filter }, func(m *keyMap, keys []string) { m.Filter = overrideBinding(m.Filter, keys) }},
	{"select", "Navigation", "select / play", "select / play", func(k keyMap) key.Binding { return k.Select }, func(m *keyMap, keys []string) { m.Select = overrideBinding(m.Select, keys) }},
	{"toggle_help", "Navigation", "toggle help", "toggle help", func(k keyMap) key.Binding { return k.ToggleHelp }, func(m *keyMap, keys []string) { m.ToggleHelp = overrideBinding(m.ToggleHelp, keys) }},
	{"settings", "Navigation", "open settings", "open settings", func(k keyMap) key.Binding { return k.Settings }, func(m *keyMap, keys []string) { m.Settings = overrideBinding(m.Settings, keys) }},
	{"close_modal", "Navigation", "close modal", "close modal", func(k keyMap) key.Binding { return k.CloseModal }, func(m *keyMap, keys []string) { m.CloseModal = overrideBinding(m.CloseModal, keys) }},
	{"quit", "Navigation", "quit (ctrl+c always quits)", "quit", func(k keyMap) key.Binding { return k.Quit }, func(m *keyMap, keys []string) {
		if !keysContainList(keys, "ctrl+c") {
			keys = append(append([]string{}, keys...), "ctrl+c")
		}
		m.Quit = overrideBinding(m.Quit, keys)
	}},
	{"queue_up", "Queue", "queue cursor up", "cursor up", func(k keyMap) key.Binding { return k.QueueUp }, func(m *keyMap, keys []string) { m.QueueUp = overrideBinding(m.QueueUp, keys) }},
	{"queue_down", "Queue", "queue cursor down", "cursor down", func(k keyMap) key.Binding { return k.QueueDown }, func(m *keyMap, keys []string) { m.QueueDown = overrideBinding(m.QueueDown, keys) }},
	{"queue_jump", "Queue", "play from queue row", "play from row", func(k keyMap) key.Binding { return k.QueueJump }, func(m *keyMap, keys []string) { m.QueueJump = overrideBinding(m.QueueJump, keys) }},
	{"queue_remove", "Queue", "remove queue row", "remove row", func(k keyMap) key.Binding { return k.QueueRemove }, func(m *keyMap, keys []string) { m.QueueRemove = overrideBinding(m.QueueRemove, keys) }},
	{"queue_move_up", "Queue", "move queue row up", "move row up", func(k keyMap) key.Binding { return k.QueueMoveUp }, func(m *keyMap, keys []string) { m.QueueMoveUp = overrideBinding(m.QueueMoveUp, keys) }},
	{"queue_move_down", "Queue", "move queue row down", "move row down", func(k keyMap) key.Binding { return k.QueueMoveDown }, func(m *keyMap, keys []string) { m.QueueMoveDown = overrideBinding(m.QueueMoveDown, keys) }},
}

// helpGroupsLayout derives the help modal's titled rows from the registry.
var helpGroupsLayout = func() []struct {
	title  string
	action string
	label  string
} {
	out := make([]struct {
		title  string
		action string
		label  string
	}, 0, len(actionRegistry))
	for _, m := range actionRegistry {
		out = append(out, struct {
			title  string
			action string
			label  string
		}{m.group, m.action, m.desc})
	}
	return out
}()

// helpGroupLines renders one group as label……key lines. Labels are padded
// in display cells; keys come from the live keyMap so rebinds reflect.
// helpGroupLines renders one group: title, then label/key rows with the key
// right-aligned at the column's own edge instead of trailing the label
// column. colW is the column's allotted share of the modal width, so the
// three columns spread across the full body instead of hugging the left.
func (m model) helpGroupLines(title string, labelWidth, colW int) []string {
	lines := []string{m.styles.styleSectionLabel.Render(title)}
	for _, g := range helpGroupsLayout {
		if g.title != title {
			continue
		}
		keys, ok := defaultKeysForAction(m.ui.keys, g.action)
		if !ok {
			continue
		}
		lines = append(lines, m.styles.styleQueueTrack.Render(padCell(g.label, labelWidth))+
			m.styles.styleTrackPopupTitle.Render(alignRight(shortKeyLabel(keys), max(0, colW-labelWidth))))
	}
	return lines
}

// helpGroupTitles derives the help modal's group order from the registry
// (dedup in registry order), so a new group renders instead of vanishing.
func helpGroupTitles() []string {
	titles := []string{}
	for _, meta := range actionRegistry {
		if !slices.Contains(titles, meta.group) {
			titles = append(titles, meta.group)
		}
	}
	return titles
}

func (m model) helpGroupedBody(contentW, availH int) string {
	groups := helpGroupTitles()
	labelWidth := 0
	for _, g := range helpGroupsLayout {
		lw := lipgloss.Width(g.label)
		if lw > labelWidth {
			labelWidth = lw
		}
	}
	labelWidth += 4

	const gutter = 4
	// Three columns, each an equal share of the full content width with the
	// keys right-aligned at their column's edge - natural-width columns
	// hugged the left and left a dead band on the right of the modal.
	// Equal shares: for the current three groups this is exactly the old
	// (contentW - 2*gutter) / 3, so the existing layout is pixel-identical.
	colW := (contentW - (len(groups)-1)*gutter) / len(groups)
	if colW >= labelWidth+6 {
		cols := make([][]string, 0, len(groups))
		for _, title := range groups {
			cols = append(cols, m.helpGroupLines(title, labelWidth, colW))
		}
		three := joinTopAligned(cols...)
		if lipgloss.Width(three) <= contentW {
			return three + "\n\n" + m.styles.styleTrackPopupHint.Render("ctrl+c always quits")
		}
	}

	// Stacked fallback: keys right-align at the full content width so the
	// narrow-terminal layout fills the row too.
	parts := make([]string, 0, len(groups))
	for _, title := range groups {
		parts = append(parts, strings.Join(m.helpGroupLines(title, labelWidth, contentW), "\n"))
	}
	// No clipping here: the caller (help viewport) decides overflow.
	return strings.Join(parts, "\n\n") + "\n\n" + m.styles.styleTrackPopupHint.Render("ctrl+c always quits")
}

// joinTopAligned pads each column to the tallest height and places them
// side by side with a shared gutter.
func joinTopAligned(cols ...[]string) string {
	blocks := make([]string, 0, len(cols))
	for _, col := range cols {
		blocks = append(blocks, strings.Join(col, "\n"))
	}
	out := blocks[0]
	for _, b := range blocks[1:] {
		out = lipgloss.JoinHorizontal(lipgloss.Top, out, "    ", b)
	}
	return out
}

// Key identity helpers.
//
// Dispatch matches keys by v2 identity (modifier flags + base code) instead
// of comparing rendered strings: each binding string is reduced to the same
// identity as the pressed key, so aliases ("esc"/"escape",
// "enter"/"return", " "/"space") and out-of-order modifier lists
// ("shift+ctrl" vs "ctrl+shift") resolve to one key.

// isQuitSignal is the single guaranteed-quit check: structually Ctrl+C and
// nothing else. The rebindable quit binding (which also carries "q") must
// not punch through modals or key capture, so this deliberately does not
// consult the registry.
func isQuitSignal(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	if k.Mod&tea.ModCtrl == 0 {
		return false
	}
	return k.Code == 'c' || k.Code == 'C' || k.Text == "c" || k.Text == "C"
}

// isCancelPress and isConfirmPress are the key-capture control keys by
// identity, so they hold however the close and select actions are rebound.
func isCancelPress(msg tea.KeyPressMsg) bool {
	return msg.Key().Code == tea.KeyEscape
}

func isConfirmPress(msg tea.KeyPressMsg) bool {
	return msg.Key().Code == tea.KeyEnter
}

// isModifierCode reports the bare left/right modifier keys, which carry
// no base key and can never match a binding on their own.
func isModifierCode(code rune) bool {
	switch code {
	case tea.KeyLeftShift, tea.KeyRightShift,
		tea.KeyLeftAlt, tea.KeyRightAlt,
		tea.KeyLeftCtrl, tea.KeyRightCtrl,
		tea.KeyLeftSuper, tea.KeyRightSuper,
		tea.KeyLeftHyper, tea.KeyRightHyper,
		tea.KeyLeftMeta, tea.KeyRightMeta:
		return true
	}
	return false
}

// keySpecModifiers is the canonical modifier vocabulary, in the order v2
// renders keystrokes.
var keySpecModifiers = []struct {
	name string
	mod  tea.KeyMod
}{
	{"ctrl", tea.ModCtrl},
	{"alt", tea.ModAlt},
	{"shift", tea.ModShift},
	{"meta", tea.ModMeta},
	{"hyper", tea.ModHyper},
	{"super", tea.ModSuper},
	{"capslock", tea.ModCapsLock},
	{"scrolllock", tea.ModScrollLock},
	{"numlock", tea.ModNumLock},
}

// keySpecBaseCode maps the named bases a binding string may carry. Aliases
// share a code, so they match the same press.
func keySpecBaseCode(name string) (rune, bool) {
	switch name {
	case "enter", "return":
		return tea.KeyEnter, true
	case "tab":
		return tea.KeyTab, true
	case "esc", "escape":
		return tea.KeyEscape, true
	case "space":
		return tea.KeySpace, true
	case "up":
		return tea.KeyUp, true
	case "down":
		return tea.KeyDown, true
	case "left":
		return tea.KeyLeft, true
	case "right":
		return tea.KeyRight, true
	case "home":
		return tea.KeyHome, true
	case "end":
		return tea.KeyEnd, true
	case "pgup":
		return tea.KeyPgUp, true
	case "pgdown":
		return tea.KeyPgDown, true
	case "delete":
		return tea.KeyDelete, true
	case "backspace":
		return tea.KeyBackspace, true
	case "insert":
		return tea.KeyInsert, true
	}
	return 0, false
}

// keyBaseName renders a base code back to its canonical binding name.
func keyBaseName(code rune) (string, bool) {
	switch code {
	case tea.KeyEnter:
		return "enter", true
	case tea.KeyTab:
		return "tab", true
	case tea.KeyEscape:
		return "esc", true
	case tea.KeyUp:
		return "up", true
	case tea.KeyDown:
		return "down", true
	case tea.KeyLeft:
		return "left", true
	case tea.KeyRight:
		return "right", true
	case tea.KeyHome:
		return "home", true
	case tea.KeyEnd:
		return "end", true
	case tea.KeyPgUp:
		return "pgup", true
	case tea.KeyPgDown:
		return "pgdown", true
	case tea.KeyDelete:
		return "delete", true
	case tea.KeyBackspace:
		return "backspace", true
	case tea.KeyInsert:
		return "insert", true
	}
	return "", false
}

// parseKeySpec reduces one binding string to v2 key identity: modifier
// flags plus a base code, with the printable text the identity implies.
// ok=false means the string names no key (loaders drop it).
func parseKeySpec(spec string) (mod tea.KeyMod, code rune, text string, ok bool) {
	if spec == "" {
		return 0, 0, "", false
	}
	parts := strings.Split(spec, "+")
	base := parts[len(parts)-1]
	modParts := parts[:len(parts)-1]
	if base == "" {
		// A trailing "+" means the bound key itself is "+": the lone
		// "+", or "ctrl++" (the real separator is the previous one).
		// A lone separator with a named head ("ctrl+") has no key.
		if len(parts) == 2 && parts[0] != "" {
			return 0, 0, "", false
		}
		base = "+"
		modParts = parts[:len(parts)-2]
	}
	for _, part := range modParts {
		found := false
		for _, m := range keySpecModifiers {
			if part == m.name {
				mod |= m.mod
				found = true
				break
			}
		}
		if !found {
			return 0, 0, "", false
		}
	}
	if base == " " {
		code = tea.KeySpace
	} else if c, named := keySpecBaseCode(base); named {
		code = c
	} else if utf8.RuneCountInString(base) == 1 {
		code, _ = utf8.DecodeRuneInString(base)
	} else {
		return 0, 0, "", false
	}
	// Printable bases imply their text unless another modifier rides
	// along — mirrors ultraviolet's matcher so "shift+j" still matches
	// a "J" press reported without modifier flags.
	if rest := mod &^ (tea.ModShift | tea.ModCapsLock); rest == 0 && text == "" && unicode.IsPrint(code) {
		if mod&(tea.ModShift|tea.ModCapsLock) != 0 {
			text = string(unicode.ToUpper(code))
		} else {
			text = string(code)
		}
	}
	return mod, code, text, true
}

// matchKeySpec compares a pressed key against one binding string by
// identity: same modifiers and base code, or (for printable keys) the
// same reported text.
func matchKeySpec(k tea.Key, spec string) bool {
	mod, code, text, ok := parseKeySpec(spec)
	if !ok {
		return false
	}
	return (k.Mod == mod && k.Code == code) || (k.Text != "" && k.Text == text)
}

// canonicalKeySpec re-emits a binding string in canonical form (modifiers
// in keystroke order, aliases folded) for comparisons. Unparseable specs
// pass through unchanged.
func canonicalKeySpec(spec string) string {
	mod, code, _, ok := parseKeySpec(spec)
	if !ok {
		return spec
	}
	var b strings.Builder
	for _, m := range keySpecModifiers {
		if mod&m.mod != 0 {
			b.WriteString(m.name)
			b.WriteByte('+')
		}
	}
	switch code {
	case tea.KeySpace:
		b.WriteString("space")
	case tea.KeyEnter:
		b.WriteString("enter")
	case tea.KeyEscape:
		b.WriteString("esc")
	default:
		if name, named := keyBaseName(code); named {
			b.WriteString(name)
		} else {
			b.WriteRune(code)
		}
	}
	return b.String()
}
