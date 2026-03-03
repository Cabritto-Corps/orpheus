package librespot

import "testing"

func TestShouldSkipPlayedShuffledTrack(t *testing.T) {
	if !shouldSkipPlayedShuffledTrack(false, true) {
		t.Fatal("expected played tracks to be skipped when repeat-context is off")
	}
	if !shouldSkipPlayedShuffledTrack(true, true) {
		t.Fatal("expected played tracks to remain skipped when repeat-context is on")
	}
	if shouldSkipPlayedShuffledTrack(false, false) {
		t.Fatal("expected unplayed tracks to remain visible")
	}
}
