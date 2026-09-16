package tui

import (
	"log/slog"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type optionKind int

const (
	optionCycle optionKind = iota
	optionSave
	optionReset
)

// themeOptionsRowsList drives the theming editor: order is the menu order,
// kind marks the save/reset action rows.
var themeOptionsRowsList = []struct {
	kind  optionKind
	label string
}{
	{optionCycle, "Base palette"},
	{optionCycle, "Page tone"},
	{optionCycle, "Accent"},
	{optionCycle, "Backgrounds"},
	{optionCycle, "Border"},
	{optionCycle, "Now playing"},
	{optionCycle, "Play/pause"},
	{optionCycle, "Spinner"},
	{optionCycle, "Progress bar"},
	{optionCycle, "Titles bold"},
	{optionCycle, "Descriptions italic"},
	{optionCycle, "Cover frame"},
	{optionSave, "Save to theme.json"},
	{optionReset, "Reset to preset"},
}

func themeOptionsRow(i int) struct {
	kind  optionKind
	label string
} {
	return themeOptionsRowsList[i]
}

func themeOptionsRowCount() int {
	return len(themeOptionsRowsList)
}

// Curated cycles: "preset" always comes first in the color cycles and
// restores the base palette's value for that role.
var (
	pageToneChoices = []string{"#06080B", "#0A0D12", "#101216", "#14181E", "#1A1B20"}
	accentChoices   = []string{"#4A90D9", "#7AA2F7", "#89B4FA", "#C4A7E7", "#8EC07C", "#FE8019", "#F38BA8", "#E5C07B", "#00FF87"}
)

func cycleValue(choices []string, current string, step int) string {
	for i, c := range choices {
		if c == current {
			return choices[(i+step+len(choices))%len(choices)]
		}
	}
	if step < 0 {
		return choices[len(choices)-1]
	}
	return choices[0]
}

// cycleColor keeps the preset value as the head of the cycle so "preset"
// and the curated hexes round-trip in one ring; a preset value already
// present in the curated list is not repeated.
func cycleColor(choices []string, current, preset string, step int) string {
	all := []string{preset}
	for _, c := range choices {
		if c != preset {
			all = append(all, c)
		}
	}
	return cycleValue(all, current, step)
}

func (m *model) openThemeOptions() {
	s := &m.ui.settings
	s.mode = settingsModeThemeOptions
	s.themeOptionsPreset = themePresetName(s.themePreset)
	s.themeStatePending = resolveThemeState(s.themeOptionsPreset, m.cachedThemeOverrides())
	s.themeStateBackup = s.themeStatePending
	s.optionsCursor = 0
}

// themeOptionsApply renders a draft state live: styles, list delegates,
// spinner and procedural art all follow.
func (m model) themeOptionsApply(state themeState) (tea.Model, tea.Cmd) {
	applyTheme(state)
	m.rethemeBrowseLists()
	m.ui.spinner = themedSpinner()
	m.refreshLikedSongsArt()
	m.ui.settings.keysTableDirty = true
	page := lipgloss.Color(state.colors.Page)
	return m, func() tea.Msg { return TerminalBGSync(page) }
}

func (m model) themeOptionsCycle(step int) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	pending := s.themeStatePending
	preset := themePreset(s.themeOptionsPreset)

	switch s.optionsCursor {
	case 0:
		names := themeRegistryNames()
		next := cycleValue(names, s.themeOptionsPreset, step)
		s.themeOptionsPreset = next
		// Base change restarts the palette from the new preset but keeps
		// the glyph/typography/cover/backgrounds edits made so far.
		base := themePresetState(next)
		base.glyphs = pending.glyphs
		base.typography = pending.typography
		base.cover = pending.cover
		base.backgrounds = pending.backgrounds
		pending = base
	case 1:
		presetPage := preset.Page
		pending.colors.Page = cycleColor(pageToneChoices, pending.colors.Page, presetPage, step)
	case 2:
		presetAccent := preset.Blue
		current := cycleColor(accentChoices, pending.colors.Blue, presetAccent, step)
		pending.colors.Blue = current
		if current == presetAccent {
			pending.colors.BlueLight = preset.BlueLight
		} else if light := mixHex(current, "#FFFFFF", 0.30); light != "" {
			pending.colors.BlueLight = light
		}
	case 3:
		pending.backgrounds.Style = cycleValue(backgroundStyleChoices, pending.backgrounds.Style, step)
	case 4:
		pending.glyphs.Border = cycleValue(glyphBorderChoices, pending.glyphs.Border, step)
	case 5:
		pending.glyphs.NowPlaying = cycleValue(glyphNowPlayingChoices, pending.glyphs.NowPlaying, step)
	case 6:
		pending.glyphs.PlayPause = cycleValue(glyphPlayPauseChoices, pending.glyphs.PlayPause, step)
	case 7:
		pending.glyphs.Spinner = cycleValue(glyphSpinnerChoices, pending.glyphs.Spinner, step)
	case 8:
		pending.glyphs.Bar = cycleValue(glyphBarChoices, pending.glyphs.Bar, step)
	case 9:
		pending.typography.BoldTitles = !pending.typography.BoldTitles
	case 10:
		pending.typography.ItalicDescs = !pending.typography.ItalicDescs
	case 11:
		pending.cover.Frame = cycleValue(coverFrameChoices, pending.cover.Frame, step)
	default:
		return m, nil
	}

	s.themeStatePending = pending
	return m.themeOptionsApply(pending)
}

