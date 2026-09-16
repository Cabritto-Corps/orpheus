package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"

	"orpheus/internal/spotify"
)

func TestActionRegistryDrivesAllSurfaces(t *testing.T) {
	if len(validKeyActions) != len(actionRegistry) {
		t.Fatalf("validKeyActions %d != registry %d", len(validKeyActions), len(actionRegistry))
	}
	if len(settingsKeyActions) != len(actionRegistry) {
		t.Fatalf("settingsKeyActions %d != registry %d", len(settingsKeyActions), len(actionRegistry))
	}
	if len(helpGroupsLayout) != len(actionRegistry) {
		t.Fatalf("helpGroupsLayout %d != registry %d", len(helpGroupsLayout), len(actionRegistry))
	}
	seen := make(map[string]bool, len(actionRegistry))
	keys := newKeys()
	for i, meta := range actionRegistry {
		if seen[meta.action] {
			t.Fatalf("duplicate action %q", meta.action)
		}
		seen[meta.action] = true
		if _, ok := validKeyActions[meta.action]; !ok {
			t.Fatalf("%q missing from validKeyActions", meta.action)
		}
		if settingsKeyActions[i].action != meta.action {
			t.Fatalf("settingsKeyActions[%d] = %q, want %q", i, settingsKeyActions[i].action, meta.action)
		}
		if settingsKeyActions[i].label != meta.label {
			t.Fatalf("settingsKeyActions[%d].label = %q, want %q", i, settingsKeyActions[i].label, meta.label)
		}
		if helpGroupsLayout[i].action != meta.action || helpGroupsLayout[i].title != meta.group {
			t.Fatalf("helpGroupsLayout[%d] = %q/%q, want %q/%q", i, helpGroupsLayout[i].title, helpGroupsLayout[i].action, meta.group, meta.action)
		}
		if _, ok := defaultKeysForAction(keys, meta.action); !ok {
			t.Fatalf("defaultKeysForAction(%q) not resolved", meta.action)
		}
	}
}

func TestHelpGroupedBodyFitsModal(t *testing.T) {
	m := guardModel(t, frameVariant{name: "help", width: 120, height: 40, tab: tabPlayer})
	// 52 is the 60-col terminal's content width where the old threshold
	// math rendered a three-column join 8-18 cells wider than the modal.
	for _, contentW := range []int{24, 34, 52, 62, 72, 82, 112} {
		body := m.helpGroupedBody(contentW, 30)
		if w := lipgloss.Width(body); w > contentW {
			t.Fatalf("contentW %d: help body %d wide", contentW, w)
		}
	}
}

func TestThemeChangePreservesBrowseListState(t *testing.T) {
	m := guardModel(t, frameVariant{name: "plain", width: 120, height: 40, tab: tabPlaylists})
	items := make([]list.Item, 0, 12)
	for i := range 12 {
		items = append(items, playlistItem{summary: spotify.PlaylistSummary{
			ID:   fmt.Sprintf("pl%d", i),
			Name: fmt.Sprintf("Playlist %d", i),
			Kind: spotify.ContextKindPlaylist,
			URI:  fmt.Sprintf("spotify:playlist:pl%d", i),
		}})
	}
	m.browse.playlistList.SetItems(items)
	m.browse.playlistList.Select(7)
	pageBefore := m.browse.playlistList.Paginator.Page
	w, h := m.browse.playlistList.Width(), m.browse.playlistList.Height()

	m.rethemeBrowseLists()

	if got := m.browse.playlistList.GlobalIndex(); got != 7 {
		t.Fatalf("global index after retheme = %d, want 7", got)
	}
	if got := m.browse.playlistList.Paginator.Page; got != pageBefore {
		t.Fatalf("paginator page after retheme = %d, want %d", got, pageBefore)
	}
	if gotW, gotH := m.browse.playlistList.Width(), m.browse.playlistList.Height(); gotW != w || gotH != h {
		t.Fatalf("list size after retheme = %dx%d, want %dx%d", gotW, gotH, w, h)
	}
}

func TestHintLineRespectsWidth(t *testing.T) {
	k := newKeys()
	// 14 is the floor modalGeometry produces (16-wide box minus inset):
	// below it bubbles' ShortHelpView stops truncating because the
	// ellipsis itself no longer fits, so narrower widths are out of contract.
	for _, width := range []int{14, 20, 40, 80} {
		out := hintLine([]key.Binding{k.Select, k.Filter, k.CloseModal, k.ToggleHelp, k.Quit}, width)
		if w := lipgloss.Width(out); w > width {
			t.Fatalf("width %d: hint %d wide: %q", width, w, out)
		}
	}
}
