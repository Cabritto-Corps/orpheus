package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
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
}

var actionRegistry = []actionMeta{
	{"play_pause", "Playback", "play/pause", "play/pause", func(k keyMap) key.Binding { return k.PlayPause }},
	{"next", "Playback", "next track", "next track", func(k keyMap) key.Binding { return k.Next }},
	{"prev", "Playback", "previous track", "previous track", func(k keyMap) key.Binding { return k.Prev }},
	{"shuffle", "Playback", "shuffle", "shuffle", func(k keyMap) key.Binding { return k.Shuffle }},
	{"loop", "Playback", "repeat", "repeat", func(k keyMap) key.Binding { return k.Loop }},
	{"vol_up", "Playback", "volume up", "volume up", func(k keyMap) key.Binding { return k.VolUp }},
	{"vol_down", "Playback", "volume down", "volume down", func(k keyMap) key.Binding { return k.VolDown }},
	{"seek_back", "Playback", "seek back", "seek back", func(k keyMap) key.Binding { return k.SeekBack }},
	{"seek_fwd", "Playback", "seek forward", "seek forward", func(k keyMap) key.Binding { return k.SeekFwd }},
	{"tab", "Navigation", "switch tab", "switch tab", func(k keyMap) key.Binding { return k.Tab }},
	{"refresh", "Navigation", "refresh library", "refresh library", func(k keyMap) key.Binding { return k.Refresh }},
	{"filter", "Navigation", "search filter", "search filter", func(k keyMap) key.Binding { return k.Filter }},
	{"select", "Navigation", "select / play", "select / play", func(k keyMap) key.Binding { return k.Select }},
	{"toggle_help", "Navigation", "toggle help", "toggle help", func(k keyMap) key.Binding { return k.ToggleHelp }},
	{"settings", "Navigation", "open settings", "open settings", func(k keyMap) key.Binding { return k.Settings }},
	{"close_modal", "Navigation", "close modal", "close modal", func(k keyMap) key.Binding { return k.CloseModal }},
	{"quit", "Navigation", "quit (ctrl+c always quits)", "quit", func(k keyMap) key.Binding { return k.Quit }},
	{"queue_up", "Queue", "queue cursor up", "cursor up", func(k keyMap) key.Binding { return k.QueueUp }},
	{"queue_down", "Queue", "queue cursor down", "cursor down", func(k keyMap) key.Binding { return k.QueueDown }},
	{"queue_jump", "Queue", "play from queue row", "play from row", func(k keyMap) key.Binding { return k.QueueJump }},
	{"queue_remove", "Queue", "remove queue row", "remove row", func(k keyMap) key.Binding { return k.QueueRemove }},
	{"queue_move_up", "Queue", "move queue row up", "move row up", func(k keyMap) key.Binding { return k.QueueMoveUp }},
	{"queue_move_down", "Queue", "move queue row down", "move row down", func(k keyMap) key.Binding { return k.QueueMoveDown }},
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
// right-aligned to the group's own edge instead of trailing the label column
// (the left-packed layout read as one undifferentiated wall of text).
func (m model) helpGroupLines(title string, labelWidth int) []string {
	type row struct{ label, key string }
	var rows []row
	keyW := 0
	for _, g := range helpGroupsLayout {
		if g.title != title {
			continue
		}
		keys, ok := defaultKeysForAction(m.ui.keys, g.action)
		if !ok {
			continue
		}
		k := shortKeyLabel(keys)
		rows = append(rows, row{g.label, k})
		if w := lipgloss.Width(k); w > keyW {
			keyW = w
		}
	}
	lines := []string{styleSectionLabel.Render(title)}
	for _, r := range rows {
		lines = append(lines, styleQueueTrack.Render(padCell(r.label, labelWidth))+
			styleTrackPopupTitle.Render(alignRight(r.key, keyW)))
	}
	return lines
}

func (m model) helpGroupedBody(contentW, availH int) string {
	groups := []string{"Playback", "Navigation", "Queue"}
	labelWidth := 0
	for _, g := range helpGroupsLayout {
		lw := lipgloss.Width(g.label)
		if lw > labelWidth {
			labelWidth = lw
		}
	}
	labelWidth += 4

	cols := make([][]string, 0, len(groups))
	for _, title := range groups {
		cols = append(cols, m.helpGroupLines(title, labelWidth))
	}

	// Fill the modal with three columns only when the joined block actually
	// fits; the old threshold math rendered the three-column join while
	// gated on a two-column budget, overflowing the modal at 70-89 cols.
	three := joinTopAligned(cols[0], cols[1], cols[2])
	var body string
	if lipgloss.Width(three) <= contentW {
		body = three
	} else {
		parts := make([]string, 0, len(cols))
		for _, col := range cols {
			parts = append(parts, strings.Join(col, "\n"))
		}
		body = strings.Join(parts, "\n\n")
	}
	body += "\n\n" + styleTrackPopupHint.Render("ctrl+c always quits")
	// No clipping here: the caller (help viewport) decides overflow.
	return body
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
