package librespot

import (
	"testing"
	"time"
)

func TestSetActiveTrue(t *testing.T) {
	s := &State{}
	s.setActive(true)
	if !s.active {
		t.Fatal("expected active to be true")
	}
	if s.activeSince.IsZero() {
		t.Fatal("expected activeSince to be set")
	}
}

func TestSetActiveTrueWhenAlreadyActive(t *testing.T) {
	s := &State{active: true, activeSince: time.Now().Add(-time.Hour)}
	before := s.activeSince
	s.setActive(true) // should be no-op
	if !s.activeSince.Equal(before) {
		t.Fatal("expected activeSince to remain unchanged")
	}
}

func TestSetActiveFalse(t *testing.T) {
	s := &State{active: true, activeSince: time.Now()}
	s.setActive(false)
	if s.active {
		t.Fatal("expected active to be false")
	}
	if !s.activeSince.IsZero() {
		t.Fatal("expected activeSince to be zero")
	}
}

func TestReset(t *testing.T) {
	s := &State{active: true, activeSince: time.Now()}
	s.reset()
	if s.active {
		t.Fatal("expected active to be false after reset")
	}
	if !s.activeSince.IsZero() {
		t.Fatal("expected activeSince to be zero after reset")
	}
	if s.player == nil {
		t.Fatal("expected player state to be initialized")
	}
	if !s.player.IsSystemInitiated {
		t.Fatal("expected IsSystemInitiated to be true")
	}
}