func (m model) themeOptionsReset() (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	base := themePresetState(s.themeOptionsPreset)
	s.themeStatePending = base
	return m.themeOptionsApply(base)
}

func (m model) themeOptionsRevert() (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	// Mode first: the apply call copies the model, so the returned copy
	// must already carry the exit to the root.
	s.mode = settingsModeRoot
	s.themePreset = themePresetName(s.themePreset)
	return m.themeOptionsApply(s.themeStateBackup)
}

func (m model) themeOptionsSave() (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	if err := SaveThemeOptions(s.themePath, s.themeOptionsPreset, s.themeStatePending); err != nil {
		s.saveErr = "theme save failed: " + err.Error()
		slog.Warn("failed saving theme options", "path", s.themePath, "error", err)
		return m, nil
	}
	s.saveErr = ""
	s.themeOverrides = nil
	s.themePreset = themePresetName(s.themeOptionsPreset)
	s.mode = settingsModeRoot
	return m, nil
}

// themeOptionsValue renders one row's current value for the menu.
func (m model) themeOptionsValue(i int) string {
	s := &m.ui.settings
	pending := s.themeStatePending
	preset := themePreset(s.themeOptionsPreset)
	switch i {
	case 0:
		return s.themeOptionsPreset
	case 1:
		if pending.colors.Page == preset.Page {
			return "preset"
		}
		return pending.colors.Page
	case 2:
		if pending.colors.Blue == preset.Blue {
			return "preset"
		}
		return pending.colors.Blue
	case 3:
		return pending.backgrounds.Style
	case 4:
		return pending.glyphs.Border
	case 5:
		return pending.glyphs.NowPlaying
	case 6:
		return pending.glyphs.PlayPause
	case 7:
		return pending.glyphs.Spinner
	case 8:
		return pending.glyphs.Bar
	case 9:
		return onOff(pending.typography.BoldTitles)
	case 10:
		return onOff(pending.typography.ItalicDescs)
	case 11:
		return pending.cover.Frame
	}
	return ""
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// themeOptionsView renders the theming editor: rows with right-aligned
// values, save/reset actions, and a scrolling window on short terminals.
func (m model) themeOptionsView(modalW, innerH int) string {
	s := m.ui.settings
	listH := max(4, innerH-6)
	rows := make([]string, 0, themeOptionsRowCount())
	for i := 0; i < themeOptionsRowCount(); i++ {
		entry := themeOptionsRow(i)
		value := ""
		if entry.kind == optionCycle {
			value = m.themeOptionsValue(i)
		}
		rows = append(rows, modalRow(entry.label, value, s.optionsCursor == i, modalW))
	}

	offset := 0
	if s.optionsCursor >= listH {
		offset = s.optionsCursor - listH + 1
	}
	window := rows[min(offset, len(rows)):min(offset+listH, len(rows))]

	var body strings.Builder
	body.WriteString("\n" + lipgloss.JoinVertical(lipgloss.Left, window...) + "\n")
	k := m.ui.keys
	hint := hintLine([]key.Binding{
		key.NewBinding(key.WithKeys(k.QueueUp.Keys()...), key.WithHelp(k.QueueUp.Help().Key+"/"+k.QueueDown.Help().Key, "select")),
		withDesc(k.VolUp, "next"),
		withDesc(k.VolDown, "prev"),
		withDesc(k.Select, "change / save"),
		withDesc(k.CloseModal, "revert"),
	}, modalW-modalContentInset)
	return modalFrame(m.ui.width, m.ui.height, styleModalTitle.Render("Theme options"), hint, body.String(), modalW, innerH)
}
