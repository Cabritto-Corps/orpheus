package tui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func teaDown() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyDown} }

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer()
}

func sendTop(m model, msg tea.KeyMsg) (model, tea.Cmd) {
	next, cmd := m.handleKey(msg)
	return next.(model), cmd
}

func ctrlC() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlC} }

func TestHelpGroupsDerivedFromRegistry(t *testing.T) {
	if got, want := helpGroupTitles(), []string{"Playback", "Navigation", "Queue"}; !slices.Equal(got, want) {
		t.Fatalf("derived groups = %v, want %v", got, want)
	}
	m, _, _, _ := newSettingsTestModel(t)
	body := m.helpGroupedBody(120, 40)
	for _, title := range []string{"Playback", "Navigation", "Queue"} {
		if !strings.Contains(body, title) {
			t.Fatalf("help body missing group %q", title)
		}
	}
	// A fourth group must render instead of vanishing: temporairely
	// extend the registry and confirm the new title appears.
	saved := actionRegistry
	actionRegistry = append(append([]actionMeta{}, actionRegistry...), actionMeta{
		action: "zz_synth", group: "Synth", label: "synth row", desc: "synth row",
		bind: func(k keyMap) key.Binding { return k.Refresh },
		set:  func(m *keyMap, keys []string) {},
	})
	defer func() { actionRegistry = saved }()
	body = m.helpGroupedBody(120, 40)
	if !strings.Contains(body, "Synth") {
		t.Fatal("a fourth registry group must render in the help modal")
	}
}
func TestFocusTrapModalsSwallowGlobalKeys(t *testing.T) {
	q := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	help := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	settings := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}}

	// Help open: q must not quit, o must not open settings behind it.
	m, _, _, _ := newSettingsTestModel(t)
	m.ui.helpOpen = true
	next, cmd := sendTop(m, q)
	if isQuitCmd(cmd) || !next.ui.helpOpen {
		t.Fatal("q must be inert while help is open")
	}
	next, _ = sendTop(m, settings)
	if !next.ui.helpOpen || next.ui.settings.open {
		t.Fatal("o must not open settings while help is open")
	}
	// ? dismisses help (the modal's own dismiss), Esc closes it.
	next, _ = sendTop(m, help)
	if next.ui.helpOpen {
		t.Fatal("? should dismiss an open help modal")
	}

	// Settings open: q must not quit, ? must not stack help on top.
	m2, _, _, _ := newSettingsTestModel(t)
	m2 = openViaKey(m2)
	next, cmd = sendTop(m2, q)
	if isQuitCmd(cmd) || !next.ui.settings.open {
		t.Fatal("q must be inert while settings are open")
	}
	next, _ = sendTop(m2, help)
	if next.ui.helpOpen {
		t.Fatal("? must not stack help on top of settings")
	}

	// Popup open: q must not quit, o must not open settings.
	pm := guardModel(t, frameVariant{name: "popup-trap", width: 100, height: 30, tab: tabPlayer, modal: "popup"})
	next, cmd = sendTop(pm, q)
	if isQuitCmd(cmd) || !next.ui.trackPopupOpen {
		t.Fatal("q must be inert while the track popup is open")
	}
	next, _ = sendTop(pm, settings)
	if next.ui.settings.open {
		t.Fatal("o must not open settings while the popup is open")
	}
}

func TestFocusTrapKeyCapture(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)
	m = openViaKey(m)
	m.ui.settings.mode = settingsModeCapture
	m.ui.settings.captureKey = "play_pause"

	// ctrl+c is quit's guaranteed key: it punches through capture.
	_, cmd := sendTop(m, ctrlC())
	if !isQuitCmd(cmd) {
		t.Fatal("ctrl+c must quit even during key capture")
	}
	// q is an ordinary key inside capture: it arms the pending rebind
	// and must never quit the app.
	next, cmd := sendTop(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if isQuitCmd(cmd) {
		t.Fatal("q must not quit during key capture")
	}
	if next.ui.settings.mode != settingsModeCapture || next.ui.settings.pendingKey != "q" {
		t.Fatalf("q should arm the pending rebind, got mode %v pending %q",
			next.ui.settings.mode, next.ui.settings.pendingKey)
	}
	// Esc cancels capture back to the keys list.
	next, _ = sendTop(next, tea.KeyMsg{Type: tea.KeyEscape})
	if next.ui.settings.mode != settingsModeKeys || next.ui.settings.captureKey != "" {
		t.Fatal("esc must cancel capture")
	}
}

