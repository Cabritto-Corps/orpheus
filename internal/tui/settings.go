package tui

import (
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"orpheus/internal/config"
)

func newSettingsModel(cfg config.Config) settingsModel {
	return settingsModel{
		themePreset:      themePresetName(cfg.Theme),
		keysPath:         cfg.KeysPath,
		themePath:        cfg.ThemePath,
		envPath:          cfg.EnvPath,
		crossfadeEnabled: cfg.Crossfade,
		crossfadeSeconds: cfg.CrossfadeSeconds,
		cacheEnabled:     cfg.AudioCacheEnabled,
		cacheSizeMB:      cfg.AudioCacheSizeMB,
	}
}

func (m model) openSettings() (tea.Model, tea.Cmd) {
	m.ui.settings.open = true
	m.ui.settings.mode = settingsModeRoot
	m.ui.settings.cursor = 0
	m.ui.settings.captureKey = ""
	m.ui.settings.pendingKey = ""
	m.ui.settings.conflicts = keyConflictActions(m.ui.keys)
	m.ui.settings.keysTableDirty = true
	return m, nil
}

func (m model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings

	switch s.mode {
	case settingsModeCapture:
		return m.handleSettingsCapture(msg)
	case settingsModeKeys:
		return m.handleSettingsKeysMode(msg)
	case settingsModeTheme:
		return m.handleSettingsTheme(msg)
	default:
		return m.handleSettingsRoot(msg)
	}
}

func (m model) handleSettingsRoot(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.CloseModal):
		m.ui.settings.open = false
		return m, nil
	case keyMatches(msg, k.QueueUp):
		m.ui.settings.cursor = (m.ui.settings.cursor + 3) % 4
		return m, nil
	case keyMatches(msg, k.QueueDown):
		m.ui.settings.cursor = (m.ui.settings.cursor + 1) % 4
		return m, nil
	case keyMatches(msg, k.Select):
		return m.settingsActivate()
	case keyMatches(msg, k.VolUp):
		return m.settingsAdjust(1)
	case keyMatches(msg, k.VolDown):
		return m.settingsAdjust(-1)
	}
	return m, nil
}

func (m model) settingsActivate() (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	switch s.cursor {
	case 0: // theme: open the live-preview picker
		m.openThemePicker()
	case 1: // keybinds: open the action list
		s.mode = settingsModeKeys
		s.keysCursor = 0
		s.captureKey = ""
	case 2: // crossfade: toggle; +/- edits seconds
		s.crossfadeEnabled = !s.crossfadeEnabled
		s.restartRequiredCrossfade = true
		m.saveCrossfadeEnv()
	case 3: // audio cache: toggle; +/- edits size
		s.cacheEnabled = !s.cacheEnabled
		s.restartRequiredCache = true
		m.saveCacheEnv()
	}
	return m, nil
}

func (m model) settingsAdjust(step int) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	switch s.cursor {
	case 2:
		s.crossfadeSeconds = clampCrossfadeSeconds(s.crossfadeSeconds + float64(step))
		s.restartRequiredCrossfade = true
		m.saveCrossfadeEnv()
	case 3:
		s.cacheSizeMB = clampCacheSizeMB(s.cacheSizeMB + int64(step)*256)
		s.restartRequiredCache = true
		m.saveCacheEnv()
	}
	return m, nil
}

func clampCrossfadeSeconds(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 30 {
		return 30
	}
	return v
}

func clampCacheSizeMB(v int64) int64 {
	const min, max = int64(64), int64(4096)
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (m *model) openThemePicker() {
	s := &m.ui.settings
	s.mode = settingsModeTheme
	s.themeBackup = s.themePreset
	s.themeCursor = max(0, slices.Index(settingsThemeOrder, themePresetName(s.themePreset)))
}

func (m model) themePreviewApply(name string) model {
	colors := resolveThemeColors(name, loadThemeOverrides(m.ui.settings.themePath))
	applyTheme(colors)
	m.rethemeBrowseLists()
	m.ui.settings.keysTableDirty = true
	return m
}

func (m model) handleSettingsTheme(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.QueueUp):
		s.themeCursor = (s.themeCursor + len(settingsThemeOrder) - 1) % len(settingsThemeOrder)
		return m.themePreviewApply(settingsThemeOrder[s.themeCursor]), nil
	case keyMatches(msg, k.QueueDown):
		s.themeCursor = (s.themeCursor + 1) % len(settingsThemeOrder)
		return m.themePreviewApply(settingsThemeOrder[s.themeCursor]), nil
	case keyMatches(msg, k.Select):
		picked := settingsThemeOrder[s.themeCursor]
		s.themePreset = themePresetName(picked)
		if err := SaveThemePreset(s.themePath, picked); err != nil {
			slog.Warn("failed saving theme preset", "path", s.themePath, "error", err)
		}
		s.mode = settingsModeRoot
		return m, nil
	case keyMatches(msg, k.CloseModal):
		// Revert to the theme that was active when the picker opened.
		s.themePreset = s.themeBackup
		m = m.themePreviewApply(s.themeBackup)
		s.mode = settingsModeRoot
		return m, nil
	}
	return m, nil
}

