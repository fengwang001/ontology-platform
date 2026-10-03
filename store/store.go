// Package store keeps cached entries and enforces byte and variant limits.
package store

import (
	"sync"

	"ontology/key"
)

// Entry is one stored response variant. Entries are immutable after creation.
type Entry struct {
	Path     string
	ID       key.Identity
	Status   int
	Body     []byte
	Size     int64
	StoredAt int64
	TTL      int64
	ExpireAt int64
	Seq      int64
	Public   bool
}

// PutResult reports the outcome of a Put: whether the new entry was
// retained and which entries were evicted or replaced.
type PutResult struct {
	Stored   bool
	Replaced *Entry
	Evicted  []*Entry
}

// Store is the capacity-managed entry container. All mutating operations
// are atomic with respect to each other.
type Store struct {
	cap int64
	max int

	mu    sync.RWMutex
	paths map[string][]*Entry
	bytes int64
	seq   int64
}

// New creates a Store with the given byte capacity and per-path variant limit.
func New(capacity int64, maxVariants int) *Store {
	return &Store{cap: capacity, max: maxVariants, paths: map[string][]*Entry{}}
}

// PathEntries returns a snapshot of the entries on path.
func (s *Store) PathEntries(path string) []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]*Entry(nil), s.paths[path]...)
}

// AllEntries returns all entries ordered by Seq.
func (s *Store) AllEntries() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Entry
	for _, es := range s.paths {
		out = append(out, es...)
	}
	sortBySeq(out)
	return out
}

func sortBySeq(es []*Entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j-1].Seq > es[j].Seq; j-- {
			es[j-1], es[j] = es[j], es[j-1]
		}
	}
}

// InvalidatePath removes every variant on path (all subjects).
func (s *Store) InvalidatePath(path string) []*Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.paths[path]
	for _, e := range old {
		s.bytes -= e.Size
	}
	delete(s.paths, path)
	return append([]*Entry(nil), old...)
}

// Bytes returns the current total stored bytes.
func (s *Store) Bytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bytes
}

// Len returns the number of stored variants.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, list := range s.paths {
		n += len(list)
	}
	return n
}

// Put atomically stores e: same-identity replacement, then per-path
// earliest-variant eviction, then global smallest-(ExpireAt,Seq) eviction.
func (s *Store) Put(e *Entry) PutResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := PutResult{Stored: true}

	list := s.paths[e.Path]
	kept := list[:0]
	for _, old := range list {
		if old.ID.Equal(e.ID) {
			res.Replaced = old
			s.bytes -= old.Size
			continue
		}
		kept = append(kept, old)
	}
	list = append(kept, e)
	s.paths[e.Path] = list

	e.Seq = s.seq + 1
	s.seq = e.Seq
	s.bytes += e.Size

	for len(list) > s.max {
		victim := 0
		for i := 1; i < len(list); i++ {
			if earlierStored(list[i], list[victim]) {
				victim = i
			}
		}
		res.Evicted = append(res.Evicted, list[victim])
		s.bytes -= list[victim].Size
		if list[victim] == e {
			res.Stored = false
		}
		list = append(list[:victim], list[victim+1:]...)
		s.paths[e.Path] = list
	}

	for s.bytes > s.cap {
		var vpath string
		var vi int
		var victim *Entry
		for p, es := range s.paths {
			for i, cand := range es {
				if victim == nil || earlierExpiry(cand, victim) {
					victim, vpath, vi = cand, p, i
				}
			}
		}
		es := s.paths[vpath]
		res.Evicted = append(res.Evicted, victim)
		s.bytes -= victim.Size
		if victim == e {
			res.Stored = false
		}
		es = append(es[:vi], es[vi+1:]...)
		if len(es) == 0 {
			delete(s.paths, vpath)
		} else {
			s.paths[vpath] = es
		}
	}

	if !res.Stored || len(s.paths[e.Path]) == 0 {
		if len(s.paths[e.Path]) == 0 {
			delete(s.paths, e.Path)
		}
	}
	return res
}

func earlierStored(a, b *Entry) bool {
	if a.StoredAt != b.StoredAt {
		return a.StoredAt < b.StoredAt
	}
	return a.Seq < b.Seq
}

func earlierExpiry(a, b *Entry) bool {
	if a.ExpireAt != b.ExpireAt {
		return a.ExpireAt < b.ExpireAt
	}
	return a.Seq < b.Seq
}
