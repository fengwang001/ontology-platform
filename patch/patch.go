// Package patch applies parsed unified diffs to text: exact or offset
// relocation of hunks, atomic rejection, reverse application, and a
// concurrent multi-document store with versioning. All state is in memory.
package patch

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/edit"
	"ontology/lines"
	"ontology/udiff"
)

// Distinguishable failure categories.
var (
	ErrContext = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: match outside offset window")
	ErrLimit   = errors.New("patch: resource limit exceeded")
)

// HunkError reports which hunk (1-based Index) failed and why.
type HunkError struct {
	Index int
	Err   error
}

func (e *HunkError) Error() string { return fmt.Sprintf("patch: hunk %d: %v", e.Index, e.Err) }
func (e *HunkError) Unwrap() error { return e.Err }

// Options configures Apply. Fuzz is the relocation window in lines;
// zero MaxBytes/MaxHunks mean unlimited.
type Options struct {
	Fuzz, MaxBytes, MaxHunks int
}

// Apply applies patch text p to src, returning the new text. On any
// failure src is returned unchanged conceptually: the result is nil and
// no state is mutated (inputs are never modified).
func Apply(src, p []byte, opts Options) ([]byte, error) { return apply(src, p, opts, false) }

// Reverse applies the inverse of p to src (i.e. turns the new text back
// into the old one).
func Reverse(src, p []byte, opts Options) ([]byte, error) { return apply(src, p, opts, true) }

func apply(src, p []byte, opts Options, rev bool) ([]byte, error) {
	if opts.MaxBytes > 0 && len(p) > opts.MaxBytes {
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrLimit, len(p), opts.MaxBytes)
	}
	f, err := udiff.Parse(p)
	if err != nil {
		return nil, err
	}
	if opts.MaxHunks > 0 && len(f.Hunks) > opts.MaxHunks {
		return nil, fmt.Errorf("%w: %d > %d hunks", ErrLimit, len(f.Hunks), opts.MaxHunks)
	}
	ls := lines.Split(src)
	delta, lastOff := 0, 0
	for i, h := range f.Hunks {
		if rev {
			h = invert(h)
		}
		pat, rep := splitLines(h)
		base := h.APos + delta + lastOff // DESIGN.md section 4
		at, err := locate(ls, pat, base, opts.Fuzz)
		if err != nil {
			return nil, &HunkError{Index: i + 1, Err: err}
		}
		out := make([][]byte, 0, len(ls)+len(rep)-len(pat))
		out = append(out, ls[:at]...)
		out = append(out, rep...)
		out = append(out, ls[at+len(pat):]...)
		delta += len(rep) - len(pat)
		lastOff = at - base
		ls = out
	}
	return lines.Join(ls), nil
}

// invert swaps old/new sides of a hunk for reverse application.
func invert(h udiff.Hunk) udiff.Hunk {
	h.APos, h.BPos = h.BPos, h.APos
	h.ACount, h.BCount = h.BCount, h.ACount
	for i, l := range h.Lines {
		if l.Kind == edit.Del {
			h.Lines[i].Kind = edit.Ins
		} else if l.Kind == edit.Ins {
			h.Lines[i].Kind = edit.Del
		}
	}
	return h
}

// splitLines splits a hunk body into the old-side pattern and the
// new-side replacement, keeping original terminators.
func splitLines(h udiff.Hunk) (pat, rep [][]byte) {
	for _, l := range h.Lines {
		switch l.Kind {
		case edit.Keep:
			pat, rep = append(pat, l.Text), append(rep, l.Text)
		case edit.Del:
			pat = append(pat, l.Text)
		case edit.Ins:
			rep = append(rep, l.Text)
		}
	}
	return pat, rep
}

// locate finds where pat matches ls exactly, preferring the position
// nearest base (earliest on ties) within +/- fuzz lines.
func locate(ls, pat [][]byte, base, fuzz int) (int, error) {
	best, bestD, foundAny := -1, 0, false
	for p := 0; p+len(pat) <= len(ls); p++ {
		match := true
		for i, l := range pat {
			if !bytes.Equal(ls[p+i], l) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		foundAny = true
		d := base - p
		if d < 0 {
			d = -d
		}
		if d <= fuzz && (best < 0 || d < bestD) {
			best, bestD = p, d
		}
	}
	if best >= 0 {
		return best, nil
	}
	if foundAny {
		return 0, ErrOffset
	}
	return 0, ErrContext
}

// Commit records one successful Store.Apply in commit order.
type Commit struct {
	Doc     string
	Version int
	Patch   []byte
}

// Store is a concurrent multi-document store. Each Apply is decided on a
// consistent snapshot and commits atomically; failures change nothing.
type Store struct {
	mu   sync.Mutex
	docs map[string][]byte
	vers map[string]int
	log  []Commit
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{docs: map[string][]byte{}, vers: map[string]int{}} }

// Put sets a document's initial text at version 0.
func (s *Store) Put(doc string, text []byte) {
	s.mu.Lock()
	s.docs[doc] = append([]byte(nil), text...)
	s.mu.Unlock()
}

// Apply applies p to doc under the store lock: the decision uses a
// consistent snapshot, success swaps the text and bumps the version,
// failure leaves the document untouched.
func (s *Store) Apply(doc string, p []byte, opts Options) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := Apply(s.docs[doc], p, opts)
	if err != nil {
		return 0, err
	}
	s.docs[doc] = next
	s.vers[doc]++
	s.log = append(s.log, Commit{Doc: doc, Version: s.vers[doc], Patch: append([]byte(nil), p...)})
	return s.vers[doc], nil
}

// Snapshot returns the current text and version of doc.
func (s *Store) Snapshot(doc string) ([]byte, int) {
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