func openSettingsForTest(m model) model {
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	return next.(model)
}

func TestModalFrameGoldenParts(t *testing.T) {
	title := styleModalTitle.Render("Settings")
	body := "\n  body line\n"
	out := modalFrame(100, 30, title, styleModalHint.Render("esc: close"), body, 52, 12)

	if !strings.Contains(out, "Settings") {
		t.Fatal("title missing")
	}
	if !strings.Contains(out, "esc: close") {
		t.Fatal("hint missing")
	}
	if !strings.Contains(out, "body line") {
		t.Fatal("body missing")
	}
	if !strings.Contains(out, "─") {
		t.Fatal("separator missing")
	}
	if !strings.Contains(out, "░") {
		t.Fatal("dim backdrop missing")
	}
	if !strings.Contains(out, "╭") && !strings.Contains(out, "┌") {
		t.Fatal("modal border missing")
	}
}

// TestModalFrameTransparentBackdrop: the dim ░ pattern survives without
// a page fill — painting page behind the box would reclaim the frame the
// mode promises to the terminal.
func TestModalFrameTransparentBackdrop(t *testing.T) {
	st := themePresetState("default")
	st.backgrounds.Style = "transparent"
	applyTheme(st)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(termenv.Ascii)
		applyTheme(themePresetState("default"))
	})
	title := styleModalTitle.Render("Settings")
	body := "\n  body line\n"
	out := modalFrame(100, 30, title, styleModalHint.Render("esc: close"), body, 52, 12)
	if !strings.Contains(out, "░") {
		t.Fatal("transparent backdrop lost the dim pattern")
	}
	if !strings.Contains(out, "╭") && !strings.Contains(out, "┌") {
		t.Fatal("transparent modal lost its border")
	}
	if pageSeq := bgSequence(colorPage); strings.Contains(out, pageSeq) {
		t.Fatal("transparent backdrop paints the page background")
	}
}

func TestModalRowSelectedAndFallback(t *testing.T) {
	sel := modalRow("Theme", "default", true, 60)
	plain := modalRow("Theme", "default", false, 60)
	if lipgloss.Width(sel) > 58 || lipgloss.Width(plain) > 58 {
		t.Fatalf("rows must fit the modal inner width: %d/%d", lipgloss.Width(sel), lipgloss.Width(plain))
	}
	if strings.Contains(sel, "> ") {
		t.Fatal("wide selected row must use the highlight, not the marker")
	}
	// Same gutter for every row: selection must never shift content (A1).
	if sel[1:8] != plain[1:8] {
		t.Fatalf("selected and unselected rows must share the label column: %q vs %q", sel[:10], plain[:10])
	}
	if lipgloss.Width(plain) != lipgloss.Width(sel) {
		t.Fatal("unselected row must be padded to the same width as the highlighted row")
	}

	fallback := modalRow("Theme", "default", true, 30)
	if !strings.HasPrefix(fallback, ">") {
		t.Fatal("narrow selected row must fall back to the > marker")
	}
	if lipgloss.Width(fallback) > 28 {
		t.Fatalf("narrow fallback row width %d exceeds the 28-cell budget", lipgloss.Width(fallback))
	}
}

func TestMiniGaugeBounds(t *testing.T) {
	full := gradientBar(1, 6)
	if !strings.Contains(full, "█") || strings.Contains(full, "░") {
		t.Fatalf("full gauge should have no empty blocks: %q", full)
	}
	empty := gradientBar(0, 6)
	if !strings.Contains(empty, "░") || strings.Contains(empty, "█") {
		t.Fatalf("empty gauge should have no filled blocks: %q", empty)
	}
	half := gradientBar(0.5, 6)
	if strings.Count(half, "█") != 3 {
		t.Fatalf("half gauge fill count = %d, want 3", strings.Count(half, "█"))
	}
}

