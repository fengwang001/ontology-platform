// Package patch applies parsed unified diffs to text with offset
// search and atomic rejection, plus a concurrent multi-document store.
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// ErrContext marks hunks whose context/deletion lines match nowhere.
var ErrContext = errors.New("patch: context mismatch")

// ErrOffset marks hunks that would match only beyond the offset range.
var ErrOffset = errors.New("patch: hunk outside offset range")

// ErrNoDoc marks operations on a missing document.
var ErrNoDoc = errors.New("patch: no such document")

// HunkError identifies the failing hunk (0-based) and its cause.
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Index, e.Err) }
func (e *HunkError) Unwrap() error { return e.Err }

// Apply applies p to src, searching +-fuzz lines around recorded
// positions. On any hunk failure it returns an error and src is
// untouched (the result is built separately).
func Apply(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	ls := lines.Split(src)
	var out [][]byte
	cursor, shift := 0, 0
	for i, h := range p.Hunks {
		base := h.OldStart - 1
		if h.OldCount == 0 {
			base = h.OldStart
		}
		base += shift
		pos, ok := locate(ls, h, base, fuzz, cursor)
		if !ok {
			return nil, &HunkError{Index: i, Err: classify(ls, h, base, fuzz)}
		}
		out = append(out, ls[cursor:pos]...)
		n := pos
		for _, l := range h.Lines {
			switch l.Kind {
			case ' ':
				out = append(out, ls[n])
				n++
			case '-':
				n++
			case '+':
				out = append(out, l.Text)
			}
		}
		shift += h.NewCount - h.OldCount + pos - base
		cursor = n
	}
	return lines.Join(append(out, ls[cursor:]...)), nil
}

// oldSide returns the context+deletion lines a hunk must match.
func oldSide(h hunk.Hunk) [][]byte {
	var old [][]byte
	for _, l := range h.Lines {
		if l.Kind != '+' {
			old = append(old, l.Text)
		}
	}
	return old
}

func matchAt(ls [][]byte, at int, old [][]byte) bool {
	if at < 0 || at+len(old) > len(ls) {
		return false
	}
	for i, o := range old {
		if string(ls[at+i]) != string(o) {
			return false
		}
	}
	return true
}

// locate tries base, then +-1..+-fuzz, nearer first, ties prefer lower.
func locate(ls [][]byte, h hunk.Hunk, base, fuzz, lo int) (int, bool) {
	old := oldSide(h)
	for d := 0; d <= fuzz; d++ {
		for _, s := range [2]int{base - d, base + d} {
			if s >= lo && matchAt(ls, s, old) {
				return s, true
			}
		}
	}
	return 0, false
}

// classify distinguishes "matches only beyond the offset range"
// (ErrOffset) from "matches nowhere" (ErrContext).
func classify(ls [][]byte, h hunk.Hunk, base, fuzz int) error {
	old := oldSide(h)
	for s := 0; s+len(old) <= len(ls); s++ {
		if (s < base-fuzz || s > base+fuzz) && matchAt(ls, s, old) {
			return ErrOffset
		}
	}
	return ErrContext
}

// Reverse applies the inverse of p to src.
func Reverse(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	return Apply(src, p.Reversed(), fuzz)
}

// Commit records one successful Store.Apply, in commit order.
type Commit struct {
	Doc     string
	Patch   *udiff.Patch
	Version int
}

// Store is an in-memory multi-document store with per-document
// versions and a commit log of successful patch applications.
type Store struct {
	mu   sync.Mutex
	docs map[string][]byte
	ver  map[string]int
	log  []Commit
}

func NewStore() *Store {
	return &Store{docs: map[string][]byte{}, ver: map[string]int{}}
}

// Put creates or replaces a document, resetting its version to 0.
func (s *Store) Put(doc string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[doc], s.ver[doc] = text, 0
}

// Get returns the document text and version.
func (s *Store) Get(doc string) ([]byte, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.docs[doc]
	if !ok {
		return nil, 0, ErrNoDoc
	}
	return t, s.ver[doc], nil
}

// Log returns the commit log in commit order.
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}

// Apply applies p to doc on a consistent snapshot: success swaps the
// text atomically and bumps the version; failure changes nothing.
func (s *Store) Apply(doc string, p *udiff.Patch, fuzz int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.docs[doc]
	if !ok {
		return 0, ErrNoDoc
	}
	out, err := Apply(t, p, fuzz)
	if err != nil {
		return 0, err
	}
	s.docs[doc] = out
	s.ver[doc]++
	s.log = append(s.log, Commit{Doc: doc, Patch: p, Version: s.ver[doc]})
	return s.ver[doc], nil
}
