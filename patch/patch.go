// Package patch applies parsed unified diffs to text with bounded offset
// search, atomic rejection, reverse application, and a concurrent
// multi-document store.
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// Failure categories for hunk application.
var (
	ErrNoMatch = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: match outside offset range")
)

// HunkError identifies the hunk (0-based) that failed and its cause.
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Index+1, e.Err) }
func (e *HunkError) Unwrap() error { return e.Err }

// Apply applies p to text. fuzz bounds the search around recorded positions.
// On any hunk failure it returns nil and the input is left untouched.
func Apply(text []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	return apply(text, p, fuzz, false)
}

// Reverse applies p backwards (new -> old).
func Reverse(text []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	return apply(text, p, fuzz, true)
}

func apply(text []byte, p *udiff.Patch, fuzz int, rev bool) ([]byte, error) {
	src := lines.Split(text)
	var out [][]byte
	pos, delta, lastOff := 0, 0, 0
	for i, h := range p.Hunks {
		start, del := h.OldStart, h.OldCount
		if rev {
			start, del = h.NewStart, h.NewCount
		}
		s0 := start - 1
		if del == 0 {
			s0 = start
		}
		want := linesOf(h, rev)
		center := s0 + delta + lastOff
		at := find(src, pos, want, center, fuzz)
		if at < 0 {
			err := ErrNoMatch
			if find(src, pos, want, center, len(src)) >= 0 {
				err = ErrOffset
			}
			return nil, &HunkError{Index: i, Err: err}
		}
		out = append(out, src[pos:at]...)
		out = append(out, linesOf(h, !rev)...)
		pos = at + del
		lastOff = at - (s0 + delta)
		delta += len(linesOf(h, !rev)) - del
	}
	out = append(out, src[pos:]...)
	return lines.Join(out), nil
}

func linesOf(h hunk.Hunk, old bool) [][]byte {
	var out [][]byte
	for _, l := range h.Lines {
		if old && l.Kind != '+' || !old && l.Kind != '-' {
			out = append(out, l.Text)
		}
	}
	return out
}

// find returns the position in src (>= pos) where want exactly matches,
// searching within fuzz lines of center; nearest wins, ties go earlier.
func find(src [][]byte, pos int, want [][]byte, center, fuzz int) (best int) {
	best = -1
	for i := pos; i <= len(src)-len(want); i++ {
		if i < center-fuzz || i > center+fuzz {
			continue
		}
		if !match(src, i, want) {
			continue
		}
		d := i - center
		if d < 0 {
			d = -d
		}
		if best < 0 || d < abs(best-center) {
			best = i
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func match(src [][]byte, at int, want [][]byte) bool {
	for i, w := range want {
		if !lines.Equal(src[at+i], w) {
			return false
		}
	}
	return true
}

// Store is a concurrent multi-document store with versioned, atomic
// patch application and a commit log of successful applies.
type Store struct {
	mu                sync.Mutex
	text              map[string][]byte
	ver               map[string]int
	log               []Entry
	MaxBytes, MaxHunk int
}

// Entry is one committed apply, in commit order.
type Entry struct {
	ID      string
	Version int
	Patch   []byte
}

// NewStore creates a store with the given patch size limits (0 = no limit).
func NewStore(maxBytes, maxHunks int) *Store {
	return &Store{text: map[string][]byte{}, ver: map[string]int{}, MaxBytes: maxBytes, MaxHunk: maxHunks}
}

// Put sets the initial text of a document.
func (s *Store) Put(id string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text[id] = text
}

// Get returns the current text and version of a document.
func (s *Store) Get(id string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text[id], s.ver[id]
}

// Log returns the commit log of successful applies.
func (s *Store) Log() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.log...)
}

// Apply parses and applies a patch to the document. On success the document
// is atomically replaced and its version incremented; on failure nothing
// changes.
func (s *Store) Apply(id string, patch []byte, fuzz int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := udiff.Parse(patch, s.MaxBytes, s.MaxHunk)
	if err != nil {
		return 0, err
	}
	next, err := apply(s.text[id], p, fuzz, false)
	if err != nil {
		return 0, err
	}
	s.text[id] = next
	s.ver[id]++
	s.log = append(s.log, Entry{ID: id, Version: s.ver[id], Patch: patch})
	return s.ver[id], nil
}
