package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"orpheus/internal/spotify"
)

func thumbnailModel(t *testing.T, active tab) model {
	t.Helper()
	m := NewLoaderModel()
	m.browse.librarySettled = true
	m.ui.activeTab = active
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(model)
	m.ui.imgs.setProtocol(imageProtocolKitty)
	m.ui.imgs.encoded["art-one"] = "b25l"
	m.ui.imgs.encoded["art-two"] = "dHdv"
	items := []list.Item{
		searchResultItem{result: spotify.SearchResultItem{ID: "one", Name: "One", Kind: "track", ImageURL: "art-one"}},
		searchResultItem{result: spotify.SearchResultItem{ID: "two", Name: "Two", Kind: "track", ImageURL: "art-two"}},
	}
	m.browse.search.list.SetItems(items)
	m.browse.songsList.SetItems([]list.Item{
		trackItem{item: spotify.QueueItem{ID: "one", Name: "One", ImageURL: "art-one"}},
		trackItem{item: spotify.QueueItem{ID: "two", Name: "Two", ImageURL: "art-two"}},
	})
	return m
}

func TestRenderedThumbnailsUseGraphicsNotHalfBlocks(t *testing.T) {
	m := thumbnailModel(t, tabSearch)
	d := newSearchResultDelegate(m.styles, m.ui.imgs)
	thumb := d.thumbnail("art-one")
	if strings.Contains(thumb, "▀") || lipgloss.Width(thumb) != searchThumbWidth || lipgloss.Height(thumb) != searchThumbHeight {
		t.Fatalf("graphics thumbnail must reserve blank cells: %q", thumb)
	}
	out, content := m.kittyThumbnailOverlayBytes()
	if !content || strings.Count(out, "a=T") != 2 {
		t.Fatalf("expected two full-resolution thumbnail transmissions: %q", out)
	}
	if out, _ := m.kittyThumbnailOverlayBytes(); out != "" {
		t.Fatal("unchanged thumbnails must not retransmit every tick")
	}
}

func TestThumbnailGeometryAndCleanup(t *testing.T) {
	for _, active := range []tab{tabSearch, tabSongs} {
		t.Run(string(active), func(t *testing.T) {
			m := thumbnailModel(t, active)
			placements := m.thumbnailPlacements()
			first := placements[0].art
			if first.row != bodyStartRow1Based+4 || first.col != m.bodyLayout().leftW+1 {
				t.Fatalf("wrong first thumbnail origin: %#v", first)
			}
			if placements[1].art.row != first.row+searchThumbHeight+1 {
				t.Fatal("thumbnail stride disagrees with list row height/spacing")
			}
			// Compare with the rendered frame, not only the geometry formula.
			firstTitleRow := 0
			for i, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
				if strings.Contains(line, "One") {
					firstTitleRow = i + 1
					break
				}
			}
			if firstTitleRow != first.row {
				t.Fatalf("graphics and row text disagree: art row=%d title row=%d", first.row, firstTitleRow)
			}
			m.kittyThumbnailOverlayBytes()
			m.ui.helpOpen = true
			out, content := m.kittyThumbnailOverlayBytes()
			if content || strings.Count(out, "a=d,d=I") != 2 {
				t.Fatalf("modal did not purge both thumbnails: %q", out)
			}
			m.ui.helpOpen = false
			if out, content := m.kittyThumbnailOverlayBytes(); !content || strings.Count(out, "a=T") != 2 {
				t.Fatalf("modal close did not restore thumbnails: %q", out)
			}
			m.ui.activeTab = tabPlaylists
			if out, content := m.kittyThumbnailOverlayBytes(); content || strings.Count(out, "a=d,d=I") != 2 {
				t.Fatalf("tab switch left thumbnails visible: %q", out)
			}
		})
	}
}

func TestThumbnailIDsDoNotCollideWithPreview(t *testing.T) {
	m := thumbnailModel(t, tabSearch)
	out, content := m.kittyOverlayBytes()
	if !content || strings.Count(out, "a=T") != 3 {
		t.Fatalf("expected preview plus two thumbnails: %q", out)
	}
	previewID := m.ui.imgs.overlay.shownID
	for _, placement := range m.ui.imgs.thumbnails {
		if placement.id == previewID {
			t.Fatal("list and preview share an image ID")
		}
		if !strings.Contains(out, fmt.Sprintf("i=%d", placement.id)) {
			t.Fatal("thumbnail transmission missing its image ID")
		}
	}
	m.ui.imgs.setProtocol(imageProtocolNone)
	if out, content := m.kittyOverlayBytes(); content || strings.Count(out, "a=d,d=I") != 3 {
		t.Fatalf("pixelated style switch did not purge graphics: %q", out)
	}
}