func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}

func formatSecondsFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (m *model) saveCrossfadeEnv() {
	m.persistEnv(map[string]string{
		"orpheus_crossfade":         formatEnvBool(m.ui.settings.crossfadeEnabled),
		"orpheus_crossfade_seconds": formatEnvFloat(m.ui.settings.crossfadeSeconds),
	})
}

func (m *model) saveCacheEnv() {
	m.persistEnv(map[string]string{
		"orpheus_audio_cache_enabled": formatEnvBool(m.ui.settings.cacheEnabled),
		"orpheus_audio_cache_size_mb": itoa64(m.ui.settings.cacheSizeMB),
	})
}

func formatEnvBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func formatEnvFloat(v float64) string {
	return formatSecondsFloat(v)
}

func (m *model) persistEnv(values map[string]string) {
	s := &m.ui.settings
	if s.envPath == "" {
		slog.Warn("no .env file found; crossfade/cache changes not persisted")
		return
	}
	if err := config.UpsertEnvFile(s.envPath, values); err != nil {
		slog.Warn("failed writing .env", "path", s.envPath, "error", err)
	}
}

func (m model) handleSettingsKeysMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.CloseModal):
		m.ui.settings.mode = settingsModeRoot
		return m, nil
	case keyMatches(msg, k.QueueUp):
		m.ui.settings.keysCursor = (m.ui.settings.keysCursor + len(settingsKeyActions) - 1) % len(settingsKeyActions)
		return m, nil
	case keyMatches(msg, k.QueueDown):
		m.ui.settings.keysCursor = (m.ui.settings.keysCursor + 1) % len(settingsKeyActions)
		return m, nil
	case keyMatches(msg, k.Select):
		action := settingsKeyActions[m.ui.settings.keysCursor].action
		m.ui.settings.mode = settingsModeCapture
		m.ui.settings.captureKey = action
		return m, nil
	}
	return m, nil
}

func (m model) handleSettingsCapture(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	if msg.String() == "esc" {
		s.mode = settingsModeKeys
		s.captureKey = ""
		s.pendingKey = ""
		return m, nil
	}

	keyName := captureKeyName(msg)
	if s.pendingKey != "" {
		// Two-step capture: a key is armed; enter confirms, any other key
		// replaces the pending one, esc cancels.
		if msg.String() == "enter" {
			err := m.applyCapture(s.captureKey, s.pendingKey)
			s.mode = settingsModeKeys
			s.captureKey = ""
			s.pendingKey = ""
			if err != nil {
				// Save failure drops back to the list; the rebind is still
				// live for the session.
				slog.Warn("rebind not persisted", "error", err)
			}
			return m, nil
		}
		if keyName != "" {
			s.pendingKey = keyName
		}
		return m, nil
	}

	if keyName == "" {
		return m, nil
	}
	s.pendingKey = keyName
	return m, nil
}

func (m *model) applyCapture(action, keyName string) error {
	s := &m.ui.settings
	overrides := LoadKeys(s.keysPath)
	if overrides == nil {
		overrides = map[string][]string{}
	}
	if action == "quit" {
		// ctrl+c always quits: keep it in the stored list like the loader does.
		if !keyContains(overrides[action], "ctrl+c") {
			overrides[action] = append(append([]string{}, overrides[action]...), "ctrl+c")
		}
		if !keyContains(overrides[action], keyName) {
			overrides[action] = []string{keyName, "ctrl+c"}
		} else {
			overrides[action] = []string{keyName}
		}
	} else {
		overrides[action] = []string{keyName}
	}
	if err := SaveKeys(s.keysPath, overrides); err != nil {
		slog.Warn("failed saving keys file", "path", s.keysPath, "error", err)
		return err
	}

	m.ui.keys = newKeysFromConfig(overrides)
	s.conflicts = keyConflictActions(m.ui.keys)
	s.keysTableDirty = true
	return nil
}

func captureKeyName(msg tea.KeyMsg) string {
	s := msg.String()
	if msg.Alt {
		s = "alt+" + s
	}
	switch s {
	case "", "enter", "esc":
		return ""
	}
	return s
}

