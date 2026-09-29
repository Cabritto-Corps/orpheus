package tui

import (
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"orpheus/internal/config"
)

// newSettingsModel seeds the settings state from the config; the preset
// name is the one the theme loader actually resolved (the theme.json
// marker wins over orpheus_theme), so the picker and editor always
// operate on the theme that is really running.
func newSettingsModel(cfg config.Config, resolvedPreset string) settingsModel {
	return settingsModel{
		themePreset:      resolvedPreset,
		keysPath:         cfg.KeysPath,
		themePath:        cfg.ThemePath,
		configPath:       cfg.SettingsPath,
		crossfadeEnabled: cfg.Crossfade,
		crossfadeSeconds: cfg.CrossfadeSeconds,
		cacheEnabled:     cfg.AudioCacheEnabled,
		cacheSizeMB:      cfg.AudioCacheSizeMB,
		imageStyle:       imageStyleOrDefault(cfg.ImageStyle),
		imageStyleSet:    config.NormalizeImageStyle(cfg.ImageStyle) != "",
	}
}

// cachedThemeOverrides returns the parsed theme.json overrides: the cache
// populated on the update paths (settings open, saves) so per-frame view
// paths do not re-read the file at the 200ms tick.
func (m model) cachedThemeOverrides() map[string]any {
	if o := m.ui.settings.themeOverrides; o != nil {
		return o
	}
	return loadThemeOverrides(m.ui.settings.themePath)
}

func (m model) openSettings() (tea.Model, tea.Cmd) {
	m.ui.settings.themeOverrides = loadThemeOverrides(m.ui.settings.themePath)
	m.ui.settings.open = true
	m.ui.settings.mode = settingsModeRoot
	m.ui.settings.cursor = 0
	m.ui.settings.captureKey = ""
	m.ui.settings.pendingKey = ""
	m.ui.settings.conflicts = keyConflictActions(m.ui.keys)
	return m, nil
}

func (m model) handleSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings

	switch s.mode {
	case settingsModeCapture:
		return m.handleSettingsCapture(msg)
	case settingsModeThemeOptions:
		return m.handleSettingsThemeOptions(msg)
	case settingsModeKeys:
		return m.handleSettingsKeysMode(msg)
	case settingsModeTheme:
		return m.handleSettingsTheme(msg)
	default:
		return m.handleSettingsRoot(msg)
	}
}

func (m model) handleSettingsRoot(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := m.ui.keys
	s := &m.ui.settings
	switch {
	case keyMatches(msg, k.CloseModal):
		s.open = false
		return m, nil
	case keyMatches(msg, k.QueueUp):
		s.cursor = (s.cursor + len(settingsRootRows) - 1) % len(settingsRootRows)
		return m, nil
	case keyMatches(msg, k.QueueDown):
		s.cursor = (s.cursor + 1) % len(settingsRootRows)
		return m, nil
	case keyMatches(msg, k.Select):
		return settingsRootRows[s.cursor].activate(m)
	case keyMatches(msg, k.VolUp):
		if adjust := settingsRootRows[s.cursor].adjust; adjust != nil {
			return adjust(m, 1)
		}
	case keyMatches(msg, k.VolDown):
		if adjust := settingsRootRows[s.cursor].adjust; adjust != nil {
			return adjust(m, -1)
		}
	}
	return m, nil
}

func (m model) handleSettingsThemeOptions(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.CloseModal):
		return m.themeOptionsRevert()
	case keyMatches(msg, k.QueueUp):
		s.optionsCursor = (s.optionsCursor + themeOptionsRowCount() - 1) % themeOptionsRowCount()
		return m, nil
	case keyMatches(msg, k.QueueDown):
		s.optionsCursor = (s.optionsCursor + 1) % themeOptionsRowCount()
		return m, nil
	case keyMatches(msg, k.Select):
		if themeOptionsRow(s.optionsCursor).kind == optionSave {
			return m.themeOptionsSave()
		}
		if themeOptionsRow(s.optionsCursor).kind == optionReset {
			return m.themeOptionsReset()
		}
		return m.themeOptionsCycle(1)
	case keyMatches(msg, k.VolUp):
		return m.themeOptionsCycle(1)
	case keyMatches(msg, k.VolDown):
		return m.themeOptionsCycle(-1)
	}
	return m, nil
}

// settingsRowKind classifies a settings root row: open rows lead to a
// submenu and ignore the volume keys; adjustable rows act on Select
// and step on the volume keys.
type settingsRowKind int

