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

type helpGroup struct {
	title string
	items []struct{ action, label string }
}

var helpGroupsLayout = []struct {
	title  string
	action string
	label  string
}{
	// Playback
	{"Playback", "play_pause", "play/pause"},
	{"Playback", "next", "next track"},
	{"Playback", "prev", "previous track"},
	{"Playback", "shuffle", "shuffle"},
	{"Playback", "loop", "repeat"},
	{"Playback", "vol_up", "volume up"},
	{"Playback", "vol_down", "volume down"},
	{"Playback", "seek_back", "seek back"},
	{"Playback", "seek_fwd", "seek forward"},
	// Navigation
	{"Navigation", "tab", "switch tab"},
	{"Navigation", "filter", "search filter"},
	{"Navigation", "select", "select / play"},
	{"Navigation", "refresh", "refresh library"},
	{"Navigation", "toggle_help", "toggle help"},
	{"Navigation", "settings", "open settings"},
	{"Navigation", "close_modal", "close modal"},
	{"Navigation", "quit", "quit"},
	// Queue
	{"Queue", "queue_up", "cursor up"},
	{"Queue", "queue_down", "cursor down"},
	{"Queue", "queue_jump", "play from row"},
	{"Queue", "queue_remove", "remove row"},
	{"Queue", "queue_move_up", "move row up"},
	{"Queue", "queue_move_down", "move row down"},
}

// helpGroupLines renders one group as label……key lines.
func (m model) helpGroupLines(title string, labelWidth int) []string {
	lines := []string{styleSectionLabel.Render(title)}
	for _, g := range helpGroupsLayout {
		if g.title != title {
			continue
		}
		keys, ok := defaultKeysForAction(m.ui.keys, g.action)
		if !ok {
			continue
		}
		label := g.label
		if g.action == "quit" {
			label += " (ctrl+c always quits)"
		}
		lines = append(lines, styleQueueTrack.Render(padTo(label, labelWidth))+
			styleTrackPopupTitle.Render(shortKeyLabel(keys)))
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

	var body string
	if m.ui.width >= 90 {
		left := strings.Join(append(cols[0], ""), "\n")
		right := strings.Join(append(cols[1], "\n", strings.Join(cols[2], "\n")), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, "    ", right)
	} else {
		parts := make([]string, 0, len(cols))
		for _, col := range cols {
			parts = append(parts, strings.Join(col, "\n"))
		}
		body = strings.Join(parts, "\n\n")
	}
	return body
}
