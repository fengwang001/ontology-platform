// Package patch applies parsed unified diffs to text (fuzz offset
// search, atomic rejection, reverse) and provides a concurrent store.
package patch

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// ErrContext: a hunk's old side matches nowhere in the target.
// ErrOffset: a match exists but only outside the fuzz range.
var (
	ErrContext = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: match beyond fuzz range")
)

// Apply applies p to src and returns the new text. If any hunk fails the
// whole patch is rejected with (nil, err) naming the 1-based hunk index.
func Apply(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	ls := lines.Split(src)
	delta, lastOff := 0, 0
	for i, h := range p.Hunks {
		idx0 := h.OldStart - 1
		if h.OldCount == 0 {
			idx0 = h.OldStart
		}
		want := idx0 + delta
		pos, ok := locate(ls, h.Ops, want+lastOff, fuzz)
		if !ok {
			err := ErrContext
			if _, any := locate(ls, h.Ops, 0, len(ls)); any {
				err = ErrOffset
			}
			return nil, fmt.Errorf("%w: hunk %d", err, i+1)
		}
		lastOff = pos - want
		delta += h.NewCount - h.OldCount
		var ins []string
		for _, op := range h.Ops {
			if op.Kind != '-' {
				ins = append(ins, op.Line)
			}
		}
		ls = slices.Concat(ls[:pos], ins, ls[pos+h.OldCount:])
	}
	return lines.Join(ls), nil
}

// locate finds the position closest to base (ties prefer the smaller
// index) within +-fuzz where the hunk's old side matches exactly.
func locate(ls []string, ops []edit.Op, base, fuzz int) (int, bool) {
	for d := 0; d <= fuzz; d++ {
		if matchAt(ls, ops, base-d) {
			return base - d, true
		}
		if d > 0 && matchAt(ls, ops, base+d) {
			return base + d, true
		}
	}
	return 0, false
}

func matchAt(ls []string, ops []edit.Op, pos int) bool {
	i := pos
	for _, op := range ops {
		if op.Kind == '+' {
			continue
		}
		if i < 0 || i >= len(ls) || ls[i] != op.Line {
			return false
		}
		i++
	}
	return i >= 0
}

// Reverse applies the inverse of p; given the new text it returns the old.
func Reverse(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	inv := &udiff.Patch{Old: p.New, New: p.Old, Hunks: make([]hunk.Hunk, len(p.Hunks))}
	for i, h := range p.Hunks {
		nh := hunk.Hunk{OldStart: h.NewStart, OldCount: h.NewCount,
			NewStart: h.OldStart, NewCount: h.OldCount, Ops: make([]edit.Op, len(h.Ops))}
		for j, op := range h.Ops {
			nh.Ops[j] = edit.Op{Kind: map[byte]byte{'-': '+', '+': '-', ' ': ' '}[op.Kind], Line: op.Line}
		}
		inv.Hunks[i] = nh
	}
	return Apply(src, inv, fuzz)
}

// Commit records one successful application, in commit order.
type Commit struct {
	Doc     string
	Version int
	Patch   *udiff.Patch
}

// Store is an in-memory multi-document store; applications are
// serialized under one mutex, so each commits on a consistent snapshot.
type Store struct {
	mu   sync.Mutex
	docs map[string][]byte
	vers map[string]int
	log  []Commit
}

func NewStore() *Store {
	return &Store{docs: map[string][]byte{}, vers: map[string]int{}}
}

func (s *Store) Put(doc string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[doc] = append([]byte(nil), text...)
}

// Apply applies p to doc: on success the text is replaced and the
// version incremented; on failure nothing changes at all.
func (s *Store) Apply(doc string, p *udiff.Patch, fuzz int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, err := Apply(s.docs[doc], p, fuzz)
	if err != nil {
		return 0, err
	}
	s.docs[doc] = out
	s.vers[doc]++
	s.log = append(s.log, Commit{Doc: doc, Version: s.vers[doc], Patch: p})
	return s.vers[doc], nil
}

func (s *Store) Get(doc string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.docs[doc]...), s.vers[doc]
}

// Log returns the commit log in commit order.
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}