const (
	settingsRowOpen settingsRowKind = iota
	settingsRowAdjustable
)

// settingsRow describes one settings root row: label, right-column
// value (gauges and suffixes included), what Select does, what the
// volume keys do (nil = ignored), and an optional bottom warning.
// The menu reads this table everywhere — cursor wrap, rendering and
// key dispatch — so adding a row is one entry here.
type settingsRow struct {
	label    string
	kind     settingsRowKind
	value    func(m model) string
	activate func(m model) (tea.Model, tea.Cmd)
	adjust   func(m model, step int) (tea.Model, tea.Cmd)
	notice   func(s *settingsModel) string
}

var settingsRootRows = []settingsRow{
	{
		label: "Theme",
		kind:  settingsRowOpen,
		value: func(m model) string { return m.themeValue(m.ui.settings.themePreset) },
		activate: func(m model) (tea.Model, tea.Cmd) {
			m.openThemePicker()
			return m, nil
		},
	},
	{
		label: "Theme options",
		kind:  settingsRowOpen,
		value: func(m model) string { return "edit..." },
		activate: func(m model) (tea.Model, tea.Cmd) {
			m.openThemeOptions()
			return m, nil
		},
	},
	{
		label: "Keybinds",
		kind:  settingsRowOpen,
		value: func(m model) string { return "edit..." },
		activate: func(m model) (tea.Model, tea.Cmd) {
			m.ui.settings.mode = settingsModeKeys
			m.ui.settings.keysCursor = 0
			m.ui.settings.captureKey = ""
			return m, nil
		},
	},
	{
		label: "Crossfade",
		kind:  settingsRowAdjustable,
		value: func(m model) string {
			s := m.ui.settings
			gauge := ""
			if s.crossfadeEnabled {
				gauge = " " + m.styles.gradientBar(s.crossfadeSeconds/30, gaugeW)
			}
			return settingsCrossfadeLabel(&s) + gauge
		},
		activate: func(m model) (tea.Model, tea.Cmd) {
			s := &m.ui.settings
			s.crossfadeEnabled = !s.crossfadeEnabled
			s.restartRequiredCrossfade = true
			m.saveAppSettings()
			return m, nil
		},
		adjust: func(m model, step int) (tea.Model, tea.Cmd) {
			s := &m.ui.settings
			s.crossfadeSeconds = clampCrossfadeSeconds(s.crossfadeSeconds + float64(step))
			s.restartRequiredCrossfade = true
			m.saveAppSettings()
			return m, nil
		},
		notice: func(s *settingsModel) string {
			if s.restartRequiredCrossfade {
				return "crossfade applies on restart"
			}
			return ""
		},
	},
	{
		label: "Audio cache",
		kind:  settingsRowAdjustable,
		value: func(m model) string {
			s := m.ui.settings
			gauge := ""
			if s.cacheEnabled {
				gauge = " " + m.styles.gradientBar(float64(s.cacheSizeMB-64)/float64(4096-64), gaugeW)
			}
			return settingsCacheLabel(&s) + gauge
		},
		activate: func(m model) (tea.Model, tea.Cmd) {
			s := &m.ui.settings
			s.cacheEnabled = !s.cacheEnabled
			s.restartRequiredCache = true
			m.saveAppSettings()
			return m, nil
		},
		adjust: func(m model, step int) (tea.Model, tea.Cmd) {
			s := &m.ui.settings
			s.cacheSizeMB = clampCacheSizeMB(s.cacheSizeMB + int64(step)*256)
			s.restartRequiredCache = true
			m.saveAppSettings()
			return m, nil
		},
		notice: func(s *settingsModel) string {
			if s.restartRequiredCache {
				return "cache applies on restart"
			}
			return ""
		},
	},
	{
		label: "Images",
		kind:  settingsRowAdjustable,
		value: func(m model) string {
			s := m.ui.settings
			return settingsImageLabel(&s)
		},
		activate: func(m model) (tea.Model, tea.Cmd) {
			s := &m.ui.settings
			s.imageStyle = cycleImageStyle(s.imageStyle, 1)
			s.imageStyleSet = true
			m.saveAppSettings()
			return m.applyImageStyle()
		},
		adjust: func(m model, step int) (tea.Model, tea.Cmd) {
			s := &m.ui.settings
			s.imageStyle = cycleImageStyle(s.imageStyle, step)
			s.imageStyleSet = true
			m.saveAppSettings()
			return m.applyImageStyle()
		},
	},
}

