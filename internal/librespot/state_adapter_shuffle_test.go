package librespot

import (
	"testing"

	connectpb "github.com/devgianlu/go-librespot/proto/spotify/connectstate"
)

func TestOrderedQueueFromCurrentWrapsContextWhenEnabled(t *testing.T) {
	all := []*connectpb.ProvidedTrack{
		{Uri: "spotify:track:a"},
		{Uri: "spotify:track:b"},
		{Uri: "spotify:track:c"},
		{Uri: "spotify:track:d"},
	}
	ordered, hasMore := orderedQueueFromCurrent(all, 2, 500, true)
	if hasMore {
		t.Fatal("did not expect hasMore for short queue")
	}
	if len(ordered) != 4 {
		t.Fatalf("expected 4 items, got %d", len(ordered))
	}
	if ordered[0].Uri != "spotify:track:c" || ordered[1].Uri != "spotify:track:d" || ordered[2].Uri != "spotify:track:a" || ordered[3].Uri != "spotify:track:b" {
		t.Fatalf("unexpected order after wrap: %#v", []string{ordered[0].Uri, ordered[1].Uri, ordered[2].Uri, ordered[3].Uri})
	}
}

func TestOrderedQueueFromCurrentDoesNotWrapWhenDisabled(t *testing.T) {
	all := []*connectpb.ProvidedTrack{
		{Uri: "spotify:track:a"},
		{Uri: "spotify:track:b"},
		{Uri: "spotify:track:c"},
		{Uri: "spotify:track:d"},
	}
	ordered, hasMore := orderedQueueFromCurrent(all, 2, 500, false)
	if hasMore {
		t.Fatal("did not expect hasMore for short queue")
	}
	if len(ordered) != 2 {
		t.Fatalf("expected only tail from current track, got %d", len(ordered))
	}
	if ordered[0].Uri != "spotify:track:c" || ordered[1].Uri != "spotify:track:d" {
		t.Fatalf("unexpected non-wrap order: %#v", []string{ordered[0].Uri, ordered[1].Uri})
	}
}

func TestOrderedQueueFromCurrentAppliesLimit(t *testing.T) {
	all := []*connectpb.ProvidedTrack{
		{Uri: "spotify:track:a"},
		{Uri: "spotify:track:b"},
		{Uri: "spotify:track:c"},
		{Uri: "spotify:track:d"},
	}
	ordered, hasMore := orderedQueueFromCurrent(all, 1, 2, true)
	if !hasMore {
		t.Fatal("expected hasMore when queue is truncated")
	}
	if len(ordered) != 2 {
		t.Fatalf("expected 2 items after truncation, got %d", len(ordered))
	}
	if ordered[0].Uri != "spotify:track:b" || ordered[1].Uri != "spotify:track:c" {
		t.Fatalf("unexpected limited order: %#v", []string{ordered[0].Uri, ordered[1].Uri})
	}
}