func keyContains(keys []string, want string) bool {
	return slices.Contains(keys, want)
}

func (m model) settingsOpen() bool {
	return m.ui.settings.open
}

func (m model) settingsKeysTable(w, h int) *table.Model {
	s := &m.ui.settings
	if s.keysTable == nil || s.keysTableDirty {
		// Key column sized to the longest real label: a width-derived
		// column left ~80 empty cells inside full-width modals.
		keyW := 6
		for _, entry := range settingsKeyActions {
			if lw := lipgloss.Width(m.primaryKeyLabel(entry.action)); lw+2 > keyW {
				keyW = lw + 2
			}
		}
		cols := []table.Column{
			{Title: "Action", Width: max(24, w-keyW)},
			{Title: "Key", Width: keyW},
		}
		rows := make([]table.Row, 0, len(settingsKeyActions))
		for _, entry := range settingsKeyActions {
			rows = append(rows, table.Row{entry.label, m.primaryKeyLabel(entry.action)})
		}
		t := table.New(
			table.WithColumns(cols),
			table.WithRows(rows),
			table.WithHeight(h),
		)
		t.SetStyles(tableStyles())
		s.keysTable = &t
		s.keysTableDirty = false
	}
	s.keysTable.SetHeight(h)
	s.keysTable.SetCursor(s.keysCursor)
	return s.keysTable
}

func tableStyles() table.Styles {
	st := table.DefaultStyles()
	st.Header = st.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorDivider).
		BorderBottom(true).
		Bold(false).
		Foreground(colorMutedBlue)
	st.Selected = st.Selected.
		Border(lipgloss.NormalBorder(), false, false, false, false).
		Foreground(colorSelectionFg).
		Background(colorSelectionBg)
	return st
}

func (m model) themePickerView(modalW, innerH int) string {
	s := m.ui.settings
	overrides := loadThemeOverrides(s.themePath)
	listH := max(3, innerH-6)

	// Scrolling window over the registry rows; a blank row between entries
	// keeps the swatch rows from reading as one joined block.
	entries := (listH + 1) / 2
	offset := 0
	if s.themeCursor >= entries {
		offset = s.themeCursor - entries + 1
	}
	var rows []string
	for i := offset; i < min(len(settingsThemeOrder), offset+entries); i++ {
		name := settingsThemeOrder[i]
		colors := resolveThemeColors(name, overrides)
		marker := "  "
		if themePresetName(s.themePreset) == themePresetName(name) {
			marker = "✓"
		}
		bar := swatchBar(themeSwatches(colors))
		row := " " + marker + " " + padCell(name, 14) + " " + bar
		rows = append(rows, modalRow(row, "", s.themeCursor == i, modalW))
		if i < len(settingsThemeOrder)-1 {
			rows = append(rows, modalRow("", "", false, modalW))
		}
	}

	var body strings.Builder
	body.WriteString("\n" + lipgloss.JoinVertical(lipgloss.Left, rows...) + "\n")
	k := m.ui.keys
	hint := hintLine([]key.Binding{
		key.NewBinding(key.WithKeys(k.QueueUp.Keys()...), key.WithHelp(k.QueueUp.Help().Key+"/"+k.QueueDown.Help().Key, "preview")),
		withDesc(k.Select, "save"),
		withDesc(k.CloseModal, "revert"),
	}, modalW-modalContentInset)
	return modalFrame(m.ui.width, m.ui.height, styleModalTitle.Render("Theme"), hint, body.String(), modalW, innerH)
}