var imageStyleChoices = []string{config.ImageStyleRendered, config.ImageStylePixelated}

func cycleImageStyle(style string, step int) string {
	return cycleValue(imageStyleChoices, imageStyleOrDefault(style), step)
}

func settingsImageLabel(s *settingsModel) string {
	return imageStyleOrDefault(s.imageStyle)
}

func (m model) applyImageStyle() (tea.Model, tea.Cmd) {
	return m.applyImageStyleWithEnv(os.Getenv)
}

func (m model) applyImageStyleWithEnv(getenv func(string) string) (tea.Model, tea.Cmd) {
	if m.ui.imgs == nil {
		return m, nil
	}
	style := imageStyleOrDefault(m.ui.settings.imageStyle)
	m.ui.imgs.setImageStyle(style, true, getenv)
	// Style-specific supervision belongs to the previous attempt: clear it
	// so a stored kitty failure cannot override the newly selected style.
	m.ui.cover.kittyFellBack = false
	m.ui.cover.kittyRecoveryStreak = 0
	m.ui.cover.playerCoverFailStreak = 0
	return m, m.reloadCurrentKittyCoverCmd()
}

// reloadCurrentKittyCoverCmd re-encodes the visible cover after a style
// switch clears the kitty payload cache: source images are retained, so
// the current one can be framed without waiting for a navigation or
// track change to reload it. Only cached images qualify; anything else
// keeps the normal cover-loading path.
func (m model) reloadCurrentKittyCoverCmd() tea.Cmd {
	if m.ui.imgs == nil || m.ui.imgs.protocolForRender() != imageProtocolKitty {
		return nil
	}
	var url string
	switch m.ui.activeTab {
	case tabPlaylists:
		url = selectedImageURLFromList(m.browse.playlistList)
	case tabAlbums:
		url = selectedImageURLFromList(m.browse.albumList)
	case tabPlayer:
		if m.transport.status != nil {
			url = m.transport.status.AlbumImageURL
		}
	}
	if strings.TrimSpace(url) == "" {
		return nil
	}
	img, ok := m.ui.imgs.getImage(url)
	if !ok {
		return nil
	}
	if cmd := m.loadImageCmd(url, true); cmd != nil {
		return cmd
	}
	// No loader is available in this path (notably tests): encode inline so
	// the newly selected style still applies to the retained source image.
	if err := m.ui.imgs.ensureKittyEncoding(url, img); err != nil {
		slog.Warn("kitty re-encode failed", "url", url, "error", err)
		return nil
	}
	m.ui.imgs.forceKittyRedraw()
	return nil
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

func (m model) themePreviewApply(name string) (tea.Model, tea.Cmd) {
	return m.themeOptionsApply(resolveThemeState(name, m.cachedThemeOverrides()))
}

func (m model) handleSettingsTheme(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	k := m.ui.keys
	switch {
	case keyMatches(msg, k.QueueUp):
		s.themeCursor = (s.themeCursor + len(settingsThemeOrder) - 1) % len(settingsThemeOrder)
		return m.themePreviewApply(settingsThemeOrder[s.themeCursor])
	case keyMatches(msg, k.QueueDown):
		s.themeCursor = (s.themeCursor + 1) % len(settingsThemeOrder)
		return m.themePreviewApply(settingsThemeOrder[s.themeCursor])
	case keyMatches(msg, k.Select):
		picked := settingsThemeOrder[s.themeCursor]
		s.themePreset = themePresetName(picked)
		if err := SaveThemePreset(s.themePath, picked); err != nil {
			s.saveErr = "theme save failed: " + err.Error()
			slog.Warn("failed saving theme preset", "path", s.themePath, "error", err)
			return m, nil
		}
		s.saveErr = ""
		m.ui.settings.themeOverrides = nil
		s.mode = settingsModeRoot
		m.refreshLikedSongsArt()
		return m, nil
	case keyMatches(msg, k.CloseModal):
		// Revert to the theme that was active when the picker opened.
		// The mode must be set before the preview apply: value receivers
		// copy the model, so the returned copy must already carry it.
		s.mode = settingsModeRoot
		s.themePreset = s.themeBackup
		return m.themeOptionsApplyAndRefresh(resolveThemeState(s.themeBackup, m.cachedThemeOverrides()))
	}
	return m, nil
}

func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}

func formatSecondsFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (m *model) saveAppSettings() {
	s := &m.ui.settings
	enabled := s.crossfadeEnabled
	seconds := s.crossfadeSeconds
	cacheEnabled := s.cacheEnabled
	sizeMB := s.cacheSizeMB
	var images *config.ImageStyleSettings
	if s.imageStyleSet {
		style := imageStyleOrDefault(s.imageStyle)
		images = &config.ImageStyleSettings{Style: &style}
	}
	if err := config.SaveAppSettings(s.configPath, config.AppSettings{
		Crossfade:  &config.CrossfadeSettings{Enabled: &enabled, Seconds: &seconds},
		AudioCache: &config.AudioCacheSettings{Enabled: &cacheEnabled, SizeMB: &sizeMB},
		Images:     images,
	}); err != nil {
		s.saveErr = "save failed: " + err.Error()
		slog.Warn("failed writing config.json", "path", s.configPath, "error", err)
		return
	}
	s.saveErr = ""
}

func (m model) handleSettingsKeysMode(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func (m model) handleSettingsCapture(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := &m.ui.settings
	if isCancelPress(msg) {
		s.mode = settingsModeKeys
		s.captureKey = ""
		s.pendingKey = ""
		return m, nil
	}

	keyName := captureKeyName(msg)
	if s.pendingKey != "" {
		// Two-step capture: a key is armed; enter confirms, any other key
		// replaces the pending one, esc cancels.
		if isConfirmPress(msg) {
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
	return nil
}

func captureKeyName(msg tea.KeyPressMsg) string {
	k := msg.Key()
	// Esc cancels and Enter confirms the pending rebind, so neither is
	// capturable; identity keeps this true however the close and select
	// actions are rebound.
	if k.Code == tea.KeyEscape || k.Code == tea.KeyEnter {
		return ""
	}
	// A lone modifier carries no base key and can never match a binding.
	if isModifierCode(k.Code) {
		return ""
	}
	if k.Mod == 0 {
		return msg.String()
	}
	// Modified keys render in canonical modifier order, so an
	// alt-modified key never gains a second "alt+" prefix.
	return msg.Keystroke()
}

// isModifierCode reports the bare left/right modifier keys.

func keyContains(keys []string, want string) bool {
	return slices.Contains(keys, want)
}

func (m model) settingsKeysTable(w, h int) *table.Model {
	// The table is rebuilt on every render: rows mirror the live keyMap, so
	// a rebind shows up in the next frame with no dirty flag or cache. (A
	// memo used to live here, but its writes landed on render-path copies
	// and could never engage — every frame rebuilt anyway.)
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
	t.SetStyles(m.styles.tableStyles())
	t.SetHeight(h)
	// Width is load-bearing, not cosmetic: the bubbles table renders its
	// rows through a viewport that drops everything when its width is 0.
	t.SetWidth(w)
	t.SetCursor(m.ui.settings.keysCursor)
	return &t
}

func (s *themeStyles) tableStyles() table.Styles {
	st := table.DefaultStyles()
	st.Header = st.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(s.colorDivider).
		BorderBottom(true).
		Bold(false).
		Foreground(s.colorMutedBlue)
	st.Selected = st.Selected.
		Border(lipgloss.NormalBorder(), false, false, false, false).
		Foreground(s.colorSelectionFg).
		Background(s.colorSelectionBg)
	return st
}

func (m model) themePickerView(modalW, innerH int) string {
	s := m.ui.settings
	overrides := m.cachedThemeOverrides()
	listH := max(3, innerH-6)

	// Scrolling window over the registry rows; a blank row between entries
	// keeps the swatch rows from reading as one joined block.
	entries := (listH + 1) / 2
	shown, offset := scrollRows(settingsThemeOrder, s.themeCursor, entries)
	var rows []string
	for j, name := range shown {
		i := offset + j
		colors := resolveThemeColors(name, overrides)
		marker := "  "
		if themePresetName(s.themePreset) == themePresetName(name) {
			marker = "✓"
		}
		bar := swatchBar(themeSwatches(colors))
		row := " " + marker + " " + padCell(name, 14) + " " + bar
		rows = append(rows, m.styles.modalRow(row, "", s.themeCursor == i, modalW))
		if i < len(settingsThemeOrder)-1 {
			rows = append(rows, m.styles.modalRow("", "", false, modalW))
		}
	}

	var body strings.Builder
	body.WriteString("\n" + lipgloss.JoinVertical(lipgloss.Left, rows...) + "\n")
	k := m.ui.keys
	hint := m.styles.hintLine([]key.Binding{
		key.NewBinding(key.WithKeys(k.QueueUp.Keys()...), key.WithHelp(k.QueueUp.Help().Key+"/"+k.QueueDown.Help().Key, "preview")),
		withDesc(k.Select, "save"),
		withDesc(k.CloseModal, "revert"),
	}, modalW-modalContentInset)
	return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Theme"), hint, body.String(), modalW, innerH)
}

func (m model) settingsModalView() string {
	modalW := m.ui.width - 4
	innerH := max(8, m.ui.height-headerH-2)

	s := m.ui.settings

	switch s.mode {
	case settingsModeThemeOptions:
		return m.themeOptionsView(modalW, innerH)

	case settingsModeCapture:
		var body string
		if s.pendingKey == "" {
			body = "\n" + m.styles.styleTrackPopupLoading.Render("  Press any key to bind \""+settingsActionLabel(s.captureKey)+"\"") + "\n"
		} else {
			pending := m.styles.styleTrackPopupTitle.Render(shortKeyLabel([]string{s.pendingKey}))
			body = "\n  bind \"" + settingsActionLabel(s.captureKey) + "\" to " + pending + "\n"
		}
		// Capture is a 3-line prompt: a full-height box reads as empty.
		return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Settings"),
			m.styles.styleModalHint.Render(m.styles.hintLine([]key.Binding{withDesc(m.ui.keys.Select, "confirm"), withDesc(m.ui.keys.CloseModal, "cancel")}, modalW-modalContentInset)), body, modalW, 7)

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
		for _, action := range sortedConflictActions(s.conflicts) {
			if shown == maxConflictHintLines {
				body.WriteString(m.styles.styleError.Render("  ⚠ more conflicts…") + "\n")
				break
			}
			body.WriteString(m.styles.styleError.Render("  ⚠ conflict: "+settingsActionLabel(action)) + "\n")
			shown++
		}
		return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Keybinds"),
			m.styles.styleModalHint.Render(m.styles.hintLine([]key.Binding{withDesc(m.ui.keys.Select, "rebind"), withDesc(m.ui.keys.CloseModal, "back")}, modalW-modalContentInset)), body.String(), modalW, innerH)

	default:
		rows := make([]string, 0, len(settingsRootRows))
		for i, row := range settingsRootRows {
			rows = append(rows, m.styles.modalRow(row.label, row.value(m), s.cursor == i, modalW))
		}
		var body strings.Builder
		body.WriteString("\n" + lipgloss.JoinVertical(lipgloss.Left, rows...) + "\n")
		body.WriteString("\n" + m.styles.styleModalHint.Render(m.styles.hintLine([]key.Binding{withDesc(m.ui.keys.Select, "change"), m.ui.keys.VolUp, m.ui.keys.VolDown}, modalW-modalContentInset)) + "\n")
		for _, row := range settingsRootRows {
			if row.notice == nil {
				continue
			}
			if hint := row.notice(&s); hint != "" {
				body.WriteString(m.styles.styleError.Render("  "+hint) + "\n")
			}
		}
		if s.saveErr != "" {
			body.WriteString(m.styles.styleError.Render("  ⚠ "+truncate(s.saveErr, modalW-modalContentInset-2)) + "\n")
		}
		for i, w := range config.Warnings() {
			if i == 2 {
				body.WriteString(m.styles.styleError.Render("  ⚠ more config warnings…") + "\n")
				break
			}
			body.WriteString(m.styles.styleError.Render("  ⚠ "+truncate(w, modalW-modalContentInset-2)) + "\n")
		}
		return m.styles.modalFrame(m.ui.width, m.ui.height, m.styles.styleModalTitle.Render("Settings"),
			m.styles.styleModalHint.Render(m.styles.hintLine([]key.Binding{m.ui.keys.CloseModal}, modalW-modalContentInset)), body.String(), modalW, innerH)
	}
}

func (m model) themeValue(preset string) string {
	colors := resolveThemeColors(preset, m.cachedThemeOverrides())
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
	m.browse.playlistList.SetDelegate(newCachedPlaylistDelegate(m.styles, m.nowPlaying))
	m.browse.albumList.SetDelegate(newCachedPlaylistDelegate(m.styles, m.nowPlaying))
	applyListStyles(&m.browse.playlistList, m.styles)
	applyListStyles(&m.browse.albumList, m.styles)
}
