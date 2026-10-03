// Package store holds every cached variant and performs deterministic
// capacity accounting and eviction. All methods are safe for concurrent use.
package store

import (
	"sync"

	"ontology/key"
)

// WhyLimit labels which constraint evicted a variant.
const (
	WhyPathLimit = "path-limit"
	WhyCapacity  = "capacity"
)

// Entry is one stored response variant.
type Entry struct {
	Path     string
	ID       key.Identity
	Status   int
	Body     []byte
	Size     int64
	StoredAt int64
	TTL      int64
	Seq      uint64
}

// Expiry is the global eviction ordering key: storedAt+TTL.
func (e *Entry) Expiry() int64 { return e.StoredAt + e.TTL }

// Eviction records one removed variant with the constraint that caused it.
type Eviction struct {
	Entry *Entry
	Why   string
}

// Store is the global variant container.
type Store struct {
	mu    sync.Mutex
	cap   int64
	maxV  int
	seq   uint64
	bytes int64
	// path -> identity-encode -> entry
	paths map[string]map[string]*Entry
}

// New creates a Store with the byte capacity and per-path variant limit.
func New(capBytes int64, maxVariants int) *Store {
	return &Store{
		cap:   capBytes,
		maxV:  maxVariants,
		paths: make(map[string]map[string]*Entry),
	}
}

// NextSeq allocates a monotonically increasing insertion sequence number.
func (s *Store) NextSeq() uint64 {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	return seq
}

// FindHit selects a fresh variant for the request. Private variants win over
// public ones; within the same class the later StoredAt wins, then the higher
// Seq. Stale variants are ignored.
func (s *Store) FindHit(path, subject string, head map[string]string, now int64) *Entry {
	s.mu.Lock()
	variants := s.paths[path]
	candidates := make([]*Entry, 0, len(variants))
	for _, entry := range variants {
		candidates = append(candidates, entry)
	}
	s.mu.Unlock()

	var bestPrivate, bestPublic *Entry
	for _, entry := range candidates {
		if !s.fresh(entry, now) {
			continue
		}
		if !key.VaryMatches(entry.ID, head) {
			continue
		}
		isPrivate := entry.ID.Owner != key.PublicScope
		if isPrivate {
			if subject == "" || string(entry.ID.Owner) != subject {
				continue
			}
			bestPrivate = pickLater(bestPrivate, entry)
			continue
		}
		bestPublic = pickLater(bestPublic, entry)
	}
	if bestPrivate != nil {
		return bestPrivate
	}
	return bestPublic
}

// Put removes the same-identity variant, enforces the per-path variant limit
// (earliest StoredAt, then lowest Seq) and the global byte limit (smallest
// storedAt+TTL, then lowest Seq), then inserts atomically. Evicted entries are
// returned in eviction order; the caller must have guaranteed Size <= cap.
func (s *Store) Put(path string, incoming *Entry) []Eviction {
	s.mu.Lock()
	defer s.mu.Unlock()

	var evicted []Eviction
	variants := s.paths[path]
	if variants == nil {
		variants = make(map[string]*Entry)
		s.paths[path] = variants
	}

	encoded := incoming.ID.Encode()
	if old := variants[encoded]; old != nil {
		delete(variants, encoded)
		s.bytes -= old.Size
		evicted = append(evicted, Eviction{Entry: old, Why: "replace"})
	}

	for len(variants) >= s.maxV {
		victim := s.earliestInPathLocked(variants)
		delete(variants, victim.ID.Encode())
		s.bytes -= victim.Size
		evicted = append(evicted, Eviction{Entry: victim, Why: WhyPathLimit})
	}

	for s.bytes+incoming.Size > s.cap {
		victim := s.globalExpireFirstLocked()
		s.detachLocked(victim)
		evicted = append(evicted, Eviction{Entry: victim, Why: WhyCapacity})
	}

	// Eviction may have emptied and unlinked this very path map; relink it.
	s.paths[path] = variants
	variants[encoded] = incoming
	s.bytes += incoming.Size
	return evicted
}

// InvalidatePath removes every variant of a path (public and all subjects).
func (s *Store) InvalidatePath(path string) []*Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	variants := s.paths[path]
	if len(variants) == 0 {
		return nil
	}
	removed := make([]*Entry, 0, len(variants))
	for _, entry := range variants {
		s.bytes -= entry.Size
		removed = append(removed, entry)
	}
	delete(s.paths, path)
	return removed
}

// Bytes reports the current total byte usage.
func (s *Store) Bytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}

func (s *Store) fresh(entry *Entry, now int64) bool {
	return now < entry.StoredAt+entry.TTL
}

func (s *Store) detachLocked(entry *Entry) {
	variants := s.paths[entry.Path]
	delete(variants, entry.ID.Encode())
	if len(variants) == 0 {
		delete(s.paths, entry.Path)
	}
	s.bytes -= entry.Size
}

func (s *Store) earliestInPathLocked(variants map[string]*Entry) *Entry {
	var victim *Entry
	for _, entry := range variants {
		if victim == nil ||
			entry.StoredAt < victim.StoredAt ||
			(entry.StoredAt == victim.StoredAt && entry.Seq < victim.Seq) {
			victim = entry
		}
	}
	return victim
}

func (s *Store) globalExpireFirstLocked() *Entry {
	var victim *Entry
	for _, variants := range s.paths {
		for _, entry := range variants {
			if victim == nil ||
				entry.Expiry() < victim.Expiry() ||
				(entry.Expiry() == victim.Expiry() && entry.Seq < victim.Seq) {
				victim = entry
			}
		}
	}
	return victim
}

// pickLater chooses the later-stored variant within one ownership class,
// breaking ties by the higher insertion sequence.
func pickLater(best, candidate *Entry) *Entry {
	if best == nil || candidate.StoredAt > best.StoredAt ||
		(candidate.StoredAt == best.StoredAt && candidate.Seq > best.Seq) {
		return candidate
	}
	return best
}
