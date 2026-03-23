package librespot

import "testing"

func TestSetCachedQueueMetaAndGetCachedQueueMeta(t *testing.T) {
	p := &AppPlayer{}

	p.setCachedQueueMeta("track-1", PlaybackStateQueueEntry{
		ID:         "track-1",
		Name:       "Track 1",
		Artist:     "Artist 1",
		DurationMS: 1234,
	})

	got := p.getCachedQueueMeta("track-1")
	if got == nil || got.Name != "Track 1" || got.Artist != "Artist 1" {
		t.Fatalf("expected cached entry, got %+v", got)
	}

	missing := p.getCachedQueueMeta("nonexistent")
	if missing != nil {
		t.Fatalf("expected nil for missing key, got %+v", missing)
	}
}

func TestResetQueueMetaForContextCreatesCache(t *testing.T) {
	p := &AppPlayer{}
	p.resetQueueMetaForContext("context-1")
	if p.queueMetaCache == nil {
		t.Fatal("expected cache to be created")
	}
}

func TestResetQueueMetaForContextSetsNamePreloadContext(t *testing.T) {
	p := &AppPlayer{}
	p.namePreloadContext = "old"
	p.namePreloadDone = true

	p.resetQueueMetaForContext("new")
	if p.namePreloadContext != "new" {
		t.Fatalf("expected context 'new', got %s", p.namePreloadContext)
	}
	if p.namePreloadDone {
		t.Fatal("expected namePreloadDone to be false after reset")
	}
	if p.queueResolveInFlight {
		t.Fatal("expected queueResolveInFlight to be false after reset")
	}
}

func TestResetQueueMetaForContextDoesNotClearCacheOnSameContext(t *testing.T) {
	p := &AppPlayer{}
	p.resetQueueMetaForContext("ctx")
	p.setCachedQueueMeta("t1", PlaybackStateQueueEntry{Name: "T1"})

	p.resetQueueMetaForContext("ctx")

	got := p.getCachedQueueMeta("t1")
	if got == nil || got.Name != "T1" {
		t.Fatal("expected cache to be preserved on same context reset")
	}
}

func TestClaimContextNamePreloadReturnsToken(t *testing.T) {
	p := &AppPlayer{}
	token, ok := p.claimContextNamePreload("ctx-1")
	if !ok {
		t.Fatal("expected first claim to succeed")
	}
	if token == 0 {
		t.Fatal("expected non-zero token")
	}
}

func TestClaimContextNamePreloadBlocksSecondClaim(t *testing.T) {
	p := &AppPlayer{}
	p.claimContextNamePreload("ctx-1")

	_, ok := p.claimContextNamePreload("ctx-1")
	if ok {
		t.Fatal("expected second claim to fail while in-flight")
	}
}

func TestClaimContextNamePreloadResetsOnContextChange(t *testing.T) {
	p := &AppPlayer{}
	p.claimContextNamePreload("ctx-1")

	token, ok := p.claimContextNamePreload("ctx-2")
	if !ok {
		t.Fatal("expected claim to succeed on new context")
	}
	if token == 0 {
		t.Fatal("expected non-zero token")
	}
}

func TestCheckNamePreloadStatusResetsOnContextChange(t *testing.T) {
	p := &AppPlayer{}
	p.namePreloadContext = "ctx-1"
	p.namePreloadDone = true

	done := p.checkNamePreloadStatus("ctx-2")
	if done {
		t.Fatal("expected false when context changes")
	}
	if p.namePreloadDone {
		t.Fatal("expected namePreloadDone to be reset")
	}
}

func TestCheckNamePreloadStatusReturnsDoneStatus(t *testing.T) {
	p := &AppPlayer{}
	p.namePreloadContext = "ctx-1"
	p.namePreloadDone = true

	done := p.checkNamePreloadStatus("ctx-1")
	if !done {
		t.Fatal("expected true for same context when done")
	}
}

func TestFinishContextNamePreload(t *testing.T) {
	p := &AppPlayer{}
	p.namePreloadContext = "ctx-1"
	p.namePreloadToken = 42
	p.queueResolveInFlight = true

	p.finishContextNamePreload("ctx-1", 42)
	if !p.namePreloadDone {
		t.Fatal("expected namePreloadDone to be true")
	}
	if p.queueResolveInFlight {
		t.Fatal("expected queueResolveInFlight to be false")
	}
}

func TestFinishContextNamePreloadIgnoresStaleToken(t *testing.T) {
	p := &AppPlayer{}
	p.namePreloadContext = "ctx-1"
	p.namePreloadToken = 5

	p.finishContextNamePreload("ctx-1", 3) // stale token
	if p.namePreloadDone {
		t.Fatal("expected namePreloadDone to remain false with stale token")
	}
}