func TestSwatchBarRendersSpacedSwatches(t *testing.T) {
	t.Cleanup(func() { lipgloss.DefaultRenderer().SetColorProfile(termenv.Ascii) })
	lipgloss.DefaultRenderer().SetColorProfile(termenv.TrueColor)

	swatches := themeSwatches(themePresetState("default").colors)
	if len(swatches) != 7 {
		t.Fatalf("swatch colors = %d, want 7", len(swatches))
	}
	bar := swatchBar(swatches)
	// 7 swatches x 2 cells + 6 single-space gaps: squares stay close within
	// a row; the line spacing between theme entries does the separating.
	if lipgloss.Width(bar) != 7*2+6 {
		t.Fatalf("swatch bar width = %d, want %d", lipgloss.Width(bar), 7*2+6)
	}
	// Every role renders as a foreground block on every row: the bar has
	// no selection variant left to reflow under the cursor.
	if strings.Count(bar, "██") != 7 {
		t.Fatalf("swatch bar should render 7 foreground blocks, got %q", bar)
	}

	// Ascii profile: color-only output degrades to nothing rather than
	// blank cells; the picker rows fall back to name + accent hex.
	lipgloss.DefaultRenderer().SetColorProfile(termenv.Ascii)
	if got := swatchBar(swatches); got != "" {
		t.Fatalf("swatch bar should be empty under Ascii profile, got %q", got)
	}
}

func TestKeyConflictDetection(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)

	// two actions rebound to the same key: both flagged
	next := openSettingsForTest(m)
	next = send(next, teaDown()) // theme options row
	next = send(next, teaDown()) // keybinds row
	next = sendEnter(next)       // keys list
	next = sendEnter(next)       // capture tab
	next = sendKey(next, "z")
	next = sendEnter(next) // confirm first rebind
	// move to the next action and rebind it to z as well
	next = send(next, teaDown()) // play_pause
	next = sendEnter(next)       // capture
	next = sendKey(next, "z")
	next = sendEnter(next) // confirm second rebind

	if len(next.ui.settings.conflicts) < 2 {
		t.Fatalf("expected at least 2 conflicting actions, got %v", next.ui.settings.conflicts)
	}

	// default bindings have no conflicts
	clean := keyConflictActions(newKeys())
	if len(clean) != 0 {
		t.Fatalf("default bindings must be conflict-free, got %v", clean)
	}
}

func TestHelpGroupedBodyHasGroups(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)
	wide := m
	wide.ui.width = 100
	body := wide.helpGroupedBody(70, 30)
	for _, title := range []string{"Playback", "Navigation", "Queue"} {
		if !strings.Contains(body, title) {
			t.Fatalf("help body missing group %q", title)
		}
	}
	if !strings.Contains(body, "play/pause") {
		t.Fatal("help body missing playback entries")
	}
	if !strings.Contains(body, "cursor up") {
		t.Fatal("help body missing queue entries")
	}

	// rebind next -> j: help must show j, not n
	next := openSettingsForTest(wide)
	next = send(next, teaDown()) // theme options row
	next = send(next, teaDown()) // keybinds row
	next = sendEnter(next)       // keys list
	next = send(next, teaDown()) // play_pause
	next = send(next, teaDown()) // next
	next = sendEnter(next)       // capture next
	next = sendKey(next, "j")
	next = sendEnter(next) // confirm
	body = next.helpGroupedBody(70, 30)
	if !strings.Contains(body, "j") {
		t.Fatal("help must reflect the rebound key j")
	}
}

func TestHelpViewportScrollKeys(t *testing.T) {
	m, _, _, _ := newSettingsTestModel(t)
	m.ui.width = 40
	m.ui.height = 14 // force overflow

	next := openViaKey(m)
	// open help on top of settings is not possible; close settings first
	next = sendEsc(next)
	m2, _ := next.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	next = m2.(model)
	if !next.ui.helpOpen {
		t.Fatal("help should be open")
	}
	if next.ui.helpViewport == nil {
		t.Fatal("overflowing help must have a viewport")
	}
	before := next.ui.helpViewport.YOffset
	// Go through handleKey, not scrollHelp directly: the seam where the
	// scrolled copy was discarded is exactly what this must cover.
	m2, _ = next.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	moved := m2.(model)
	if moved.ui.helpViewport.YOffset <= before {
		t.Fatalf("scroll down should advance offset: %d -> %d", before, moved.ui.helpViewport.YOffset)
	}
}
