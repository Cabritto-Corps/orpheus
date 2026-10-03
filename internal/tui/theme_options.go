package tui

import (
	"log/slog"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type optionKind int

const (
	optionCycle optionKind = iota
	optionSave
	optionReset
)

// The editor reads this table everywhere (cursor wrap, rendering,
// cycling), so adding a control is one entry here.
type themeOptionRow struct {
	kind  optionKind
	label string
	value func(s *settingsModel) string
	cycle func(s *settingsModel, pending *themeState, step int)
}

var themeOptionsRowsList = []themeOptionRow{
	{optionCycle, "Base palette",
		func(s *settingsModel) string { return s.themeOptionsPreset },
		func(s *settingsModel, pending *themeState, step int) {
			names := themeRegistryNames()
			next := cycleValue(names, s.themeOptionsPreset, step)
			s.themeOptionsPreset = next
			// Palette restarts from the new preset; glyph/cover/etc
			// edits made so far are kept.
			base := themePresetState(next)
			base.glyphs = pending.glyphs
			base.typography = pending.typography
			base.cover = pending.cover
			base.backgrounds = pending.backgrounds
			*pending = base
		}},
	{optionCycle, "Page tone",
		func(s *settingsModel) string {
			if s.themeStatePending.colors.Page == themePreset(s.themeOptionsPreset).Page {
				return "preset"
			}
			return s.themeStatePending.colors.Page
		},
		func(s *settingsModel, pending *themeState, step int) {
			presetPage := themePreset(s.themeOptionsPreset).Page
			pending.colors.Page = cycleColor(pageToneChoices, pending.colors.Page, presetPage, step)
		}},
	{optionCycle, "Accent",
		func(s *settingsModel) string {
			if s.themeStatePending.colors.Blue == themePreset(s.themeOptionsPreset).Blue {
				return "preset"
			}
			return s.themeStatePending.colors.Blue
		},
		func(s *settingsModel, pending *themeState, step int) {
			presetAccent := themePreset(s.themeOptionsPreset).Blue
			current := cycleColor(accentChoices, pending.colors.Blue, presetAccent, step)
			pending.colors.Blue = current
			if current == presetAccent {
				pending.colors.BlueLight = themePreset(s.themeOptionsPreset).BlueLight
			} else if light := mixHex(current, "#FFFFFF", 0.30); light != "" {
				pending.colors.BlueLight = light
			}
		}},
	{optionCycle, "Backgrounds",
		func(s *settingsModel) string { return s.themeStatePending.backgrounds.Style },
		func(s *settingsModel, pending *themeState, step int) {
			pending.backgrounds.Style = cycleValue(backgroundStyleChoices, pending.backgrounds.Style, step)
		}},
	{optionCycle, "Border",
		func(s *settingsModel) string { return s.themeStatePending.glyphs.Border },
		func(s *settingsModel, pending *themeState, step int) {
			pending.glyphs.Border = cycleValue(glyphBorderChoices, pending.glyphs.Border, step)
		}},
	{optionCycle, "Queue cursor",
		func(s *settingsModel) string { return s.themeStatePending.glyphs.QueueCursor },
		func(s *settingsModel, pending *themeState, step int) {
			pending.glyphs.QueueCursor = cycleValue(glyphQueueCursorChoices, pending.glyphs.QueueCursor, step)
		}},
	{optionCycle, "Play/pause",
		func(s *settingsModel) string { return s.themeStatePending.glyphs.PlayPause },
		func(s *settingsModel, pending *themeState, step int) {
			pending.glyphs.PlayPause = cycleValue(glyphPlayPauseChoices, pending.glyphs.PlayPause, step)
		}},
	{optionCycle, "Spinner",
		func(s *settingsModel) string { return s.themeStatePending.glyphs.Spinner },
		func(s *settingsModel, pending *themeState, step int) {
			pending.glyphs.Spinner = cycleValue(glyphSpinnerChoices, pending.glyphs.Spinner, step)
		}},
	{optionCycle, "Progress bar",
		func(s *settingsModel) string { return s.themeStatePending.glyphs.Bar },
		func(s *settingsModel, pending *themeState, step int) {
			pending.glyphs.Bar = cycleValue(glyphBarChoices, pending.glyphs.Bar, step)
		}},
	{optionCycle, "Titles bold",
		func(s *settingsModel) string { return onOff(s.themeStatePending.typography.BoldTitles) },
		func(s *settingsModel, pending *themeState, step int) {
			pending.typography.BoldTitles = !pending.typography.BoldTitles
		}},
	{optionCycle, "Descriptions italic",
		func(s *settingsModel) string { return onOff(s.themeStatePending.typography.ItalicDescs) },
		func(s *settingsModel, pending *themeState, step int) {
			pending.typography.ItalicDescs = !pending.typography.ItalicDescs
		}},
	{optionCycle, "Cover frame",
		func(s *settingsModel) string { return s.themeStatePending.cover.Frame },
		func(s *settingsModel, pending *themeState, step int) {
			pending.cover.Frame = cycleValue(coverFrameChoices, pending.cover.Frame, step)
		}},
	{optionSave, "Save to theme.json", nil, nil},
	{optionReset, "Reset to preset", nil, nil},
}

func themeOptionsRow(i int) themeOptionRow {
	return themeOptionsRowsList[i]
}

func themeOptionsRowCount() int {
	return len(themeOptionsRowsList)
}

// "preset" heads each color cycle and restores the base value.
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

// Preset heads the ring (deduped) so it and the curated hexes
// round-trip.
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

// The procedural cover stays out of the live path (its regen
// dominates a keypress) and syncs on save/exit instead.
func (m model) themeOptionsApply(state themeState) (tea.Model, tea.Cmd) {
	m.styles = buildThemeStyles(state)
	m.rethemeBrowseLists()
	m.ui.spinner = themedSpinner(m.styles)
	return m, func() tea.Msg {
		return TerminalBGSync(m.styles.colorPage, m.styles.transparentFrame(), m.styles.colorProfile)
	}
}

// Use it wherever a theme mode closes over a changed palette.
func (m model) themeOptionsApplyAndRefresh(state themeState) (tea.Model, tea.Cmd) {
	next, cmd := m.themeOptionsApply(state)
	applied := next.(model)
	applied.refreshLikedSongsArt()
	return applied, cmd
}

func (m model) themeOptionsCycle(step int) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	row := themeOptionsRow(s.optionsCursor)
	if row.cycle == nil {
		return m, nil
	}
	row.cycle(s, &s.themeStatePending, step)
	return m.themeOptionsApply(s.themeStatePending)
}

