package patch

import (
	"sync"
)

// Commit records one successful application in commit order.
type Commit struct {
	Doc     string
	Version int
	Patch   []byte
	Result  []byte
}

type docState struct {
	text    []byte
	version int
}

// Store is a versioned, concurrency-safe in-memory document collection.
type Store struct {
	mu   sync.Mutex
	docs map[string]*docState
	log  []Commit
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{docs: map[string]*docState{}}
}

// Put creates or replaces a document at version 1.
func (s *Store) Put(name string, text []byte) {
	s.mu.Lock()
	s.docs[name] = &docState{text: append([]byte(nil), text...), version: 1}
	s.mu.Unlock()
}

// Result reports the snapshot and version of a document.
func (s *Store) Result(name string) ([]byte, int, bool) {
	s.mu.Lock()
	d, ok := s.docs[name]
	if !ok {
		s.mu.Unlock()
		return nil, 0, false
	}
	t, v := append([]byte(nil), d.text...), d.version
	s.mu.Unlock()
	return t, v, true
}

// ApplyResult is the outcome of one store application.
type ApplyResult struct {
	OK      bool
	Version int
	Text    []byte
	Err     error
}

// Apply applies text to one document on a consistent snapshot: either it
// succeeds and replaces the document with a bumped version plus a log entry,
// or the document is left unchanged.
func (s *Store) Apply(name string, text []byte, opt Options) ApplyResult {
	s.mu.Lock()
	d, ok := s.docs[name]
	if !ok {
		d = &docState{version: 0}
		s.docs[name] = d
	}
	snap := append([]byte(nil), d.text...)
	out, err := Apply(snap, text, opt)
	if err != nil {
		s.mu.Unlock()
		return ApplyResult{OK: false, Version: d.version, Err: err}
	}
	d.text = out
	d.version++
	s.log = append(s.log, Commit{Doc: name, Version: d.version,
		Patch: append([]byte(nil), text...), Result: append([]byte(nil), out...)})
	v := d.version
	s.mu.Unlock()
	return ApplyResult{OK: true, Version: v, Text: append([]byte(nil), out...)}
}

// Log returns the ordered successful commits for a document.
func (s *Store) Log(name string) []Commit {
	s.mu.Lock()
	var out []Commit
	for _, c := range s.log {
		if c.Doc == name {
			out = append(out, c)
		}
	}
	s.mu.Unlock()
	return out
}

// Replay serially reapplies a document's successful commits from a starting
// text, returning the final bytes. Useful to assert the concurrency result.
func Replay(start []byte, log []Commit, opt Options) ([]byte, error) {
	cur := start
	for _, c := range log {
		next, err := Apply(cur, c.Patch, opt)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}
