// Package patch applies parsed unified diffs to text: exact or offset
// placement, atomic rejection, reverse application, and a concurrent
// multi-document store.
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// Failure classes for hunk placement.
var (
	ErrContext = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: offset out of range")
)

// HunkError reports the 1-based hunk that failed and why.
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Index, e.Err) }
func (e *HunkError) Unwrap() error { return e.Err }

// Apply applies p to src, allowing hunks to shift by at most fuzz lines.
// On any failure it returns an error and src is left untouched.
func Apply(src []byte, p udiff.FilePatch, fuzz int) ([]byte, error) {
	return apply(src, p, fuzz, false)
}

// Reverse applies p backwards (new side matched, old side produced).
func Reverse(src []byte, p udiff.FilePatch, fuzz int) ([]byte, error) {
	return apply(src, p, fuzz, true)
}

func side(h hunk.Hunk, del edit.OpKind, ins edit.OpKind) (match, emit []lines.Line) {
	for _, e := range h.Entries {
		switch e.Kind {
		case edit.Equal:
			match = append(match, e.Line)
			emit = append(emit, e.Line)
		case del:
			match = append(match, e.Line)
		case ins:
			emit = append(emit, e.Line)
		}
	}
	return match, emit
}

func apply(src []byte, p udiff.FilePatch, fuzz int, rev bool) ([]byte, error) {
	doc := lines.Split(src)
	type placement struct {
		pos  int
		emit []lines.Line
	}
	places := make([]placement, len(p.Hunks))
	delta, prevEnd := 0, 0
	for i, h := range p.Hunks {
		match, emit := side(h, edit.Del, edit.Ins)
		start, count := h.OldStart, h.OldCount
		if rev {
			match, emit = side(h, edit.Ins, edit.Del)
			start, count = h.NewStart, h.NewCount
		}
		base := start - 1
		if count == 0 {
			base = start // count 0: insert after line `start`
		}
		pos, anywhere, ok := locate(doc, match, base+delta, prevEnd, fuzz)
		if !ok {
			err := ErrContext
			if anywhere {
				err = ErrOffset
			}
			return nil, &HunkError{Index: i + 1, Err: err}
		}
		delta += pos - (base + delta)
		places[i] = placement{pos, emit}
		prevEnd = pos + len(match)
	}
	var out []lines.Line
	cur := 0
	for i, pl := range places {
		out = append(out, doc[cur:pl.pos]...)
		out = append(out, pl.emit...)
		cur = pl.pos
		if rev {
			_, m := side(p.Hunks[i], edit.Ins, edit.Del)
			cur += len(m)
		} else {
			_, m := side(p.Hunks[i], edit.Del, edit.Ins)
			cur += len(m)
		}
	}
	out = append(out, doc[cur:]...)
	return lines.Join(out), nil
}

// locate finds where match occurs in doc nearest to want (within fuzz
// lines, not before minPos). anywhere reports a match outside the range.
func locate(doc, match []lines.Line, want, minPos, fuzz int) (pos int, anywhere, ok bool) {
	best, bestDist := -1, 0
	for at := 0; at+len(match) <= len(doc); at++ {
		if !matchAt(doc, at, match) {
			continue
		}
		if at < minPos {
			continue
		}
		anywhere = true
		d := at - want
		if d < 0 {
			d = -d
		}
		if d > fuzz {
			continue
		}
		if best < 0 || d < bestDist {
			best, bestDist = at, d
		}
	}
	return best, anywhere, best >= 0
}

func matchAt(doc []lines.Line, at int, match []lines.Line) bool {
	for j, l := range match {
		if doc[at+j] != l {
			return false
		}
	}
	return true
}

// Commit records one successful application in store order.
type Commit struct {
	Doc     string
	Version int
	Patch   udiff.FilePatch
}

// Store is a concurrency-safe multi-document store with versioning.
type Store struct {
	mu   sync.Mutex
	docs map[string][]byte
	ver  map[string]int
	log  []Commit
}

func NewStore() *Store {
	return &Store{docs: map[string][]byte{}, ver: map[string]int{}}
}

func (s *Store) Create(name string, initial []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[name] = initial
}

// Apply applies p to doc name atomically; on success version increments
// and the commit is logged. On failure nothing changes.
func (s *Store) Apply(name string, p udiff.FilePatch, fuzz int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.docs[name]
	if !ok {
		return 0, errors.New("patch: unknown document")
	}
	next, err := Apply(cur, p, fuzz)
	if err != nil {
		return 0, err
	}
	s.docs[name] = next
	s.ver[name]++
	s.log = append(s.log, Commit{Doc: name, Version: s.ver[name], Patch: p})
	return s.ver[name], nil
}

func (s *Store) Text(name string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[name]
}

func (s *Store) Version(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ver[name]
}

func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}
