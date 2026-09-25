// Package lru tracks per-key logical access times and the hot-tier
// MRU→LRU order, and locates the eviction candidate (the LRU key).
// It depends on no other package. It is NOT goroutine-safe; callers
// serialize access.
package lru

import (
	"container/list"
	"fmt"
)

// EvictionCostBound is the size-independent upper bound on keys
// examined to locate one LRU candidate via the ordered list.
const EvictionCostBound = 1

// LRU is the hot-tier access-order index. Front of the list is MRU,
// back is LRU. The logical clock starts at 0 and the first Touch stamps 1.
type LRU struct {
	ll    *list.List
	pos   map[string]*list.Element
	atime map[string]int64
	clock int64

	// lastEvictScans counts hot keys whose access time was
	// inspected/compared to locate the LRU key during the latest Evict.
	// With an ordered list the back node is the unique candidate, so it
	// is 1 regardless of Len; unexported, never exposed via any method.
	lastEvictScans int
}

type node struct{ key string }

// New creates an empty index.
func New() *LRU {
	return &LRU{ll: list.New(), pos: map[string]*list.Element{}, atime: map[string]int64{}}
}

// Touch stamps key with the current logical clock value (clock+1),
// inserts or moves it to MRU, advances the clock by one, and returns
// the stamped access time.
func (l *LRU) Touch(key string) int64 {
	now := l.clock + 1
	if e, ok := l.pos[key]; ok {
		l.ll.MoveToFront(e)
	} else {
		l.pos[key] = l.ll.PushFront(&node{key: key})
	}
	l.atime[key] = now
	l.clock = now
	return now
}

// Len is the number of tracked (hot) keys.
func (l *LRU) Len() int { return l.ll.Len() }

// Contains reports whether key is currently hot.
func (l *LRU) Contains(key string) bool { _, ok := l.pos[key]; return ok }

// Atime returns the access time last stamped on key.
func (l *LRU) Atime(key string) (int64, bool) {
	t, ok := l.atime[key]
	return t, ok
}

// Evict removes and returns the LRU key (earliest access time). It
// records in lastEvictScans how many hot keys were examined to locate
// it: exactly the single back node, never a full scan.
func (l *LRU) Evict() (string, bool) {
	if l.ll.Len() == 0 {
		l.lastEvictScans = 0
		return "", false
	}
	l.lastEvictScans = 1
	e := l.ll.Back()
	l.ll.Remove(e)
	k := e.Value.(*node).key
	delete(l.pos, k)
	delete(l.atime, k)
	return k, true
}

// Remove drops key from the index (used on tier bookkeeping).
func (l *LRU) Remove(key string) bool {
	e, ok := l.pos[key]
	if !ok {
		return false
	}
	l.ll.Remove(e)
	delete(l.pos, key)
	delete(l.atime, key)
	return true
}

// Keys returns hot keys in MRU→LRU order.
func (l *LRU) Keys() []string {
	out := make([]string, 0, l.ll.Len())
	for e := l.ll.Front(); e != nil; e = e.Next() {
		out = append(out, e.Value.(*node).key)
	}
	return out
}

// VerifyEvictionCost fills an index with m keys and triggers one
// eviction, reporting an error if the number of keys examined to
// locate the LRU key grew with m rather than staying within the
// size-independent bound. It never returns the raw counter value.
func VerifyEvictionCost(m int) error {
	l := New()
	for i := 0; i < m; i++ {
		l.Touch(fmt.Sprintf("k%06d", i))
	}
	l.Evict()
	if l.lastEvictScans > EvictionCostBound {
		return fmt.Errorf("lru: eviction examined %d keys at m=%d, bound %d",
			l.lastEvictScans, m, EvictionCostBound)
	}
	return nil
}
