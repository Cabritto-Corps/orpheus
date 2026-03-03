package cache

import (
	"testing"
	"time"
)

func TestTTLExpiresEntriesOnGet(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewTTL[string, int](2, 10*time.Second)
	c.now = func() time.Time { return now }
	c.Set("a", 1)
	if got, ok := c.Get("a"); !ok || got != 1 {
		t.Fatalf("expected value before expiry, got %d ok=%v", got, ok)
	}
	now = now.Add(11 * time.Second)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired entry to be removed")
	}
}

func TestTTLKeysFiltersExpired(t *testing.T) {
	now := time.Unix(200, 0)
	c := NewTTL[string, int](4, 5*time.Second)
	c.now = func() time.Time { return now }
	c.Set("a", 1)
	now = now.Add(3 * time.Second)
	c.Set("b", 2)
	now = now.Add(3 * time.Second)
	keys := c.Keys()
	if len(keys) != 1 || keys[0] != "b" {
		t.Fatalf("expected only key b after expiry filtering, got %v", keys)
	}
}

func TestTTLZeroDurationDoesNotExpire(t *testing.T) {
	now := time.Unix(300, 0)
	c := NewTTL[string, int](2, 0)
	c.now = func() time.Time { return now }
	c.Set("a", 1)
	now = now.Add(24 * time.Hour)
	if got, ok := c.Peek("a"); !ok || got != 1 {
		t.Fatalf("expected non-expiring key, got %d ok=%v", got, ok)
	}
}

