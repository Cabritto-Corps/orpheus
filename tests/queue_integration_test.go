package tests

import (
	"testing"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/tracks"
)


func TestShuffleStartPosExported(t *testing.T) {
	var l *tracks.List
	if l != nil {
		_ = l.ShuffleStartPos()
	}
}

func TestUpcomingTracksExported(t *testing.T) {
	var l *tracks.List
	_ = l
}

func TestNormalizeSpotifyIdRoundTrip(t *testing.T) {
	uri := "spotify:track:7GhIk7Il098yCjg4BQjzvb"
	id := golibrespot.NormalizeSpotifyId(uri)
	if id != "7GhIk7Il098yCjg4BQjzvb" {
		t.Fatalf("expected base62 id, got %s", id)
	}

	plain := golibrespot.NormalizeSpotifyId("7GhIk7Il098yCjg4BQjzvb")
	if plain != "7GhIk7Il098yCjg4BQjzvb" {
		t.Fatalf("expected plain id unchanged, got %s", plain)
	}
}

func TestSpotifyIdTypeConstants(t *testing.T) {
	_ = golibrespot.SpotifyIdTypeTrack
	_ = golibrespot.SpotifyIdTypeEpisode
	_ = golibrespot.SpotifyIdTypePlaylist
}

func TestMaxStateVolumeConstant(t *testing.T) {
	if golibrespot.MaxStateVolume == 0 {
		t.Fatal("MaxStateVolume should be non-zero")
	}
	if golibrespot.MaxStateVolume != 65535 {
		t.Fatalf("expected 65535, got %d", golibrespot.MaxStateVolume)
	}
}
