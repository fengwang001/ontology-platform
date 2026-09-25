// Package patch applies parsed unified diffs to in-memory documents:
// nearest-candidate offset application, atomic rejection, reverse
// application and a versioned multi-document store safe for concurrent use.
package patch

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

var (
	// ErrContext means a hunk matches nowhere in the target.
	ErrContext = errors.New("patch: hunk context does not match")
	// ErrOffset means a hunk matches only outside the fuzz window.
	ErrOffset = errors.New("patch: hunk offset outside allowed range")
)

// HunkError identifies the failing hunk and wraps the cause sentinel.
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string {
	return fmt.Sprintf("patch: hunk %d: %v", e.Index+1, e.Err)
}
func (e *HunkError) Unwrap() error { return e.Err }

func sides(h hunk.Hunk, rev bool) (seek, repl [][]byte) {
	for _, l := range h.Lines {
		inOld, inNew := l.Kind == ' ' || l.Kind == '-', l.Kind == ' ' || l.Kind == '+'
		if rev {
			inOld, inNew = inNew, inOld
		}
		if inOld {
			seek = append(seek, l.Text)
		}
		if inNew {
			repl = append(repl, l.Text)
		}
	}
	return
}

// findBest returns the valid position nearest guess (ties prefer earlier),
// restricted to |pos-guess| <= fuzz, or -1.
func findBest(cur, seek [][]byte, guess, fuzz int) int {
	best, bestD := -1, 0
	for p := 0; p+len(seek) <= len(cur); p++ {
		d := p - guess
		if d < 0 {
			d = -d
		}
		if d > fuzz {
			continue
		}
		ok := true
		for i, l := range seek {
			if !bytes.Equal(cur[p+i], l) {
				ok = false
				break
			}
		}
		if ok && (best < 0 || d < bestD || (d == bestD && p < best)) {
			best, bestD = p, d
		}
	}
	return best
}

func apply(src []byte, f *udiff.File, fuzz int, rev bool) ([]byte, error) {
	cur := append([][]byte(nil), lines.Split(src)...)
	drift := 0
	for hi, h := range f.Hunks {
		seek, repl := sides(h, rev)
		start, count := h.OldStart, h.OldCount
		if rev {
			start, count = h.NewStart, h.NewCount
		}
		pos := start
		if count > 0 {
			pos--
		}
		guess := pos + drift
		q := findBest(cur, seek, guess, fuzz)
		if q < 0 {
			cause := ErrContext
			if findBest(cur, seek, guess, len(cur)) >= 0 {
				cause = ErrOffset
			}
			return nil, &HunkError{Index: hi, Err: cause}
		}
		cur = append(append(cur[:q:q], repl...), cur[q+len(seek):]...)
		drift += q - guess + len(repl) - len(seek)
	}
	return lines.Join(cur), nil
}

// Apply applies f to src, searching fuzz lines around each recorded position.
// On any hunk failure src is returned untouched (the error wraps the cause).
func Apply(src []byte, f *udiff.File, fuzz int) ([]byte, error) {
	return apply(src, f, fuzz, false)
}

// Reverse applies f backwards: new-side rows are sought and old-side restored.
func Reverse(src []byte, f *udiff.File, fuzz int) ([]byte, error) {
	return apply(src, f, fuzz, true)
}

// Commit records one successful store application, in commit order.
type Commit struct {
	ID      string
	Version uint64
	Patch   *udiff.File
}

type docState struct {
	text []byte
	ver  uint64
}

// Store is a versioned multi-document store. Every application is evaluated
// against a consistent snapshot and committed atomically under one lock.
type Store struct {
	mu   sync.Mutex
	docs map[string]*docState
	log  []Commit
}

func NewStore() *Store { return &Store{docs: map[string]*docState{}} }

// Put creates or replaces a document at version 0.
func (s *Store) Put(id string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[id] = &docState{text: append([]byte(nil), text...)}
}

// Get returns the snapshot text and current version.
func (s *Store) Get(id string) ([]byte, uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs[id]
	if !ok {
		return nil, 0, false
	}
	return append([]byte(nil), d.text...), d.ver, true
}

// ApplyDoc applies f to the document; success bumps the version and appends
// to the commit log, failure leaves state byte-for-byte unchanged.
func (s *Store) ApplyDoc(id string, f *udiff.File, fuzz int) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[id]
	out, err := Apply(d.text, f, fuzz)
	if err != nil {
		return d.ver, err
	}
	d.text = out
	d.ver++
	s.log = append(s.log, Commit{ID: id, Version: d.ver, Patch: f})
	return d.ver, nil
}

// Log returns a copy of the successful-application log, in commit order.
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}
