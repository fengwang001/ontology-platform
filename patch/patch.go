// Package patch applies parsed unified diffs with fuzz offsets, atomic
// rejection and reverse application, plus a versioned multi-doc store.
package patch

import (
	"errors"
	"fmt"
	"sync"

	"ontology/edit"
	"ontology/lines"
	"ontology/udiff"
)

// Distinct error categories; all wrap a sentinel for errors.Is.
var (
	ErrFormat    = udiff.ErrFormat
	ErrContext   = errors.New("patch: context mismatch")
	ErrOffset    = errors.New("patch: no match within fuzz")
	ErrTooLarge  = errors.New("patch: resource limit exceeded")
	ErrTooDiff   = edit.ErrTooDifferent
)

// ApplyError reports a failed hunk with its 0-based index and category.
type ApplyError struct {
	Hunk int
	Kind error // ErrContext or ErrOffset
	Msg  string
}

func (e *ApplyError) Error() string {
	return fmt.Sprintf("patch: hunk %d: %s: %s", e.Hunk, e.Kind, e.Msg)
}
func (e *ApplyError) Unwrap() error { return e.Kind }

// Options configures Apply. Fuzz is +/- lines searched around the recorded
// spot; MaxBytes/MaxHunks reject oversized patches before any change.
type Options struct {
	Fuzz                         int
	MaxBytes, MaxHunks           int
}

func rowLine(r *udiff.Row, old bool) lines.Line {
	if old {
		return lines.Line{Text: []byte(r.Text), EOL: r.OldEOL}
	}
	return lines.Line{Text: []byte(r.Text), EOL: r.NewEOL}
}

func match(src []lines.Line, h *udiff.Hunk, pos int) bool {
	j := pos
	for ri := range h.Rows {
		r := &h.Rows[ri]
		if r.Kind == edit.Insert {
			continue
		}
		if j >= len(src) || !lines.Equal(src[j], rowLine(r, true)) {
			return false
		}
		j++
	}
	return true
}

// Apply validates and atomically applies p to a. On any hunk failure or
// limit breach it returns a non-nil error and leaves a untouched.
func Apply(a []byte, p *udiff.Patch, o Options) ([]byte, error) {
	if o.MaxBytes > 0 && len(p.Render()) > o.MaxBytes {
		return nil, ErrTooLarge
	}
	if o.MaxHunks > 0 && len(p.Hunks) > o.MaxHunks {
		return nil, ErrTooLarge
	}
	src := lines.Split(a)
	out := make([]lines.Line, 0, len(src))
	cursor, shift := 0, 0
	for hi, h := range p.Hunks {
		recorded := h.S0 - 1 // b=0 => insert at index S0
		if h.N0 == 0 {
			recorded = h.S0
		}
		center := recorded + shift
		best, found := -1, false
		for d := 0; d <= o.Fuzz || (!found && d == 0); d++ {
			for _, cand := range []int{center - d, center + d} {
				if cand < 0 || cand > len(src)-cursor {
					continue
				}
				if match(src[cursor:], h, cand-cursor) {
					best, found, d = cand, true, o.Fuzz+1
					break
				}
			}
		}
		if !found {
			k := ErrOffset
			if o.Fuzz == 0 {
				k = ErrContext
			}
			return nil, &ApplyError{Hunk: hi, Kind: k, Msg: "hunk does not apply"}
		}
		out = append(out, src[cursor:best]...)
		oldN := 0
		for ri := range h.Rows {
			r := &h.Rows[ri]
			switch r.Kind {
			case edit.Equal:
				out = append(out, rowLine(r, false))
				oldN++
			case edit.Insert:
				out = append(out, rowLine(r, false))
			case edit.Delete:
				oldN++
			}
		}
		shift += (best - recorded)
		cursor = best + oldN
	}
	out = append(out, src[cursor:]...)
	return lines.Join(out), nil
}

// ApplyBytes parses then applies text. Parse errors wrap ErrFormat.
func ApplyBytes(a, text []byte, o Options) ([]byte, error) {
	p, err := udiff.Parse(text)
	if err != nil {
		return nil, err
	}
	return Apply(a, p, o)
}

// Reverse flips a patch in place (old<->new, '-' <-> '+') and returns it.
func Reverse(p *udiff.Patch) *udiff.Patch {
	p.OldLabel, p.NewLabel = p.NewLabel, p.OldLabel
	for _, h := range p.Hunks {
		h.S0, h.N0, h.S1, h.N1 = h.S1, h.N1, h.S0, h.N0
		for i := range h.Rows {
			r := &h.Rows[i]
			r.OldEOL, r.NewEOL = r.NewEOL, r.OldEOL
			r.OldNoNL, r.NewNoNL = r.NewNoNL, r.OldNoNL
			if r.Kind == edit.Delete {
				r.Kind = edit.Insert
			} else if r.Kind == edit.Insert {
				r.Kind = edit.Delete
			}
		}
}
	return p
}

// Entry is one versioned document.
type Entry struct {
	Text    []byte
	Version int
}

// Commit records one successful application in submission order.
type Commit struct {
	Doc     string
	Version int
}

// Store is an in-memory multi-document store safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	Docs    map[string]*Entry
	Log     []Commit
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{Docs: map[string]*Entry{}} }

// Apply runs p against doc on a consistent snapshot; on success it replaces
// the text atomically, bumps version and appends a commit. Failures change
// nothing. Returns the new version (or -1) and whether it applied.
func (s *Store) Apply(doc string, p *udiff.Patch, o Options) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.Docs[doc]
	var base []byte
	if e != nil {
		base = e.Text
	}
	next, err := patchApply(base, p, o)
	if err != nil {
		return -1, false, err
	}
	if e == nil {
		e = &Entry{}
		s.Docs[doc] = e
	}
	e.Text = next
	e.Version++
	s.Log = append(s.Log, Commit{Doc: doc, Version: e.Version})
	return e.Version, true, nil
}

// Get returns a snapshot copy of a document (nil when absent).
func (s *Store) Get(doc string) *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.Docs[doc]
	if e == nil {
		return nil
	}
	cp := append([]byte(nil), e.Text...)
	return &Entry{Text: cp, Version: e.Version}
}
