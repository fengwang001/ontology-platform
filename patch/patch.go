// Package patch applies parsed unified diffs to text, with offset
// search, atomic rejection, reverse application, and a concurrent
// multi-document store.
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
	// ErrContext: no position in the document matches the hunk.
	ErrContext = errors.New("patch: context mismatch")
	// ErrOffset: matches exist but all lie outside the fuzz window.
	ErrOffset = errors.New("patch: offset out of range")
)

// Apply applies p to src. It is atomic: if any hunk fails, it returns
// an error "hunk N: <category>" and src is returned unchanged (nil).
// fuzz is the search radius in lines around the recorded position.
func Apply(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	srcLines := lines.Split(src)
	var out [][]byte
	cursor, lastOff := 0, 0
	for hi, h := range p.Hunks {
		old := side(h, '-')
		pos, err := locate(srcLines, old, h.OldIdx+lastOff, fuzz)
		if err != nil {
			return nil, fmt.Errorf("hunk %d: %w", hi+1, err)
		}
		if pos < cursor {
			return nil, fmt.Errorf("hunk %d: %w", hi+1, ErrContext)
		}
		out = append(out, srcLines[cursor:pos]...)
		out = append(out, side(h, '+')...)
		cursor = pos + len(old)
		lastOff = pos - h.OldIdx
	}
	out = append(out, srcLines[cursor:]...)
	return lines.Join(out), nil
}

// Reverse applies the inverse of p to src (turning the new text back
// into the old text).
func Reverse(src []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	q := &udiff.Patch{OldName: p.NewName, NewName: p.OldName}
	for _, h := range p.Hunks {
		h2 := hunk.Hunk{
			OldIdx: h.NewIdx, OldCount: h.NewCount,
			NewIdx: h.OldIdx, NewCount: h.OldCount,
		}
		for _, l := range h.Lines {
			switch l.Kind {
			case '-':
				h2.Lines = append(h2.Lines, hunk.Line{Kind: '+', Text: l.Text})
			case '+':
				h2.Lines = append(h2.Lines, hunk.Line{Kind: '-', Text: l.Text})
			default:
				h2.Lines = append(h2.Lines, l)
			}
		}
		q.Hunks = append(q.Hunks, h2)
	}
	return Apply(src, q, fuzz)
}

// side returns the old ('-') or new ('+') line sequence of a hunk.
func side(h hunk.Hunk, kind byte) [][]byte {
	var out [][]byte
	for _, l := range h.Lines {
		if l.Kind == kind || l.Kind == ' ' {
			out = append(out, l.Text)
		}
	}
	return out
}

// locate finds where old matches in src, nearest to guess within ±fuzz;
// ties go to the earlier position.
func locate(src, old [][]byte, guess, fuzz int) (int, error) {
	if len(old) == 0 {
		switch {
		case guess < 0:
			return 0, nil
		case guess > len(src):
			return len(src), nil
		default:
			return guess, nil
		}
	}
	best, bestDist, any := -1, 0, false
	for i := 0; i+len(old) <= len(src); i++ {
		if !matchAt(src, old, i) {
			continue
		}
		any = true
		d := guess - i
		if d < 0 {
			d = -d
		}
		if d <= fuzz && (best < 0 || d < bestDist) {
			best, bestDist = i, d
		}
	}
	if best >= 0 {
		return best, nil
	}
	if any {
		return -1, ErrOffset
	}
	return -1, ErrContext
}

func matchAt(src, old [][]byte, i int) bool {
	for j, l := range old {
		if !bytes.Equal(src[i+j], l) {
			return false
		}
	}
	return true
}

// Commit records one successful application in the store's log.
type Commit struct {
	Doc     string
	Version int
	Patch   *udiff.Patch
}

// Store is an in-memory multi-document store with per-document versions.
type Store struct {
	mu   sync.Mutex
	docs map[string][]byte
	vers map[string]int
	log  []Commit
}

func NewStore() *Store {
	return &Store{docs: map[string][]byte{}, vers: map[string]int{}}
}

// Put creates or replaces a document without logging a commit.
func (s *Store) Put(doc string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[doc] = text
}

// Apply applies p to doc on a consistent snapshot: on success the text
// is atomically replaced and the version incremented; on failure the
// document is left untouched.
func (s *Store) Apply(doc string, p *udiff.Patch, fuzz int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := Apply(s.docs[doc], p, fuzz)
	if err != nil {
		return err
	}
	s.docs[doc] = next
	s.vers[doc]++
	s.log = append(s.log, Commit{Doc: doc, Version: s.vers[doc], Patch: p})
	return nil
}

// State returns the current text and version of doc.
func (s *Store) State(doc string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[doc], s.vers[doc]
}

// Log returns the commit log in commit order.
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}
