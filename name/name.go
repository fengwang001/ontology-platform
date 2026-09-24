package name

import (
	"bytes"
	"sync"
)

// Namespace is a concurrent-safe set of currently occupied names.
type Namespace struct {
	mu    sync.RWMutex
	items map[string]struct{}
}

func New(items ...string) *Namespace {
	n := &Namespace{items: make(map[string]struct{}, len(items))}
	for _, item := range items {
		n.items[item] = struct{}{}
	}
	return n
}

func Valid(s string) bool {
	return !bytes.ContainsRune([]byte(s), 0)
}

func (n *Namespace) Snapshot() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]string, 0, len(n.items))
	for item := range n.items {
		out = append(out, item)
	}
	return out
}

func (n *Namespace) Has(item string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	_, ok := n.items[item]
	return ok
}

func (n *Namespace) Add(item string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.items[item]; ok {
		return false
	}
	n.items[item] = struct{}{}
	return true
}

func (n *Namespace) Remove(item string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.items[item]; !ok {
		return false
	}
	delete(n.items, item)
	return true
}

func (n *Namespace) Move(from, to string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if from == to {
		_, ok := n.items[from]
		return ok
	}
	if _, ok := n.items[from]; !ok {
		return false
	}
	if _, ok := n.items[to]; ok {
		return false
	}
	delete(n.items, from)
	n.items[to] = struct{}{}
	return true
}

func (n *Namespace) WithLock(fn func(map[string]struct{})) {
	n.mu.Lock()
	defer n.mu.Unlock()
	fn(n.items)
}

func Equal(a, b *Namespace) bool {
	sa, sb := a.Snapshot(), b.Snapshot()
	if len(sa) != len(sb) {
		return false
	}
	want := make(map[string]struct{}, len(sb))
	for _, item := range sb {
		want[item] = struct{}{}
	}
	for _, item := range sa {
		if _, ok := want[item]; !ok {
			return false
		}
	}
	return true
}
