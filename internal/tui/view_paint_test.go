package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"orpheus/internal/spotify"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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
	m, _, _, _ := newSettingsTestModel(t)
	applyTheme(themePresetState("default"))
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
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
			m.ui.settings.keysTableDirty = true
			return m.settingsModalView()
		}},
		{"theme", func(m *model) string {
			m.ui.settings.mode = settingsModeTheme
			return m.settingsModalView()
		}},
		{"help", func(m *model) string {
			m.ui.helpOpen = true
			m.ensureHelpViewport()
			return m.helpModalView()
		}},
	}
	for _, variant := range variants {
		for _, size := range [][2]int{{100, 40}, {80, 30}, {60, 24}} {
			m := withPaintTestModel(t, size[0], size[1])
			m.ui.settings.open = true
			out := variant.render(&m)
			holes, samples := countPaintHoles(out)
			if holes != 0 {
				t.Fatalf("%s at %dx%d: %d unpainted text runs, samples %q", variant.name, size[0], size[1], holes, samples)
			}
		}
	}
}

func TestFramePaintNeverLosesBackground(t *testing.T) {
	for _, size := range [][2]int{{100, 40}, {80, 30}, {60, 24}} {
		m := withPaintTestModel(t, size[0], size[1])
		out := m.View()
		holes, samples := countPaintHoles(out)
		if holes != 0 {
			t.Fatalf("frame at %dx%d: %d unpainted text runs, samples %q", size[0], size[1], holes, samples)
		}
	}
}

func TestSelectedRowKeepsHighlightThroughFragments(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	// a styled fragment inside a selected row, like the real gauge bar
	row := "Crossfade|" + lipgloss.NewStyle().Foreground(colorBlue).Render("██████") + "|end"
	out := modalRow(row, "", true, 96)

	selBg := bgSequence(colorSelectionBg)
	selParams := strings.TrimPrefix(selBg, "\x1b[")
	reset := "\x1b[0m"
	if !strings.Contains(out[:80], selParams) {
		t.Fatalf("selection bg missing at the row start: %q", out)
	}
	pos := 0
	for {
		rel := strings.Index(out[pos:], reset)
		if rel < 0 {
			break
		}
		idx := pos + rel
		after := out[idx+len(reset):]
		segment := after
		if next := strings.Index(after, reset); next >= 0 {
			segment = after[:next]
		}
		if !strings.Contains(segment, selBg) {
			t.Fatalf("selection bg lost after a fragment reset at %d: %q", idx, out)
		}
		pos = idx + len(reset)
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
		m.ui.trackPopupList = newTrackPopupList(m.ui.width, m.ui.height)
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
