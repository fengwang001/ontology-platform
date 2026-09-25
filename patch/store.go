package patch

import "sync"

// Commit is one successful application recorded in submission order.
type Commit struct {
	DocID   string
	Version int
	Patch   []byte
}

type doc struct {
	text    []byte
	version int
	log     []Commit
	mu      sync.Mutex
}

// Store is an in-memory multi-document store. Each Apply is evaluated on
// a consistent snapshot and commits atomically with a version bump.
type Store struct {
	mu   sync.Mutex
	docs map[string]*doc
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{docs: map[string]*doc{}} }

// Put installs a document at version 0.
func (s *Store) Put(id string, text []byte) {
	s.mu.Lock()
	s.docs[id] = &doc{text: append([]byte(nil), text...), version: 0}
	s.mu.Unlock()
}

// Get returns a snapshot copy and the current version.
func (s *Store) Get(id string) ([]byte, int, bool) {
	s.mu.Lock()
	d, ok := s.docs[id]
	s.mu.Unlock()
	if !ok {
		return nil, 0, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), d.text...), d.version, true
}

// ApplyResult reports one application attempt.
type ApplyResult struct {
	OK         bool
	NewVersion int
	Err        error
}

// Apply runs on a consistent snapshot and either atomically replaces the
// document (version + 1, commit logged) or changes nothing.
func (s *Store) Apply(id string, p []byte, opt Options) ApplyResult {
	s.mu.Lock()
	d, ok := s.docs[id]
	s.mu.Unlock()
	if !ok {
		return ApplyResult{Err: &ApplyError{Hunk: -1, Category: ErrContext, Reason: "unknown doc"}}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out, err := Apply(d.text, p, opt)
	if err != nil {
		return ApplyResult{OK: false, NewVersion: d.version, Err: err}
	}
	d.text = out
	d.version++
	d.log = append(d.log, Commit{DocID: id, Version: d.version, Patch: append([]byte(nil), p...)})
	return ApplyResult{OK: true, NewVersion: d.version}
}

// Log returns the submission-order commit log for a document.
func (s *Store) Log(id string) []Commit {
	s.mu.Lock()
	d, ok := s.docs[id]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Commit(nil), d.log...)
}
