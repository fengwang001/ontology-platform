// Package siblings implements a dot-version-vector sibling store.
//
// Each key holds a set of siblings. A sibling pairs a dot (id, n) with a
// value, where id is the node identifier and n is a per-key, per-node
// counter. Every key also tracks a seen vector mapping node ids to the
// highest counter that node has issued under that key.
package siblings

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	ErrInvalidCap   = errors.New("siblings: cap must be >= 1")
	ErrEmptyKey     = errors.New("siblings: key is empty")
	ErrEmptyID      = errors.New("siblings: node id is empty")
	ErrEmptyCtxNode = errors.New("siblings: ctx contains empty node id")
	ErrCtxAhead     = errors.New("siblings: ctx count exceeds seen for key")
	ErrCapExceeded  = errors.New("siblings: sibling count would exceed cap")
)

// Dot identifies a single write: node id plus its per-key counter.
type Dot struct {
	ID string
	N  uint64
}

// Sibling is a value tagged with the dot of the write that produced it.
type Sibling struct {
	Dot   Dot
	Value string
}

type keyState struct {
	sibs []Sibling
	seen map[string]uint64
}

func (ks *keyState) hasDot(d Dot) bool {
	for _, sib := range ks.sibs {
		if sib.Dot == d {
			return true
		}
	}
	return false
}

func (ks *keyState) clone() *keyState {
	sibs := make([]Sibling, len(ks.sibs))
	copy(sibs, ks.sibs)
	seen := make(map[string]uint64, len(ks.seen))
	for node, n := range ks.seen {
		seen[node] = n
	}
	return &keyState{sibs: sibs, seen: seen}
}

// Store is a concurrency-safe dot-version-vector sibling store.
type Store struct {
	mu   sync.Mutex
	cap  int
	keys map[string]*keyState
}

// NewStore returns a Store allowing at most cap siblings per key.
func NewStore(capacity int) (*Store, error) {
	if capacity < 1 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidCap, capacity)
	}
	return &Store{cap: capacity, keys: make(map[string]*keyState)}, nil
}

// Cap returns the per-key sibling limit.
func (s *Store) Cap() int {
	return s.cap
}

// Put writes value under key on behalf of node id using write context ctx.
//
// The new dot is (id, seen[id]+1). Every sibling whose dot (j, m) satisfies
// ctx[j] >= m is removed, then the new sibling is added. Validation happens
// before any mutation, in this order, reporting only the first failure:
// empty key, empty id, empty ctx node id, ctx count ahead of the key's seen
// vector, and sibling count exceeding Cap after pruning.
func (s *Store) Put(key, id string, ctx map[string]uint64, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if key == "" {
		return ErrEmptyKey
	}
	if id == "" {
		return ErrEmptyID
	}
	for node := range ctx {
		if node == "" {
			return ErrEmptyCtxNode
		}
	}
	ks := s.keys[key]
	var seen map[string]uint64
	if ks != nil {
		seen = ks.seen
	}
	for node, c := range ctx {
		if c > seen[node] {
			return fmt.Errorf("%w: node %q ctx=%d seen=%d", ErrCtxAhead, node, c, seen[node])
		}
	}
	var kept []Sibling
	if ks != nil {
		kept = make([]Sibling, 0, len(ks.sibs)+1)
		for _, sib := range ks.sibs {
			if ctx[sib.Dot.ID] >= sib.Dot.N {
				continue
			}
			kept = append(kept, sib)
		}
	}
	if len(kept)+1 > s.cap {
		return fmt.Errorf("%w: cap=%d kept=%d", ErrCapExceeded, s.cap, len(kept))
	}
	n := seen[id] + 1
	kept = append(kept, Sibling{Dot: Dot{ID: id, N: n}, Value: value})
	if ks == nil {
		ks = &keyState{seen: make(map[string]uint64)}
		s.keys[key] = ks
	}
	ks.sibs = kept
	ks.seen[id] = n
	return nil
}

// Get returns the siblings of key sorted by (id, n) and a copy of the
// key's seen vector as the read context.
func (s *Store) Get(key string) ([]Sibling, map[string]uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keys[key]
	if ks == nil {
		return []Sibling{}, map[string]uint64{}
	}
	sibs := make([]Sibling, len(ks.sibs))
	copy(sibs, ks.sibs)
	sort.Slice(sibs, func(i, j int) bool {
		if sibs[i].Dot.ID != sibs[j].Dot.ID {
			return sibs[i].Dot.ID < sibs[j].Dot.ID
		}
		return sibs[i].Dot.N < sibs[j].Dot.N
	})
	ctx := make(map[string]uint64, len(ks.seen))
	for node, n := range ks.seen {
		ctx[node] = n
	}
	return sibs, ctx
}

// Keys returns the sorted list of keys present in the store.
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.keys))
	for k := range s.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Merge folds a snapshot of other into s. It is not bounded by Cap.
//
// Per key, a sibling s of this side is kept iff the other side holds the
// same dot or the other side's seen[s.id] < s.n; siblings of the other side
// are kept symmetrically. Kept siblings are unioned and deduplicated by dot
// (lexicographically larger value wins on conflict), and seen vectors are
// merged per node by maximum. Keys present on only one side are copied
// whole. Merging a store into itself is a no-op.
func (s *Store) Merge(other *Store) {
	if other == nil || other == s {
		return
	}
	snap := other.snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, oks := range snap.keys {
		ks := s.keys[key]
		if ks == nil {
			s.keys[key] = oks.clone()
			continue
		}
		dots := make(map[Dot]string, len(ks.sibs)+len(oks.sibs))
		for _, sib := range ks.sibs {
			if oks.hasDot(sib.Dot) || oks.seen[sib.Dot.ID] < sib.Dot.N {
				dots[sib.Dot] = sib.Value
			}
		}
		for _, sib := range oks.sibs {
			if ks.hasDot(sib.Dot) || ks.seen[sib.Dot.ID] < sib.Dot.N {
				if v, ok := dots[sib.Dot]; !ok || sib.Value > v {
					dots[sib.Dot] = sib.Value
				}
			}
		}
		sibs := make([]Sibling, 0, len(dots))
		for d, v := range dots {
			sibs = append(sibs, Sibling{Dot: d, Value: v})
		}
		ks.sibs = sibs
		for node, n := range oks.seen {
			if n > ks.seen[node] {
				ks.seen[node] = n
			}
		}
	}
}

func (s *Store) snapshot() *Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := &Store{cap: s.cap, keys: make(map[string]*keyState, len(s.keys))}
	for k, ks := range s.keys {
		cp.keys[k] = ks.clone()
	}
	return cp
}
