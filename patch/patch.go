package patch

import (
	"errors"
	"sync"

	"ontology/udiff"
)

// Sentinel errors for the four distinguishable failure classes.
var (
	ErrFormat      = udiff.ErrFormat
	ErrContext     = errors.New("patch: context mismatch")
	ErrOutOfOffset = errors.New("patch: match outside fuzz range")
	ErrTooDifferent = udiff.ErrLimits
)

// HunkError identifies the failing hunk (0-based) and reason.
type HunkError struct {
	Hunk  int
	Class error
}

func (e *HunkError) Error() string { return e.Class.Error() }
func (e *HunkError) Unwrap() error { return e.Class }

// Apply atomically applies p to text with fuzz F offset lines.
func Apply(text string, p *udiff.Patch, F int) (string, error) {
	return text, nil
}

// Reverse swaps old/new directions of the patch in place and returns it.
func Reverse(p *udiff.Patch) *udiff.Patch { return p }

// Commit is one Store application record in submission order.
type Commit struct {
	OK bool
}

// Store is a versioned single-document store.
type Store struct {
	mu      sync.Mutex
	text    string
	version int
	log     []Commit
}

// NewStore creates a store at version 0.
func NewStore(text string) *Store { return &Store{text: text} }

// Snapshot returns the current text, version and commit log.
func (s *Store) Snapshot() (string, int, []Commit) {
	return s.text, s.version, append([]Commit(nil), s.log...)
}

// Apply applies a patch on a consistent snapshot and bumps version on success.
func (s *Store) Apply(p *udiff.Patch, F int) bool { return false }
