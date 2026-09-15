package tui

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/list"

	"orpheus/internal/spotify"
)

func TestCachedDelegateRenderMatchesDefault(t *testing.T) {
	items := make([]list.Item, 8)
	for i := range items {
		items[i] = playlistItem{summary: spotify.PlaylistSummary{ID: fmt.Sprintf("pl-%d", i), Name: fmt.Sprintf("playlist-%d", i)}}
	}
	cd := newCachedPlaylistDelegate()
	m := list.New(items, cd, 40, 20)
	m.SetShowTitle(false)
	m.SetShowStatusBar(false)
	m.SetShowHelp(false)

	for _, sel := range []int{0, 3} {
		m.Select(sel)
		for _, idx := range []int{0, 3, 5} {
			var got bytes.Buffer
			cd.Render(&got, m, idx, items[idx])
			var want bytes.Buffer
			newPlaylistDelegate().Render(&want, m, idx, items[idx])
			if got.String() != want.String() {
				t.Fatalf("delegate output mismatch (sel=%d idx=%d)", sel, idx)
			}
		}
	}
}

func TestTabBarAndPlaceholderStableAcrossReads(t *testing.T) {
	m := benchModel(t, 4)
	a := m.tabBarView()
	b := m.tabBarView()
	if a != b {
		t.Fatal("tabBarView must be deterministic per (width, tab, theme)")
	}
	pa := m.placeholderArt(40, 12)
	pb := m.placeholderArt(40, 12)
	if pa != pb {
		t.Fatal("placeholderArt must be deterministic per (cols, rows, theme)")
	}
}
