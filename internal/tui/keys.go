package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

type keyMap struct {
	PlayPause  key.Binding
	Next       key.Binding
	Prev       key.Binding
	Shuffle    key.Binding
	Loop       key.Binding
	VolUp      key.Binding
	VolDown    key.Binding
	SeekBack   key.Binding
	SeekFwd    key.Binding
	Refresh    key.Binding
	Filter     key.Binding
	ToggleHelp key.Binding
	Select     key.Binding
	OpenPicker key.Binding
	CloseModal key.Binding
	Quit       key.Binding
}

func newKeys() keyMap {
	return keyMap{
		PlayPause:  key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "play/pause")),
		Next:       key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next")),
		Prev:       key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "prev")),
		Shuffle:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shuffle")),
		Loop:       key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "repeat")),
		VolUp:      key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "vol+")),
		VolDown:    key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "vol-")),
		SeekBack:   key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "-5s")),
		SeekFwd:    key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "+5s")),
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		ToggleHelp: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Select:     key.NewBinding(key.WithKeys("enter", "return"), key.WithHelp("enter", "play")),
		OpenPicker: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "playlists")),
		CloseModal: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Select, k.PlayPause, k.Next, k.Prev, k.Shuffle, k.Loop, k.OpenPicker, k.Filter, k.ToggleHelp, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Select, k.OpenPicker, k.CloseModal, k.Refresh, k.Filter},
		{k.PlayPause, k.Next, k.Prev, k.Shuffle, k.Loop, k.VolUp, k.VolDown},
		{k.SeekBack, k.SeekFwd, k.ToggleHelp, k.Quit},
	}
}

func newHelp() help.Model {
	h := help.New()
	h.Styles.ShortKey = lipgloss.NewStyle().Foreground(colorBlue)
	h.Styles.ShortDesc = lipgloss.NewStyle().Foreground(colorMutedBlue)
	h.Styles.ShortSeparator = lipgloss.NewStyle().Foreground(colorMutedBlue)
	h.Styles.FullKey = lipgloss.NewStyle().Foreground(colorBlue)
	h.Styles.FullDesc = lipgloss.NewStyle().Foreground(colorMutedBlue)
	h.Styles.FullSeparator = lipgloss.NewStyle().Foreground(colorMutedBlue)
	h.Styles.Ellipsis = lipgloss.NewStyle().Foreground(colorMutedBlue)
	return h
}
