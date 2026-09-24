package patch

import (
	"sync"

	"ontology/udiff"
)

// Commit records one successful application in commit order.
type Commit struct {
	Doc     string
	Version int
	Diff    []byte
}

type doc struct {
	text    []byte
	version int
}

// Store is an in-memory multi-document store safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	docs    map[string]*doc
	commits []Commit
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{docs: map[string]*doc{}} }

// Put creates or replaces a document at version 0.
func (s *Store) Put(name string, text []byte) {
	s.mu.Lock()
	s.docs[name] = &doc{text: append([]byte(nil), text...)}
	s.mu.Unlock()
}

// Get returns a snapshot copy and the current version.
func (s *Store) Get(name string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	if d == nil {
		return nil, 0
	}
	return append([]byte(nil), d.text...), d.version
}

// Result reports one application attempt.
type Result struct {
	Version int
	Err     error
}

// Apply validates the patch against the current snapshot and, only on full
// success, atomically replaces the text and bumps the version. Failed
// attempts leave state untouched.
func (s *Store) Apply(name string, diff []byte, opt Options, lim udiff.Limits) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	if d == nil {
		d = &doc{}
		s.docs[name] = d
	}
	p, err := udiff.Parse(diff, lim)
	if err != nil {
		return Result{Version: d.version, Err: classifyParse(err)}
	}
	snap := append([]byte(nil), d.text...)
	out, err := Apply(snap, p, opt)
	if err != nil {
		return Result{Version: d.version, Err: err}
	}
	d.text = out
	d.version++
	s.commits = append(s.commits, Commit{Doc: name, Version: d.version, Diff: append([]byte(nil), diff...)})
	return Result{Version: d.version}
}

// Replay returns the text obtained by replaying the recorded successful
// commits for name serially starting from the original (empty/base) text.
func (s *Store) Replay(name string, base []byte) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := append([]byte(nil), base...)
	for _, c := range s.commits {
		if c.Doc != name {
			continue
		}
		out, err := ApplyText(text, c.Diff, Options{Fuzz: 0}, udiff.Limits{})
		if err != nil {
			panic("store: recorded commit failed to replay")
		}
		text = out
	}
	return text
}

// Commits returns a copy of the commit log.
func (s *Store) Commits() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.commits...)
}
