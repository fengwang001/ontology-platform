// Package patch applies parsed unified diffs to in-memory text: offset
// search, atomic rejection, reverse application and a versioned multi-file
// store safe for concurrent use. It depends on udiff and lines.
package patch

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

// Four distinguishable failure classes.
var (
	ErrFormat       = udiff.ErrFormat
	ErrContext      = errors.New("patch: context does not match target")
	ErrOutOfRange   = errors.New("patch: no match within fuzz offset range")
	ErrTooDifferent = errors.New("patch: source and patch content differ too much")
)

// ApplyError identifies the failing hunk and the failure class.
type ApplyError struct {
	Hunk   int   // 1-based hunk index
	Class  error // ErrContext or ErrOutOfRange
	Reason string
}

func (e *ApplyError) Error() string {
	return fmt.Sprintf("%s: hunk %d: %s", e.Class, e.Hunk, e.Reason)
}
func (e *ApplyError) Unwrap() error { return e.Class }

// Options configures Apply.
type Options struct {
	Fuzz     int // search radius in lines around the recorded position
	MaxBytes int // forwarded to the parser; 0 = unlimited
	MaxHunks int
}

// Apply parses and applies diff to target. On any failure the returned bytes
// are nil and target is unchanged (the operation never mutates input).
func Apply(target, diff []byte, opts Options) ([]byte, error) {
	p, err := udiff.Parse(diff, udiff.Limits{MaxBytes: opts.MaxBytes, MaxHunks: opts.MaxHunks})
	if err != nil {
		return nil, err
	}
	return ApplyParsed(target, p, opts.Fuzz)
}

// ApplyParsed applies an already-parsed patch with offset fuzz F.
func ApplyParsed(target []byte, p udiff.Patch, fuzz int) ([]byte, error) {
	cur := lines.Split(string(target))
	delta := 0
	for hi := range p.Hunks {
		pos, err := locate(cur, &p.Hunks[hi], delta, fuzz)
		if err != nil {
			return nil, &ApplyError{Hunk: hi + 1, Class: err}
		}
		ins := make([]lines.Line, 0)
		del := 0
		for _, it := range p.Hunks[hi].Items {
			switch it.Kind {
			case edit.Equal:
				del++
				ins = append(ins, lines.Line{Text: it.Text, EOL: it.EOL})
			case edit.Delete:
				del++
			case edit.Insert:
				ins = append(ins, lines.Line{Text: it.Text, EOL: it.EOL})
			}
		}
		cur = append(append(append([]lines.Line(nil), cur[:pos]...), ins...), cur[pos+del:]...)
		delta += len(ins) - del
	}
	return []byte(lines.Join(cur)), nil
}

// locate finds the hunk in cur, searching around its recorded old position
// shifted by the accumulated delta; nearest wins, ties prefer earlier.
func locate(cur []lines.Line, h *hunk.Hunk, delta, fuzz int) (int, error) {
	old := make([]lines.Line, 0)
	for _, it := range h.Items {
		if it.Kind == edit.Equal || it.Kind == edit.Delete {
			old = append(old, lines.Line{Text: it.Text, EOL: it.EOL})
		}
	}
	center := h.OldStart + delta // number of lines before the insertion point
	if h.OldCount > 0 {
		center = h.OldStart - 1 + delta // index of first replaced line
	}
	type cand struct{ pos, dist int }
	var cands []cand
	for pos := 0; pos+len(old) <= len(cur); pos++ {
		if lines.Equal(cur[pos:pos+len(old)], old) {
			cands = append(cands, cand{pos, abs(pos - center)})
		}
	}
	if len(cands) == 0 {
		return 0, ErrContext
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].pos < cands[j].pos
	})
	if cands[0].dist > fuzz {
		return 0, ErrOutOfRange
	}
	return cands[0].pos, nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Reverse swaps old/new sides of a patch, so it undoes the forward change.
func Reverse(p udiff.Patch) udiff.Patch {
	r := udiff.Patch{OldName: p.NewName, NewName: p.OldName, Context: p.Context, Hunks: make([]hunk.Hunk, len(p.Hunks))}
	for i, h := range p.Hunks {
		h.OldStart, h.NewStart = h.NewStart, h.OldStart
		h.OldCount, h.NewCount = h.NewCount, h.OldCount
		for j := range h.Items {
			switch h.Items[j].Kind {
			case edit.Delete:
				h.Items[j].Kind = edit.Insert
			case edit.Insert:
				h.Items[j].Kind = edit.Delete
			}
		}
		r.Hunks[i] = h
	}
	return r
}

// Commit is one successful application recorded by Store.
type Commit struct {
	Name  string
	Patch udiff.Patch
	Delta int // version increase, always 1
}

// Doc is a versioned document.
type Doc struct {
	Content []byte
	Version int
}

// Store is an in-memory multi-document store; zero value is ready.
type Store struct {
	mu   sync.Mutex
	docs map[string]*Doc
	log  []Commit
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{docs: map[string]*Doc{}}
}

// Put inserts or replaces a document at the given version.
func (s *Store) Put(name string, content []byte, version int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[name] = &Doc{Content: append([]byte(nil), content...), Version: version}
}

// Get returns a snapshot copy of a document.
func (s *Store) Get(name string) (Doc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs[name]
	if !ok {
		return Doc{}, false
	}
	return Doc{Content: append([]byte(nil), d.Content...), Version: d.Version}, true
}

// Apply validates p against the current snapshot and, on success, atomically
// replaces content and increments the version; failures change nothing.
func (s *Store) Apply(name string, p udiff.Patch, fuzz int) (Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.docs[name]
	if d == nil {
		d = &Doc{}
		s.docs[name] = d
	}
	out, err := ApplyParsed(d.Content, p, fuzz)
	if err != nil {
		return Doc{}, err
	}
	d.Content = out
	d.Version++
	s.log = append(s.log, Commit{Name: name, Patch: p, Delta: 1})
	return Doc{Content: append([]byte(nil), out...), Version: d.Version}, nil
}

// Log returns a copy of the successful-application log in commit order.
func (s *Store) Log() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Commit(nil), s.log...)
}

// Replay rebuilds content by serially applying the log to initial content.
func Replay(initial []byte, log []Commit) ([]byte, error) {
	cur := initial
	var err error
	for _, c := range log {
		cur, err = ApplyParsed(cur, c.Patch, 0)
		if err != nil {
			return nil, err
		}
	}
	return cur, nil
}
