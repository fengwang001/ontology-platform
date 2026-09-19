package ontology

import (
	"crypto/rand"
	"sort"
	"sync"
	"sync/atomic"
)

// Object is a single stored element: a string primary key plus one int field.
type Object struct {
	Key   string
	Value int
}

// Store is an in-memory collection of objects ordered by string primary key.
// It is safe for concurrent use. Writes never block on ongoing traversals.
type Store struct {
	mu     sync.RWMutex
	keys   []string // sorted primary keys
	values map[string]int
	insCnt atomic.Int64 // cumulative real inserts (new keys)
	delCnt atomic.Int64 // cumulative real deletes (existing keys)

	sessMu   sync.Mutex
	sessions map[uint64]*session
	nextID   atomic.Uint64

	macKey [32]byte // per-store cursor integrity key
}

// NewStore returns an empty Store with a fresh random cursor key.
func NewStore() *Store {
	s := &Store{
		values:   make(map[string]int),
		sessions: make(map[uint64]*session),
	}
	if _, err := rand.Read(s.macKey[:]); err != nil {
		panic(err)
	}
	return s
}

// Put inserts or updates the object with the given primary key.
func (s *Store) Put(key string, value int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; ok {
		s.values[key] = value
		return
	}
	i := sort.SearchStrings(s.keys, key)
	s.keys = append(s.keys, "")
	copy(s.keys[i+1:], s.keys[i:])
	s.keys[i] = key
	s.values[key] = value
	s.insCnt.Add(1)
}

// Delete removes the object with the given primary key, if present.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; !ok {
		return
	}
	i := sort.SearchStrings(s.keys, key)
	s.keys = append(s.keys[:i], s.keys[i+1:]...)
	delete(s.values, key)
	s.delCnt.Add(1)
}

// Get returns the value stored under key.
func (s *Store) Get(key string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[key]
	return v, ok
}

// newSession snapshots the current key order and registers the session.
func (s *Store) newSession() *session {
	s.mu.RLock()
	keys := make([]string, len(s.keys))
	copy(keys, s.keys)
	s.mu.RUnlock()

	sess := &session{
		id:      s.nextID.Add(1),
		keys:    keys,
		set:     make(map[string]struct{}, len(keys)),
		baseIns: s.insCnt.Load(),
		baseDel: s.delCnt.Load(),
	}
	for _, k := range keys {
		sess.set[k] = struct{}{}
	}
	s.sessMu.Lock()
	s.sessions[sess.id] = sess
	s.sessMu.Unlock()
	return sess
}

func (s *Store) findSession(id uint64) (*session, bool) {
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}
