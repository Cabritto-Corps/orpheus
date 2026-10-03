package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"

	"orpheus/internal/spotify"
)

func popupItems(n int) []list.Item {
	items := make([]list.Item, 0, n)
	for i := range n {
		items = append(items, trackItem{item: spotify.QueueItem{
			ID: fmt.Sprintf("spotify:track:%011d", i), Name: fmt.Sprintf("Track %d", i), Artist: "Artist", DurationMS: 200000,
		}})
	}
	return items
}

func TestTrackPopupFooterVisible(t *testing.T) {
	st := buildThemeStyles(themePresetState("default"))
	popup := newTrackPopupList(st, 100, 40)
	popup.SetItems(popupItems(5))
	if view := popup.View(); !strings.Contains(view, "5 tracks") {
		t.Fatalf("single-page popup should show the item count, got %q", lastLine(view))
	}

	paged := newTrackPopupList(st, 100, 40)
	paged.SetItems(popupItems(80))
	view := paged.View()
	if !strings.Contains(view, "80 tracks") {
		t.Fatal("paged popup should show the item count")
	}
	if !strings.Contains(view, "•") {
		t.Fatal("paged popup should show pagination dots")
	}
}

func TestTrackPopupDotsOnFirstOpen(t *testing.T) {
	// First SetItems derived PerPage while TotalPages was still 0, overflowing
	// the modal budget by the pagination row — dots only appeared after resize.
	m := guardModel(t, frameVariant{name: "popup", width: 100, height: 40, tab: tabPlaylists})
	m.ui.trackPopupOpen = true
	m.ui.trackPopupList = newTrackPopupList(m.styles, m.ui.width, m.ui.height)
	m.ui.trackPopupWidth = m.ui.trackPopupList.Width() - 4
	qi := make([]spotify.QueueItem, 0, 80)
	for i := range 80 {
		qi = append(qi, spotify.QueueItem{ID: fmt.Sprintf("spotify:track:%011d", i), Name: fmt.Sprintf("Track %d", i), Artist: "Artist", DurationMS: 200000})
	}
	m.ui.trackPopupItems = qi
	m.retruncateTrackPopupTitles()

	view := m.trackPopupView()
	if !strings.Contains(view, "•") {
		t.Fatal("first-open popup view should show pagination dots")
	}
	if !strings.Contains(view, "80 tracks") {
		t.Fatal("first-open popup view should show the item count")
	}
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
