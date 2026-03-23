package librespot

import (
	"context"
	"testing"
)

func TestHandleTUIPlaybackCommandDefault(t *testing.T) {
	p := &AppPlayer{}
	handled, err := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: 999})
	if handled {
		t.Fatal("expected unknown command to not be handled")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleTUIPlaybackCommandShuffleNilState(t *testing.T) {
	p := &AppPlayer{}
	handled, _ := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandShuffle})
	if !handled {
		t.Fatal("expected shuffle to be handled even with nil state")
	}
}

func TestHandleTUIPlaybackCommandRepeatNilState(t *testing.T) {
	p := &AppPlayer{}
	handled, _ := p.handleTUIPlaybackCommand(context.Background(), TUICommand{Kind: TUICommandCycleRepeat})
	if !handled {
		t.Fatal("expected repeat to be handled even with nil state")
	}
}

func TestHandleTUIContextCommandDefault(t *testing.T) {
	p := &AppPlayer{}
	handled, err := p.handleTUIContextCommand(context.Background(), TUICommand{Kind: 999})
	if handled {
		t.Fatal("expected unknown context command to not be handled")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
