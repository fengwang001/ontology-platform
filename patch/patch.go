// Package patch applies parsed unified diffs, forwards or backwards,
// and hosts a concurrent in-memory multi-document store.
package patch

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/lines"
	"ontology/udiff"
)

// Rejection classes for Apply; udiff.ErrFormat and edit.ErrTooBig are
// the other two classes, all distinguishable via errors.Is.
var (
	ErrContext = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: hunk beyond offset range")
	ErrNoDoc   = errors.New("patch: no such document")
)

// HunkError identifies the failing hunk (1-based) and its class.
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Index, e.Err) }
func (e *HunkError) Unwrap() error { return e.Err }

// Apply applies p to text. Each hunk may match up to fuzz lines away
// from its recorded position; on tie the nearer position wins, then the
// earlier. Any hunk failure rejects the whole patch (text is unchanged).
func Apply(text []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	return apply(text, p, fuzz, false)
}

// Reverse applies p backwards, turning the new text back into the old.
func Reverse(text []byte, p *udiff.Patch, fuzz int) ([]byte, error) {
	return apply(text, p, fuzz, true)
}

type span struct {
	at       int
	oldN     int
	newLines [][]byte
}

func apply(text []byte, p *udiff.Patch, fuzz int, rev bool) ([]byte, error) {
	ls := lines.Split(text)
	spans := make([]span, 0, len(p.Hunks))
	shift, prevEnd := 0, 0
	for i, h := range p.Hunks {
		start, count := h.AStart, h.ACount
		if rev {
			start, count = h.BStart, h.BCount
		}
		base := start - 1
		if count == 0 {
			base = start
		}
		base += shift
		var old, fresh [][]byte
		for _, op := range h.Ops {
			removal := op.Kind == '-'
			if rev {
				removal = op.Kind == '+'
			}
			switch {
			case op.Kind == ' ':
				old = append(old, op.Line)
				fresh = append(fresh, op.Line)
			case removal:
				old = append(old, op.Line)
			default:
				fresh = append(fresh, op.Line)
			}
		}
		at, err := locate(ls, old, base, prevEnd, fuzz)
		if err != nil {
			return nil, &HunkError{Index: i + 1, Err: err}
		}
		spans = append(spans, span{at, len(old), fresh})
		shift += at - base + len(fresh) - len(old)
		prevEnd = at + len(old)
	}
	var out [][]byte
	pos := 0
	for _, sp := range spans {
		out = append(out, ls[pos:sp.at]...)
		out = append(out, sp.newLines...)
		pos = sp.at + sp.oldN
	}
	out = append(out, ls[pos:]...)
	return lines.Join(out), nil
}

// locate searches base- d before base+ d (same distance: earlier first),
// classifying failure as ErrOffset when a match exists outside fuzz.
func locate(ls, pat [][]byte, base, lo, fuzz int) (int, error) {
	match := func(c int) bool {
		if c < lo || c+len(pat) > len(ls) {
			return false
		}
		for i := range pat {
			if !bytes.Equal(ls[c+i], pat[i]) {
				return false
			}
		}
		return true
	}
	if match(base) {
		return base, nil
	}
	for d := 1; d <= fuzz; d++ {
		if match(base - d) {
			return base - d, nil
		}
		if match(base + d) {
			return base + d, nil
		}
	}
	for c := lo; c+len(pat) <= len(ls); c++ {
		if match(c) {
			return 0, ErrOffset
		}
	}
	return 0, ErrContext
}

// Store is an in-memory multi-document store with per-doc versions.
type Store struct {
	mu   sync.Mutex
	docs map[string]*document
}

type document struct {
	text    []byte
	version int
	log     []*udiff.Patch
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{docs: map[string]*document{}} }

// Put creates or replaces a document, resetting version and log.
func (s *Store) Put(name string, text []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[name] = &document{text: append([]byte(nil), text...)}
}

// Apply atomically tests-and-sets name: success swaps the text and
// increments the version; failure leaves state untouched.
func (s *Store) Apply(name string, p *udiff.Patch, fuzz int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	if d == nil {
		return 0, ErrNoDoc
	}
	next, err := Apply(d.text, p, fuzz)
	if err != nil {
		return d.version, err
	}
	d.text = next
	d.version++
	d.log = append(d.log, p)
	return d.version, nil
}

// Get returns the current text and version of name.
func (s *Store) Get(name string) ([]byte, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	return append([]byte(nil), d.text...), d.version
}

// Log returns successfully committed patches in commit order.
func (s *Store) Log(name string) []*udiff.Patch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*udiff.Patch(nil), s.docs[name].log...)
}