func (m model) settingsModalView() string {
	modalW := m.ui.width - 4
	innerH := max(8, m.ui.height-headerH-2)

	s := m.ui.settings

	switch s.mode {
	case settingsModeCapture:
		var body string
		if s.pendingKey == "" {
			body = "\n" + styleTrackPopupLoading.Render("  Press any key to bind \""+settingsActionLabel(s.captureKey)+"\"") + "\n"
		} else {
			pending := styleTrackPopupTitle.Render(shortKeyLabel([]string{s.pendingKey}))
			body = "\n  bind \"" + settingsActionLabel(s.captureKey) + "\" to " + pending + "\n"
		}
		// Capture is a 3-line prompt: a full-height box reads as empty.
		return modalFrame(m.ui.width, m.ui.height, styleModalTitle.Render("Settings"),
			styleModalHint.Render(hintLine([]key.Binding{withDesc(m.ui.keys.Select, "confirm"), withDesc(m.ui.keys.CloseModal, "cancel")}, modalW-modalContentInset)), body, modalW, 7)

	case settingsModeTheme:
		return m.themePickerView(modalW, innerH)

	case settingsModeKeys:
		conflictCount := min(len(s.conflicts), maxConflictHintLines)
		tableH := max(4, innerH-4-conflictCount)
		// modalW-6: the box content (inset 2) minus the table cells' own
		// Padding(0,1) on both columns — wider would wrap inside the box.
		t := m.settingsKeysTable(max(4, modalW-6), tableH)
		var body strings.Builder
		body.WriteString("\n" + t.View() + "\n")
		shown := 0
		for action := range s.conflicts {
			if shown == maxConflictHintLines {
				body.WriteString(styleError.Render("  ⚠ more conflicts…") + "\n")
				break
			}
			body.WriteString(styleError.Render("  ⚠ conflict: "+settingsActionLabel(action)) + "\n")
			shown++
		}
		return modalFrame(m.ui.width, m.ui.height, styleModalTitle.Render("Keybinds"),
			styleModalHint.Render(hintLine([]key.Binding{withDesc(m.ui.keys.Select, "rebind"), withDesc(m.ui.keys.CloseModal, "back")}, modalW-modalContentInset)), body.String(), modalW, innerH)

	default:
		crossfadeGauge := ""
		cacheGauge := ""
		if s.crossfadeEnabled {
			crossfadeGauge = " " + gradientBar(s.crossfadeSeconds/30, gaugeW)
		}
		if s.cacheEnabled {
			cacheGauge = " " + gradientBar(float64(s.cacheSizeMB-64)/float64(4096-64), gaugeW)
		}
		rows := []string{
			modalRow("Theme", m.themeValue(s.themePreset), s.cursor == 0, modalW),
			modalRow("Keybinds", "edit...", s.cursor == 1, modalW),
			modalRow("Crossfade", settingsCrossfadeLabel(&s)+crossfadeGauge, s.cursor == 2, modalW),
			modalRow("Audio cache", settingsCacheLabel(&s)+cacheGauge, s.cursor == 3, modalW),
		}
		body := "\n" + lipgloss.JoinVertical(lipgloss.Left, rows...) + "\n"
		body += "\n" + styleModalHint.Render(hintLine([]key.Binding{withDesc(m.ui.keys.Select, "change"), m.ui.keys.VolUp, m.ui.keys.VolDown}, modalW-modalContentInset)) + "\n"
		if s.restartRequiredCrossfade {
			body += styleError.Render("  crossfade applies on restart") + "\n"
		}
		if s.restartRequiredCache {
			body += styleError.Render("  cache applies on restart") + "\n"
		}
		return modalFrame(m.ui.width, m.ui.height, styleModalTitle.Render("Settings"),
			styleModalHint.Render(hintLine([]key.Binding{m.ui.keys.CloseModal}, modalW-modalContentInset)), body, modalW, innerH)
	}
}

func (m model) themeValue(preset string) string {
	colors := resolveThemeColors(preset, loadThemeOverrides(m.ui.settings.themePath))
	return preset + "  " + swatchBar(themeSwatches(colors))
}

func settingsActionLabel(action string) string {
	for _, entry := range settingsKeyActions {
		if entry.action == action {
			return entry.label
		}
	}
	return action
}

func settingsCrossfadeLabel(s *settingsModel) string {
	label := "off"
	if s.crossfadeEnabled {
		label = "on, " + formatSecondsFloat(s.crossfadeSeconds)
	}
	if s.restartRequiredCrossfade {
		label += "  (restart)"
	}
	return label
}

func settingsCacheLabel(s *settingsModel) string {
	label := "off"
	if s.cacheEnabled {
		label = "on, " + itoa64(s.cacheSizeMB) + "MB"
	}
	if s.restartRequiredCache {
		label += "  (restart)"
	}
	return label
}

func (m model) primaryKeyLabel(action string) string {
	def, ok := defaultKeysForAction(m.ui.keys, action)
	if !ok || len(def) == 0 {
		return "-"
	}
	return shortKeyLabel(def)
}

const maxConflictHintLines = 3

// rethemeBrowseLists re-styles both browser lists for a new palette.
func (m *model) rethemeBrowseLists() {
	// Swap the delegates in place: SetDelegate keeps items, cursor and
	// pagination, so a theme change no longer tears down and rebuilds the
	// list models (the fresh delegate brings a fresh render cache).
	m.browse.playlistList.SetDelegate(newCachedPlaylistDelegate())
	m.browse.albumList.SetDelegate(newCachedPlaylistDelegate())
	applyListStyles(&m.browse.playlistList)
	applyListStyles(&m.browse.albumList)
}