func (m model) themeOptionsReset() (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	base := themePresetState(s.themeOptionsPreset)
	s.themeStatePending = base
	return m.themeOptionsApply(base)
}

func (m model) themeOptionsRevert() (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	// Mode first: apply copies the model, so the returned copy must
	// already carry the exit.
	s.mode = settingsModeRoot
	s.themePreset = themePresetName(s.themePreset)
	return m.themeOptionsApplyAndRefresh(s.themeStateBackup)
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
	m.refreshLikedSongsArt()
	return m, nil
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func (m model) themeOptionsView(modalW, innerH int) string {
	s := m.ui.settings
	listH := max(4, innerH-6)
	rows := make([]string, 0, themeOptionsRowCount())
	for i := 0; i < themeOptionsRowCount(); i++ {
		entry := themeOptionsRow(i)
		value := ""
		if entry.kind == optionCycle && entry.value != nil {
			value = entry.value(&m.ui.settings)
		}
		rows = append(rows, m.styles.modalRow(entry.label, value, s.optionsCursor == i, modalW))
	}

	window, _ := scrollRows(rows, s.optionsCursor, listH)

	var body strings.Builder
	body.WriteString("\n" + lipgloss.JoinVertical(lipgloss.Left, window...) + "\n")
	k := m.ui.keys
	hint := m.styles.hintLine([]key.Binding{
		key.NewBinding(key.WithKeys(k.QueueUp.Keys()...), key.WithHelp(k.QueueUp.Help().Key+"/"+k.QueueDown.Help().Key, "select")),
		withDesc(k.VolUp, "next"),
		withDesc(k.VolDown, "prev"),
		withDesc(k.Select, "change / save"),
		withDesc(k.CloseModal, "revert"),
	}, modalW-modalContentInset)
	return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Theme options"), hint, body.String(), modalW, innerH)
}
