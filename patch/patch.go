// Package patch applies unified diffs to text with offset search and
// atomic rejection, and provides a concurrent multi-document store.
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// Distinguishable failure categories.
var (
	ErrContext  = errors.New("patch: context mismatch")
	ErrOffset   = errors.New("patch: match beyond offset range")
	ErrTooLarge = errors.New("patch: patch too large")
)

// HunkError reports which hunk (0-based) failed and why.
type HunkError struct {
	Index  int
	Reason error
}

func (e *HunkError) Error() string { return fmt.Sprintf("hunk %d: %v", e.Index, e.Reason) }
func (e *HunkError) Unwrap() error { return e.Reason }

// Options: Fuzz is the offset search radius; zero limits mean no limit.
type Options struct{ Fuzz, MaxBytes, MaxHunks int }

// Apply applies a unified diff to old atomically: on any failure old is
// left untouched and the error identifies the hunk and category.
func Apply(old, patchText string, o Options) (string, error) { return apply(old, patchText, o, false) }

// Reverse applies a unified diff backwards (new -> old).
func Reverse(new, patchText string, o Options) (string, error) { return apply(new, patchText, o, true) }
func apply(text, patchText string, o Options, rev bool) (string, error) {
	if o.MaxBytes > 0 && len(patchText) > o.MaxBytes {
		return "", fmt.Errorf("%w: %d > %d bytes", ErrTooLarge, len(patchText), o.MaxBytes)
	}
	hs, err := udiff.Parse(patchText)
	if err != nil {
		return "", err
	}
	if o.MaxHunks > 0 && len(hs) > o.MaxHunks {
		return "", fmt.Errorf("%w: %d > %d hunks", ErrTooLarge, len(hs), o.MaxHunks)
	}
	if rev {
		for i := range hs {
			h := &hs[i]
			h.OldStart, h.NewStart = h.NewStart, h.OldStart
			h.OldCount, h.NewCount = h.NewCount, h.OldCount
			for j, l := range h.Lines {
				if l.Kind == '-' {
					h.Lines[j].Kind = '+'
				} else if l.Kind == '+' {
					h.Lines[j].Kind = '-'
				}
			}
		}
	}
	return run(lines.Split([]byte(text)), hs, o.Fuzz)
}

// run applies hunks; each search center tracks the accumulated shift.
func run(src []string, hs []hunk.Hunk, fuzz int) (string, error) {
	shift := 0
	for i, h := range hs {
		want, olds := h.OldStart+shift, h.OldLines()
		pos := locate(src, olds, want, fuzz)
		if pos < 0 {
			reason := ErrContext
			if locate(src, olds, want, len(src)) >= 0 {
				reason = ErrOffset
			}
			return "", &HunkError{i, reason}
		}
		out := make([]string, 0, len(src)+h.NewCount)
		out = append(out, src[:pos]...)
		for _, l := range h.Lines {
			if l.Kind != '-' {
				out = append(out, l.Text)
			}
		}
		src = append(out, src[pos+h.OldCount:]...)
		shift += pos - want + h.NewCount - h.OldCount
	}
	return string(lines.Join(src)), nil
}

// locate finds the exact match nearest to want (ties: lowest) in +-fuzz.
func locate(src, olds []string, want, fuzz int) int {
	best, bestDist := -1, 0
	for p := want - fuzz; p <= want+fuzz; p++ {
		if p < 0 || p+len(olds) > len(src) {
			continue
		}
		ok := true
		for j, t := range olds {
			ok = ok && src[p+j] == t
		}
		if d := max(p-want, want-p); ok && (best < 0 || d < bestDist) {
			best, bestDist = p, d
		}
	}
	return best
}

type Commit struct {
	ID      string
	Version int
	Patch   string
}

// Store is a concurrent multi-document store with per-document versions.
type Store struct {
	mu    sync.Mutex
	texts map[string]string
	vers  map[string]int
	log   []Commit
}

func NewStore() *Store { return &Store{texts: map[string]string{}, vers: map[string]int{}} }
func (s *Store) Put(id, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts[id], s.vers[id] = text, 0
}

func (s *Store) Apply(id, patchText string, o Options) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nt, err := Apply(s.texts[id], patchText, o)
	if err != nil {
		return 0, err
	}
	s.texts[id] = nt
	s.vers[id]++
	s.log = append(s.log, Commit{ID: id, Version: s.vers[id], Patch: patchText})
	return s.vers[id], nil
}

func (s *Store) State(id string) (string, int, []Commit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.texts[id], s.vers[id], append([]Commit(nil), s.log...)
}
