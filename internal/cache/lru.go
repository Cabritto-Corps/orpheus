package cache

import "container/list"

type entry[K comparable, V any] struct {
	key   K
	value V
}

type Stats struct {
	Hits      uint64
	Misses    uint64
	Sets      uint64
	Updates   uint64
	Evictions uint64
	Deletes   uint64
	Clears    uint64
}

type LRU[K comparable, V any] struct {
	capacity int
	items    map[K]*list.Element
	order    *list.List
	stats    Stats
}

func NewLRU[K comparable, V any](capacity int) *LRU[K, V] {
	if capacity <= 0 {
		capacity = 1
	}
	return &LRU[K, V]{
		capacity: capacity,
		items:    make(map[K]*list.Element, capacity),
		order:    list.New(),
	}
}

func (c *LRU[K, V]) Get(key K) (V, bool) {
	var zero V
	elem, ok := c.items[key]
	if !ok {
		c.stats.Misses++
		return zero, false
	}
	c.stats.Hits++
	c.order.MoveToBack(elem)
	return elem.Value.(entry[K, V]).value, true
}

func (c *LRU[K, V]) Peek(key K) (V, bool) {
	var zero V
	elem, ok := c.items[key]
	if !ok {
		return zero, false
	}
	return elem.Value.(entry[K, V]).value, true
}

func (c *LRU[K, V]) Set(key K, value V) (evictedKey K, evictedValue V, evicted bool) {
	c.stats.Sets++
	if elem, ok := c.items[key]; ok {
		c.stats.Updates++
		elem.Value = entry[K, V]{key: key, value: value}
		c.order.MoveToBack(elem)
		return evictedKey, evictedValue, false
	}
	elem := c.order.PushBack(entry[K, V]{key: key, value: value})
	c.items[key] = elem
	if len(c.items) <= c.capacity {
		return evictedKey, evictedValue, false
	}
	front := c.order.Front()
	if front == nil {
		return evictedKey, evictedValue, false
	}
	c.order.Remove(front)
	old := front.Value.(entry[K, V])
	delete(c.items, old.key)
	c.stats.Evictions++
	return old.key, old.value, true
}

func (c *LRU[K, V]) Delete(key K) {
	elem, ok := c.items[key]
	if !ok {
		return
	}
	c.order.Remove(elem)
	delete(c.items, key)
	c.stats.Deletes++
}

func (c *LRU[K, V]) Clear() {
	c.items = make(map[K]*list.Element, c.capacity)
	c.order = list.New()
	c.stats.Clears++
}

func (c *LRU[K, V]) Keys() []K {
	out := make([]K, 0, len(c.items))
	for key := range c.items {
		out = append(out, key)
	}
	return out
}

func (c *LRU[K, V]) Len() int {
	return len(c.items)
}

func (c *LRU[K, V]) Capacity() int {
	return c.capacity
}

func (c *LRU[K, V]) Stats() Stats {
	return c.stats
}
