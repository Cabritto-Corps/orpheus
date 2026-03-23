package playbackdomain

import "testing"

func boolPtr(v bool) *bool { return &v }

func TestResolveOptionsNoOverrides(t *testing.T) {
	curr := TraversalOptions{RepeatContext: true, RepeatTrack: false, Shuffle: false}
	got := ResolveOptions(curr, nil, nil, nil)
	if got != curr {
		t.Fatalf("expected no change, got %+v", got)
	}
}

func TestResolveOptionsOverrideShuffle(t *testing.T) {
	curr := TraversalOptions{Shuffle: false}
	got := ResolveOptions(curr, nil, nil, boolPtr(true))
	if !got.Shuffle {
		t.Fatal("expected shuffle to be true")
	}
}

func TestResolveOptionsRepeatTrackDisablesRepeatContext(t *testing.T) {
	curr := TraversalOptions{RepeatContext: true, RepeatTrack: false}
	got := ResolveOptions(curr, nil, boolPtr(true), nil)
	if got.RepeatTrack != true {
		t.Fatal("expected repeat track to be true")
	}
	if got.RepeatContext != false {
		t.Fatal("expected repeat context to be false when repeat track is true")
	}
}

func TestResolveOptionsPartialOverride(t *testing.T) {
	curr := TraversalOptions{RepeatContext: true, RepeatTrack: false, Shuffle: true}
	got := ResolveOptions(curr, boolPtr(false), nil, nil)
	if got.RepeatContext {
		t.Fatal("expected repeat context to be false")
	}
	if !got.Shuffle {
		t.Fatal("expected shuffle to remain true")
	}
}

func TestNextRepeatCycle(t *testing.T) {
	off := TraversalOptions{}
	ctx := NextRepeatTraversalOptions(off)
	if !ctx.RepeatContext || ctx.RepeatTrack {
		t.Fatalf("expected repeat context, got %+v", ctx)
	}

	track := NextRepeatTraversalOptions(ctx)
	if track.RepeatContext || !track.RepeatTrack {
		t.Fatalf("expected repeat track, got %+v", track)
	}

	back := NextRepeatTraversalOptions(track)
	if back.RepeatContext || back.RepeatTrack {
		t.Fatalf("expected no repeat, got %+v", back)
	}
}

func TestNextRepeatTrackDisablesContext(t *testing.T) {
	withCtx := TraversalOptions{RepeatContext: true}
	got := NextRepeatTraversalOptions(withCtx)
	if got.RepeatContext {
		t.Fatal("expected repeat context to be false after cycle to track")
	}
	if !got.RepeatTrack {
		t.Fatal("expected repeat track to be true")
	}
}
