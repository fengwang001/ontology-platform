// Package tier tracks per-key residency metadata and selects LRU victims.
// It depends on no other package.
package tier

import "container/list"

// Entry is one hot-layer key with its last-access timestamp.
type Entry struct {
	Key   string
	Stamp int64
}

// Older reports whether a must be evicted before b: the smaller timestamp
// wins; ties break to the lexicographically smaller key.
func Older(a, b Entry) bool {
	if a.Stamp != b.Stamp {
		return a.Stamp < b.Stamp
	}
	return a.Key < b.Key
}

// node is the payload held in the ordering list.
type node struct {
	key   string
	stamp int64
}

// Index is an access-ordered set of hot keys: front = most recently used,
// back = LRU victim. Touch and Victim are O(1); the key->element map makes
// lookup, removal and stamp refresh O(1) too.
type Index struct {
	ll *list.List
	m  map[string]*list.Element
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{ll: list.New(), m: make(map[string]*list.Element)}
}

// Add inserts e as the most recently used entry. The key must be absent.
func (x *Index) Add(e Entry) {
	x.m[e.Key] = x.ll.PushFront(&node{key: e.Key, stamp: e.Stamp})
}

// Touch refreshes an existing key's timestamp and moves it to the front.
func (x *Index) Touch(key string, stamp int64) bool {
	el, ok := x.m[key]
	if !ok {
		return false
	}
	el.Value.(*node).stamp = stamp
	x.ll.MoveToFront(el)
	return true
}

// Victim returns the LRU entry (back of the list) in O(1).
func (x *Index) Victim() (Entry, bool) {
	el := x.ll.Back()
	if el == nil {
		return Entry{}, false
	}
	n := el.Value.(*node)
	return Entry{Key: n.key, Stamp: n.stamp}, true
}

// Remove deletes key and returns the entry it held.
func (x *Index) Remove(key string) (Entry, bool) {
	el, ok := x.m[key]
	if !ok {
		return Entry{}, false
	}
	n := el.Value.(*node)
	x.ll.Remove(el)
	delete(x.m, key)
	return Entry{Key: n.key, Stamp: n.stamp}, true
}

func (x *Index) Len() int { return x.ll.Len() }

func (x *Index) Contains(key string) bool {
	_, ok := x.m[key]
	return ok
}

// Get returns the current entry for key.
func (x *Index) Get(key string) (Entry, bool) {
	el, ok := x.m[key]
	if !ok {
		return Entry{}, false
	}
	n := el.Value.(*node)
	return Entry{Key: n.key, Stamp: n.stamp}, true
}
