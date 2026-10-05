package tui

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
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
	Search        key.Binding
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
		Tab:       key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch tab")),
		PlayPause: key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "play/pause")),
		Next:      key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next")),
		Prev:      key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "prev")),
		Shuffle:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shuffle")),
		Loop:      key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "repeat")),
		VolUp:     key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "vol+")),
		VolDown:   key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "vol-")),
		SeekBack:  key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "-5s")),
		SeekFwd:   key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "+5s")),
		Refresh:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Filter:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		// Ctrl+L is swallowed by some terminal emulators as a redraw command;
		// keep it as an alias but offer Ctrl+F and F3 as reliable alternatives.
		Search:        key.NewBinding(key.WithKeys("ctrl+f", "ctrl+l", "f3"), key.WithHelp("ctrl+f", "open Search tab")),
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
	return []key.Binding{k.Tab, k.Select, k.PlayPause, k.Next, k.Prev, k.Shuffle, k.Loop, k.Filter, k.Search, k.ToggleHelp, k.Settings, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Tab, k.Select, k.CloseModal, k.Refresh, k.Filter, k.Search},
		{k.PlayPause, k.Next, k.Prev, k.Shuffle, k.Loop, k.VolUp, k.VolDown},
		{k.QueueUp, k.QueueDown, k.QueueJump, k.QueueRemove, k.QueueMoveUp, k.QueueMoveDown},
		{k.SeekBack, k.SeekFwd, k.ToggleHelp, k.Settings, k.Quit},
	}
}

