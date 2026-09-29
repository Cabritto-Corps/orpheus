package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"orpheus/internal/spotify"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
)

// The view paints backgrounds by re-asserting them after every style reset,
// so any rendered row must never print text with no background set. This
// replays the SGR state and counts violations (holes), which would show as
// page-colored notches inside themed bands.
var paintTokenRe = regexp.MustCompile(`(\x1b\[[0-9;]*m)|([^\x1b]+)`)

func countPaintHoles(out string) (int, []string) {
	bgOn := false
	holes := 0
	samples := []string{}
	for _, tok := range paintTokenRe.FindAllString(out, -1) {
		if strings.HasPrefix(tok, "\x1b[") {
			if strings.Contains(tok, "48;") {
				bgOn = true
			} else if tok == "\x1b[0m" || tok == "\x1b[m" {
				bgOn = false
			}
			continue
		}
		if !bgOn && strings.TrimSpace(tok) != "" {
			holes++
			if len(samples) < 2 {
				trimmed := strings.TrimSpace(tok)
				samples = append(samples, trimmed[:min(30, len(trimmed))])
			}
		}
	}
	return holes, samples
}

func withPaintTestModel(t *testing.T, w, h int) model {
	return withPaintTestModelStyle(t, w, h, "solid")
}

// withPaintTestModelStyle renders under one background mode: solid is
// the shipped default, transparent the unpainted one. Styles live on the
// returned model, so mode switches cannot leak between tests.
func withPaintTestModelStyle(t *testing.T, w, h int, style string) model {
	t.Helper()
	m, _, _, _ := newSettingsTestModel(t)
	st := themePresetState("default")
	st.backgrounds.Style = style
	m.styles = buildThemeStyles(st)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "0")
	t.Cleanup(func() {
		t.Setenv("NO_COLOR", "1")
	})
	m.ui.width = w
	m.ui.height = h
	return m
}

func TestModalPaintNeverLosesBackground(t *testing.T) {
	variants := []struct {
		name   string
		render func(*model) string
	}{
		{"settings", func(m *model) string {
			m.openSettings()
			return m.settingsModalView()
		}},
		{"keys", func(m *model) string {
			m.ui.settings.mode = settingsModeKeys
			return m.settingsModalView()
		}},
		{"theme", func(m *model) string {
			m.ui.settings.mode = settingsModeTheme
			return m.settingsModalView()
		}},
		{"theme-options", func(m *model) string {
			m.openSettings()
			m.openThemeOptions()
			return m.settingsModalView()
		}},
		{"help", func(m *model) string {
			m.ui.helpOpen = true
			m.ensureHelpViewport()
			return m.helpModalView()
		}},
	}
	for _, variant := range variants {
		// The painted mode only: in transparent mode the backdrop keeps
		// no page fill by design (see TestTransparentModalKeepsChrome).
		for _, style := range []string{"solid"} {
			for _, size := range [][2]int{{100, 40}, {80, 30}, {60, 24}} {
				m := withPaintTestModelStyle(t, size[0], size[1], style)
				m.ui.settings.open = true
				out := variant.render(&m)
				holes, samples := countPaintHoles(out)
				if holes != 0 {
					t.Fatalf("%s/%s at %dx%d: %d unpainted text runs, samples %q", style, variant.name, size[0], size[1], holes, samples)
				}
			}
		}
	}
}

func TestFramePaintNeverLosesBackground(t *testing.T) {
	// The painted mode only: transparent frames are unpainted by design
	// (covered by TestTransparentFramePaintsNoPageBackground instead).
	for _, style := range []string{"solid"} {
		for _, size := range [][2]int{{100, 40}, {80, 30}, {60, 24}} {
			m := withPaintTestModelStyle(t, size[0], size[1], style)
			out := m.View().Content
			holes, samples := countPaintHoles(out)
			if holes != 0 {
				t.Fatalf("%s frame at %dx%d: %d unpainted text runs, samples %q", style, size[0], size[1], holes, samples)
			}
		}
	}
}

