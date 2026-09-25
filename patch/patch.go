// Package patch applies unified diffs and stores versioned documents.
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

// ErrNoMatch: context/deleted lines match nowhere in the target.
var ErrNoMatch = errors.New("patch: context mismatch")

// ErrOffset: a match exists, but outside the configured offset window.
var ErrOffset = errors.New("patch: match outside offset window")

// ApplyError identifies the failing hunk (0-based) and its cause.
type ApplyError struct {
	Hunk int
	Err  error
}

func (e *ApplyError) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Hunk+1, e.Err) }
func (e *ApplyError) Unwrap() error { return e.Err }

// Apply applies f to src with up to fuzz lines of offset per hunk.
func Apply(src []byte, f *udiff.File, fuzz int) ([]byte, error) {
	return run(src, f.Hunks, fuzz, false)
}

// Reverse applies f backwards, turning the new text back into the old.
func Reverse(src []byte, f *udiff.File, fuzz int) ([]byte, error) {
	return run(src, f.Hunks, fuzz, true)
}

func run(src []byte, hs []hunk.Hunk, fuzz int, rev bool) ([]byte, error) {
	out := lines.Split(src)
	shift := 0
	for i, h := range hs {
		old, newSide, pos := sides(h, rev)
		at := locate(out, old, pos+shift, fuzz)
		if at < 0 {
			err := ErrNoMatch
			if locate(out, old, pos+shift, len(out)+1) >= 0 {
				err = ErrOffset
			}
			return nil, &ApplyError{Hunk: i, Err: err}
		}
		next := make([][]byte, 0, len(out)-len(old)+len(newSide))
		next = append(next, out[:at]...)
		next = append(next, newSide...)
		out = append(next, out[at+len(old):]...)
		shift = at - pos + len(newSide) - len(old)
	}
	return lines.Join(out), nil
}

// sides extracts the old/new-side sequences and the 0-based old-side
// position of a hunk; rev swaps roles for reverse application.
func sides(h hunk.Hunk, rev bool) (old, newSide [][]byte, pos int) {
	start, count := h.OldStart, h.OldCount
	if rev {
		start, count = h.NewStart, h.NewCount
	}
	pos = start - 1
	if count == 0 {
		pos = start
	}
	for _, l := range h.Lines {
		k := l.Kind
		if rev && k != ' ' {
			k = '-' + '+' - k
		}
		if k == ' ' || k == '-' {
			old = append(old, l.Text)
		}
		if k == ' ' || k == '+' {
			newSide = append(newSide, l.Text)
		}
	}
	return old, newSide, pos
}

// locate finds where pat matches s within fuzz lines of guess.
func locate(s, pat [][]byte, guess, fuzz int) int {
	best, bestD := -1, 0
	lo, hi := max(guess-fuzz, 0), min(guess+fuzz, len(s)-len(pat))
scan:
	for p := lo; p <= hi; p++ {
		d := max(p-guess, guess-p)
		if best >= 0 && d >= bestD {
			continue
		}
		for i, l := range pat {
			if !bytes.Equal(s[p+i], l) {
				continue scan
			}
		}
		best, bestD = p, d
	}
	return best
}

// Store is an in-memory multi-document store with per-document versions.
type Store struct {
	mu   sync.Mutex
	docs map[string][]byte
	vers map[string]int
	log  []*udiff.File
}

func NewStore() *Store {
	return &Store{docs: map[string][]byte{}, vers: map[string]int{}}
}

func (s *Store) Put(name string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[name], s.vers[name] = text, 0
}

// Apply atomically applies f; failure leaves the document untouched.
func (s *Store) Apply(name string, f *udiff.File, fuzz int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := Apply(s.docs[name], f, fuzz)
	if err != nil {
		return s.vers[name], err
	}
	s.docs[name] = out
	s.vers[name]++
	s.log = append(s.log, f)
	return s.vers[name], nil
}

func (s *Store) Get(name string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[name], s.vers[name]
}

func (s *Store) Log() []*udiff.File {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*udiff.File(nil), s.log...)
}
