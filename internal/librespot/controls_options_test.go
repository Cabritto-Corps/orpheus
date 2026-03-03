package librespot

import (
	"testing"

	"orpheus/internal/playbackdomain"
)

func TestResolveTraversalOptionsKeepsShuffleWhenTogglingRepeat(t *testing.T) {
	curr := playbackdomain.TraversalOptions{RepeatContext: false, RepeatTrack: false, Shuffle: true}
	repeatCtx := true
	next := playbackdomain.ResolveOptions(curr, &repeatCtx, nil, nil)
	if !next.Shuffle || !next.RepeatContext || next.RepeatTrack {
		t.Fatalf("unexpected next options: %+v", next)
	}
}

func TestResolveTraversalOptionsKeepsRepeatContextWhenTogglingShuffle(t *testing.T) {
	curr := playbackdomain.TraversalOptions{RepeatContext: true, RepeatTrack: false, Shuffle: false}
	shuffle := true
	next := playbackdomain.ResolveOptions(curr, nil, nil, &shuffle)
	if !next.RepeatContext || !next.Shuffle || next.RepeatTrack {
		t.Fatalf("unexpected next options: %+v", next)
	}
}

func TestResolveTraversalOptionsRepeatTrackWins(t *testing.T) {
	curr := playbackdomain.TraversalOptions{RepeatContext: true, RepeatTrack: false, Shuffle: true}
	repeatTrack := true
	next := playbackdomain.ResolveOptions(curr, nil, &repeatTrack, nil)
	if next.RepeatContext || !next.RepeatTrack {
		t.Fatalf("expected repeat-track to disable repeat-context, got %+v", next)
	}
}

func TestNextRepeatTraversalOptionsCycle(t *testing.T) {
	if got := playbackdomain.NextRepeatTraversalOptions(playbackdomain.TraversalOptions{}); !got.RepeatContext || got.RepeatTrack {
		t.Fatalf("off->context expected, got %+v", got)
	}
	if got := playbackdomain.NextRepeatTraversalOptions(playbackdomain.TraversalOptions{RepeatContext: true}); got.RepeatContext || !got.RepeatTrack {
		t.Fatalf("context->track expected, got %+v", got)
	}
	if got := playbackdomain.NextRepeatTraversalOptions(playbackdomain.TraversalOptions{RepeatTrack: true}); got.RepeatContext || got.RepeatTrack {
		t.Fatalf("track->off expected, got %+v", got)
	}
}
