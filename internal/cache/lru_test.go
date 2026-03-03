package cache

import "testing"

func TestLRUEvictionOrder(t *testing.T) {
	c := NewLRU[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected to read key a")
	}
	if k, _, evicted := c.Set("c", 3); !evicted || k != "b" {
		t.Fatalf("expected key b eviction, got key=%q evicted=%v", k, evicted)
	}
	if _, ok := c.Peek("a"); !ok {
		t.Fatal("expected key a to remain")
	}
	if _, ok := c.Peek("b"); ok {
		t.Fatal("expected key b to be evicted")
	}
}

func TestLRUOverwriteDoesNotEvict(t *testing.T) {
	c := NewLRU[string, int](2)
	c.Set("a", 1)
	c.Set("b", 2)
	if _, _, evicted := c.Set("a", 10); evicted {
		t.Fatal("overwrite should not evict")
	}
	if got, ok := c.Peek("a"); !ok || got != 10 {
		t.Fatalf("expected updated value 10, got %d ok=%v", got, ok)
	}
}

func TestLRUClearAndKeys(t *testing.T) {
	c := NewLRU[string, int](4)
	c.Set("a", 1)
	c.Set("b", 2)
	if len(c.Keys()) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(c.Keys()))
	}
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("expected empty cache, got len=%d", c.Len())
	}
	if len(c.Keys()) != 0 {
		t.Fatalf("expected no keys after clear, got %d", len(c.Keys()))
	}
}