// TestTransparentFramePaintsNoPageBackground pins the transparent
// contract: no page surface anywhere in the frame, while overlay chrome
// (here: the active-tab highlight) keeps working.
func TestTransparentFramePaintsNoPageBackground(t *testing.T) {
	m := withPaintTestModelStyle(t, 100, 40, "transparent")
	out := m.View().Content
	pageSeq := m.styles.bgSequence(m.styles.colorPage)
	if pageSeq == "" {
		t.Fatal("need TrueColor background sequences")
	}
	if strings.Contains(out, pageSeq) {
		t.Fatal("transparent frame paints the page background")
	}
	// Chrome keeps working: match the bare SGR params, not the standalone
	// sequence — lipgloss merges the tab highlight's fg+bg into one
	// combined sequence (the TestSelectedRowKeepsHighlightThroughFragments
	// pattern).
	if dimParams := strings.TrimPrefix(m.styles.bgSequence(m.styles.colorDimBlue), "\x1b["); !strings.Contains(out, dimParams) {
		t.Fatal("transparent frame lost the active-tab highlight")
	}
}

// TestTransparentModalKeepsChrome: modals are floating chrome, not frame
// zones, so the box, the dim pattern and the selection highlight all
// survive transparent mode — only the page fill goes away.
func TestTransparentModalKeepsChrome(t *testing.T) {
	m := withPaintTestModelStyle(t, 100, 40, "transparent")
	m.openSettings()
	m.ui.settings.open = true
	out := m.settingsModalView()
	if !strings.Contains(out, "░") {
		t.Fatal("transparent modal lost the dim backdrop")
	}
	if !strings.Contains(out, "╭") && !strings.Contains(out, "┌") {
		t.Fatal("transparent modal lost its border")
	}
	if !strings.Contains(out, m.styles.bgSequence(m.styles.colorSelectionBg)) {
		t.Fatal("transparent modal lost the selection highlight")
	}
	if strings.Contains(out, m.styles.bgSequence(m.styles.colorPage)) {
		t.Fatal("transparent modal paints the page background")
	}
}

func TestSelectedRowKeepsHighlightThroughFragments(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "0")

	// a styled fragment inside a selected row, like the real gauge bar
	st := buildThemeStyles(themePresetState("default"))
	row := "Crossfade|" + lipgloss.NewStyle().Foreground(st.colorBlue).Render("██████") + "|end"
	out := st.modalRow(row, "", true, 96)

	selBg := st.bgSequence(st.colorSelectionBg)
	selParams := strings.TrimPrefix(selBg, "\x1b[")
	// Reset spellings the renderers emit: v1 terminates styles with
	// \x1b[0m, v2 abbreviates to \x1b[m. Matching one literal passed
	// vacuously after the migration (the loop below never iterated),
	// so the guard matches both.
	nextReset := func(s string) (idx, length int) {
		idx, length = -1, 0
		for _, r := range []string{"\x1b[0m", "\x1b[m"} {
			if i := strings.Index(s, r); i >= 0 && (idx < 0 || i < idx) {
				idx, length = i, len(r)
			}
		}
		return idx, length
	}
	if !strings.Contains(out[:80], selParams) {
		t.Fatalf("selection bg missing at the row start: %q", out)
	}
	pos := 0
	for {
		rel, ln := nextReset(out[pos:])
		if rel < 0 {
			break
		}
		idx := pos + rel
		after := out[idx+ln:]
		segment := after
		if j, _ := nextReset(after); j >= 0 {
			segment = after[:j]
		}
		if !strings.Contains(segment, selBg) {
			t.Fatalf("selection bg lost after a fragment reset at %d: %q", idx, out)
		}
		pos = idx + ln
	}
}

func TestTrackPopupPaintNeverLosesBackground(t *testing.T) {
	for _, w := range []int{60, 70, 80, 100, 140} {
		m := withPaintTestModel(t, w, 40)
		items := make([]spotify.QueueItem, 0, 6)
		for i := range 6 {
			items = append(items, spotify.QueueItem{ID: fmt.Sprintf("spotify:track:%011d", i), Name: fmt.Sprintf("Track %d", i), Artist: "Artist", DurationMS: 200000})
		}
		m.ui.trackPopupOpen = true
		m.ui.trackPopupName = "Some Playlist"
		m.ui.trackPopupItems = items
		_, listW, listH := popupModalSize(m.ui.width, m.ui.height)
		m.ui.trackPopupList = newTrackPopupList(m.styles, m.nowPlaying, m.ui.width, m.ui.height)
		m.ui.trackPopupList.SetSize(listW, listH)
		listItems := make([]list.Item, 0, len(items))
		for _, it := range items {
			listItems = append(listItems, trackItem{item: it})
		}
		m.ui.trackPopupList.SetItems(listItems)

		out := m.trackPopupView()
		holes, samples := countPaintHoles(out)
		if holes != 0 {
			t.Fatalf("popup at w=%d: %d unpainted text runs, samples %q", w, holes, samples)
		}
	}
}
