package tui

import (
	"log/slog"
	"strconv"
	"strings"

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
	return m, nil
}

func (m model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings

	switch s.mode {
	case settingsModeCapture:
		return m.handleSettingsCapture(msg)
	case settingsModeKeys:
		return m.handleSettingsKeysMode(msg)
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
	case 0: // theme: cycle presets, live-apply
		next := settingsNextThemePreset(s.themePreset)
		s.themePreset = next
		applyTheme(themePreset(next))
		if err := SaveThemePreset(s.themePath, next); err != nil {
			slog.Warn("failed saving theme preset", "path", s.themePath, "error", err)
		}
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

func settingsNextPresetIdx(preset string) int {
	for i, name := range settingsThemeOrder {
		if name == themePresetName(preset) {
			return i
		}
	}
	return 0
}

func settingsNextThemePreset(current string) string {
	return settingsThemeOrder[(settingsNextPresetIdx(current)+1)%len(settingsThemeOrder)]
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
		return m, nil
	}

	keyName := captureKeyName(msg)
	if keyName == "" {
		return m, nil
	}

	overrides := LoadKeys(s.keysPath)
	if overrides == nil {
		overrides = map[string][]string{}
	}
	action := s.captureKey
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
		return m, nil
	}

	m.ui.keys = newKeysFromConfig(overrides)
	s.mode = settingsModeKeys
	s.captureKey = ""
	return m, nil
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
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func (m model) settingsOpen() bool {
	return m.ui.settings.open
}

func (m model) settingsModalView() string {
	modalW := min(m.ui.width-8, 52)
	bodyH := m.ui.height - headerH - tabBarH - 2
	innerH := max(bodyH-4, 10)

	title := styleModalTitle.Render("Settings")
	s := m.ui.settings

	var body string
	switch s.mode {
	case settingsModeCapture:
		body = "\n  Press any key to bind \"" + settingsKeyActions[s.keysCursor].action + "\"\n\n  esc: cancel"
	case settingsModeKeys:
		var b strings.Builder
		b.WriteString("\n")
		start := max(0, s.keysCursor-(innerH-6)/2)
		end := min(start+innerH-5, len(settingsKeyActions))
		for i := start; i < end; i++ {
			entry := settingsKeyActions[i]
			row := "  "
			if i == s.keysCursor {
				row = " > "
			}
			key := m.primaryKeyLabel(entry.action)
			body += b.String()
			body += row + padTo(entry.label, 24) + key + "\n"
		}
		body += "\n  enter: rebind   esc: back"
	default:
		rows := [4]string{
			"  Theme          " + s.themePreset,
			"  Keybinds       edit...",
			"  Crossfade      " + settingsCrossfadeLabel(&s),
			"  Audio cache    " + settingsCacheLabel(&s),
		}
		body = "\n"
		for i, row := range rows {
			if i == s.cursor {
				body += " > " + row[3:] + "\n"
			} else {
				body += row + "\n"
			}
		}
		body += "\n  enter: cycle/toggle   +/-: adjust   esc: close\n"
		if s.restartRequiredCrossfade {
			body += styleError.Render("  crossfade change applies on restart") + "\n"
		}
		if s.restartRequiredCache {
			body += styleError.Render("  cache change applies on restart") + "\n"
		}
		if s.cursor == 0 {
			body += styleTrackPopupHint.Render("  edit theme.json for per-color overrides") + "\n"
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		body,
	)
	if s.mode == settingsModeRoot {
		hint := styleTrackPopupHint.Render("  o/esc: close")
		content = lipgloss.JoinVertical(lipgloss.Left, title, body, hint)
	}

	box := styleModalBox.
		Width(modalW).
		Height(innerH).
		Render(content)
	return lipgloss.Place(m.ui.width, bodyH, lipgloss.Center, lipgloss.Center, box)
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

func padTo(s string, width int) string {
	for len(s) < width {
		s += " "
	}
	return s
}
