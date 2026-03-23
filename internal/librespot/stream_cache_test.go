package librespot

import (
	"testing"

	golibrespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/player"
)

func mustID(uri string) golibrespot.SpotifyId {
	id, err := golibrespot.SpotifyIdFromUri(uri)
	if err != nil {
		panic(err)
	}
	return *id
}

func TestPutAndHasTransitionCachedStream(t *testing.T) {
	p := &AppPlayer{}
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	stream := &player.Stream{}

	if !p.putTransitionCachedStream(id, stream) {
		t.Fatal("expected put to succeed")
	}
	if !p.hasTransitionCachedStream(id) {
		t.Fatal("expected has to return true")
	}
}

func TestPutDuplicateReturnsFalse(t *testing.T) {
	p := &AppPlayer{}
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	p.putTransitionCachedStream(id, &player.Stream{})

	if p.putTransitionCachedStream(id, &player.Stream{}) {
		t.Fatal("expected duplicate put to return false")
	}
}

func TestPutNilStreamReturnsFalse(t *testing.T) {
	p := &AppPlayer{}
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	if p.putTransitionCachedStream(id, nil) {
		t.Fatal("expected put nil to return false")
	}
}

func TestTakeRemovesAndReturns(t *testing.T) {
	p := &AppPlayer{}
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	stream := &player.Stream{}
	p.putTransitionCachedStream(id, stream)

	taken := p.takeTransitionCachedStream(id)
	if taken != stream {
		t.Fatal("expected same stream")
	}
	if p.hasTransitionCachedStream(id) {
		t.Fatal("expected stream to be removed after take")
	}
}

func TestTakeNonexistentReturnsNil(t *testing.T) {
	p := &AppPlayer{}
	p.transitionStreamCache = make(map[string]*player.Stream)
	p.transitionStreamOrder = nil
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	if p.takeTransitionCachedStream(id) != nil {
		t.Fatal("expected nil for nonexistent")
	}
}

func TestEvictionRespectsCacheSize(t *testing.T) {
	p := &AppPlayer{}

	ids := []string{
		"spotify:track:7GhIk7Il098yCjg4BQjzvb",
		"spotify:track:2WfaOiMkCvy7F5fcp2zZ8L",
		"spotify:track:4cOdK2wGLETKBW3PvgPWqT",
		"spotify:track:6rqhFgbbKwnb9MLmUQDhG6",
		"spotify:track:3n3Ppam7vgaVa1iaRUc9Lp",
	}

	for _, uri := range ids {
		p.putTransitionCachedStream(mustID(uri), &player.Stream{})
	}

	if len(p.transitionStreamCache) > transitionStreamCacheMax {
		t.Fatalf("cache should not exceed max %d, got %d", transitionStreamCacheMax, len(p.transitionStreamCache))
	}
	if len(p.transitionStreamOrder) > transitionStreamCacheMax {
		t.Fatalf("order should not exceed max %d, got %d", transitionStreamCacheMax, len(p.transitionStreamOrder))
	}
}

func TestClearTransitionStreamCache(t *testing.T) {
	p := &AppPlayer{}
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	p.putTransitionCachedStream(id, &player.Stream{})

	p.clearTransitionStreamCache()
	if p.hasTransitionCachedStream(id) {
		t.Fatal("expected cache to be empty after clear")
	}
	if len(p.transitionStreamCache) != 0 {
		t.Fatal("expected empty cache map")
	}
}

func TestBumpPrefetchGeneration(t *testing.T) {
	p := &AppPlayer{}
	g1 := p.bumpPrefetchGeneration()
	g2 := p.bumpPrefetchGeneration()
	if g2 <= g1 {
		t.Fatalf("expected generation to increase, got %d -> %d", g1, g2)
	}
}

func TestMarkAndHasPrefetchPending(t *testing.T) {
	p := &AppPlayer{}
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")

	if !p.markPrefetchPending(id) {
		t.Fatal("expected first mark to succeed")
	}
	if !p.hasPrefetchPending(id) {
		t.Fatal("expected has to return true")
	}
	if p.markPrefetchPending(id) {
		t.Fatal("expected duplicate mark to return false")
	}
}

func TestClearPrefetchPending(t *testing.T) {
	p := &AppPlayer{}
	id := mustID("spotify:track:7GhIk7Il098yCjg4BQjzvb")
	p.markPrefetchPending(id)

	p.clearPrefetchPending(id)
	if p.hasPrefetchPending(id) {
		t.Fatal("expected pending to be cleared")
	}
}
