package cache

import "time"

type ttlEntry[V any] struct {
	value     V
	expiresAt time.Time
}

type TTL[K comparable, V any] struct {
	lru *LRU[K, ttlEntry[V]]
	ttl time.Duration
	now func() time.Time
}

func NewTTL[K comparable, V any](capacity int, ttl time.Duration) *TTL[K, V] {
	return &TTL[K, V]{
		lru: NewLRU[K, ttlEntry[V]](capacity),
		ttl: ttl,
		now: time.Now,
	}
}

func (c *TTL[K, V]) Get(key K) (V, bool) {
	var zero V
	item, ok := c.lru.Get(key)
	if !ok {
		return zero, false
	}
	if c.expired(item.expiresAt) {
		c.lru.Delete(key)
		return zero, false
	}
	return item.value, true
}

func (c *TTL[K, V]) Peek(key K) (V, bool) {
	var zero V
	item, ok := c.lru.Peek(key)
	if !ok {
		return zero, false
	}
	if c.expired(item.expiresAt) {
		c.lru.Delete(key)
		return zero, false
	}
	return item.value, true
}

func (c *TTL[K, V]) Set(key K, value V) (evictedKey K, evictedValue V, evicted bool) {
	expiresAt := time.Time{}
	if c.ttl > 0 {
		expiresAt = c.now().Add(c.ttl)
	}
	oldKey, oldItem, oldEvicted := c.lru.Set(key, ttlEntry[V]{value: value, expiresAt: expiresAt})
	if !oldEvicted {
		return evictedKey, evictedValue, false
	}
	return oldKey, oldItem.value, true
}

func (c *TTL[K, V]) Delete(key K) {
	c.lru.Delete(key)
}

func (c *TTL[K, V]) Clear() {
	c.lru.Clear()
}

func (c *TTL[K, V]) Len() int {
	return c.lru.Len()
}

func (c *TTL[K, V]) Capacity() int {
	return c.lru.Capacity()
}

func (c *TTL[K, V]) Stats() Stats {
	return c.lru.Stats()
}

func (c *TTL[K, V]) Keys() []K {
	keys := c.lru.Keys()
	if c.ttl <= 0 {
		return keys
	}
	out := make([]K, 0, len(keys))
	for _, k := range keys {
		item, ok := c.lru.Peek(k)
		if !ok {
			continue
		}
		if c.expired(item.expiresAt) {
			c.lru.Delete(k)
			continue
		}
		out = append(out, k)
	}
	return out
}

func (c *TTL[K, V]) expired(expiresAt time.Time) bool {
	return !expiresAt.IsZero() && !c.now().Before(expiresAt)
}