// actionRegistry is the single ordered source for every bindable action;
// order defines help group and settings menu order.
type actionMeta struct {
	action string
	group  string
	label  string
	desc   string
	bind   func(keyMap) key.Binding
	set    func(*keyMap, []string)
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
	{"search", "Navigation", "open Search tab", "open Search tab", func(k keyMap) key.Binding { return k.Search }, func(m *keyMap, keys []string) { m.Search = overrideBinding(m.Search, keys) }},
	{"select", "Navigation", "select / play", "select / play", func(k keyMap) key.Binding { return k.Select }, func(m *keyMap, keys []string) { m.Select = overrideBinding(m.Select, keys) }},
	{"toggle_help", "Navigation", "toggle help", "toggle help", func(k keyMap) key.Binding { return k.ToggleHelp }, func(m *keyMap, keys []string) { m.ToggleHelp = overrideBinding(m.ToggleHelp, keys) }},
	{"settings", "Navigation", "open settings", "open settings", func(k keyMap) key.Binding { return k.Settings }, func(m *keyMap, keys []string) { m.Settings = overrideBinding(m.Settings, keys) }},
	{"close_modal", "Navigation", "close modal", "close modal", func(k keyMap) key.Binding { return k.CloseModal }, func(m *keyMap, keys []string) { m.CloseModal = overrideBinding(m.CloseModal, keys) }},
	{"quit", "Navigation", "quit (ctrl+c always quits)", "quit", func(k keyMap) key.Binding { return k.Quit }, func(m *keyMap, keys []string) {
		if !slices.Contains(keys, "ctrl+c") {
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

// Keys right-align at their column's edge and come from the live keyMap so rebinds reflect.
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

	// Stacked fallback for narrow terminals.
	labelWidth = min(labelWidth, max(8, contentW-7))
	parts := make([]string, 0, len(groups))
	for _, title := range groups {
		parts = append(parts, strings.Join(m.helpGroupLines(title, labelWidth, contentW), "\n"))
	}
	// No clipping here: the caller (help viewport) decides overflow.
	return strings.Join(parts, "\n\n") + "\n\n" + m.styles.styleTrackPopupHint.Render("ctrl+c always quits")
}

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

// Key identity: dispatch reduces bindings and presses to the same v2 identity
// (modifier flags + base code), so aliases and reordered modifiers resolve to one key.

// isQuitSignal is structurally Ctrl+C and nothing else; it deliberately does
// not consult the registry, so the rebindable quit binding cannot punch through modals.
func isQuitSignal(msg tea.KeyPressMsg) bool {
	k := msg.Key()
	if k.Mod&tea.ModCtrl == 0 {
		return false
	}
	return k.Code == 'c' || k.Code == 'C' || k.Text == "c" || k.Text == "C"
}

// Capture controls match by identity, so they hold however close/select are rebound.
func isCancelPress(msg tea.KeyPressMsg) bool {
	return msg.Key().Code == tea.KeyEscape
}

func isConfirmPress(msg tea.KeyPressMsg) bool {
	return msg.Key().Code == tea.KeyEnter
}

// Bare modifiers carry no base key and can never match a binding on their own.
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

func keyModFlag(s string) (tea.KeyMod, bool) {
	for _, m := range keySpecModifiers {
		if s == m.name {
			return m.mod, true
		}
	}
	return 0, false
}

func isKeyModName(s string) bool {
	_, ok := keyModFlag(s)
	return ok
}

// Aliases share a code, so they match the same press.
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

// ok=false means the string names no key (loaders drop it).
func parseKeySpec(spec string) (mod tea.KeyMod, code rune, text string, ok bool) {
	if spec == "" {
		return 0, 0, "", false
	}
	parts := strings.Split(spec, "+")
	base := parts[len(parts)-1]
	modParts := parts[:len(parts)-1]
	if base == "" {
		// A trailing "+" names the plus key itself ("ctrl++"); "ctrl+" names no key.
		if len(parts) == 2 && parts[0] != "" {
			return 0, 0, "", false
		}
		base = "+"
		modParts = parts[:len(parts)-2]
	}
	for _, part := range modParts {
		flag, ok := keyModFlag(part)
		if !ok {
			return 0, 0, "", false
		}
		mod |= flag
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
	// Mirrors ultraviolet's matcher so "shift+j" matches a bare "J" press.
	if rest := mod &^ (tea.ModShift | tea.ModCapsLock); rest == 0 && text == "" && unicode.IsPrint(code) {
		if mod&(tea.ModShift|tea.ModCapsLock) != 0 {
			text = string(unicode.ToUpper(code))
		} else {
			text = string(code)
		}
	}
	return mod, code, text, true
}

// The comparison itself is ultraviolet's; this wrapper only rewrites the two
// forms uv cannot parse: a "return" base (uv knows just "enter") and
// a literal "+" base (a trailing "+" is uv's separator, with no
// escape for naming the plus key itself).
func matchKeySpec(k tea.Key, spec string) bool {
	if isLiteralPlusSpec(spec) {
		mod, code, text, ok := parseKeySpec(spec)
		if !ok {
			return false
		}
		// Same comparison ultraviolet performs (mod+code, else text);
		// kept inline because no uv-parseable spelling names this key.
		return (k.Mod == mod && k.Code == code) || (k.Text != "" && k.Text == text)
	}
	return uv.Key(k).MatchString(foldReturnSpec(spec))
}

// isLiteralPlusSpec reports whether spec binds the literal "+" key: a
// trailing "+" with no key after it. parseKeySpec stays the authority
// on the shape (a lone "ctrl+" still names no key).
func isLiteralPlusSpec(spec string) bool {
	return strings.HasSuffix(spec, "+")
}

// ultraviolet's vocabulary has no "return" alias; non-final segments are validated
// modifiers, so the fold cannot rescue a malformed binding.
func foldReturnSpec(spec string) string {
	if spec == "return" {
		return "enter"
	}
	if rest, found := strings.CutSuffix(spec, "+return"); found {
		return rest + "+enter"
	}
	return spec
}

// The modifier vocabulary stays local because uv's renderer drops the lock
// modifiers the conflict scan must keep distinct.
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
	b.WriteString(uv.Key{Code: code}.Keystroke())
	return b.String()
}
