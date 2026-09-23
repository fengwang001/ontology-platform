// Package patch applies parsed unified diffs, including fuzz-offset
// lookup, atomic rejection, reverse application, and a concurrent
// versioned multi-document store.
package patch

import (
	"errors"

	"ontology/udiff"
)

// Options tunes application. Fuzz is the +/- line search window.
type Options struct {
	Fuzz int
}

// Error sentinels for the distinguishable failure classes.
var (
	ErrContext  = errors.New("patch: hunk context does not match")
	ErrFuzz     = errors.New("patch: matchable location outside fuzz window")
	ErrFormat   = errors.New("patch: malformed patch")
	ErrTooLarge = errors.New("patch: differences exceed configured limit")
)

// HunkError names the 0-based hunk that failed and why.
type HunkError struct {
	Hunk int
	Err  error
}

func (e *HunkError) Error() string { return "" }
func (e *HunkError) Unwrap() error { return e.Err }

// Apply puts p onto text. On any hunk failure text is returned
// byte-identical to the input together with a *HunkError.
func Apply(text []byte, p *udiff.Patch, opts Options) ([]byte, error) {
	return text, nil
}

// Reverse returns the reverse patch (old/new swapped).
func Reverse(p *udiff.Patch) *udiff.Patch { return nil }

// Doc is one versioned document in a Store.
type Doc struct {
	text    []byte
	version int
}

// Commit records one successful application, in commit order.
type Commit struct {
	Name    string
	Version int
	Patch   *udiff.Patch
}

// Store is a concurrency-safe multi-document store.
type Store struct{ mu struct{} }

// NewStore creates an empty store.
func NewStore() *Store { return &Store{} }

// Put creates or replaces a document at the given version.
func (s *Store) Put(name string, text []byte) {}

// Get returns a snapshot of the document and its version.
func (s *Store) Get(name string) ([]byte, int, bool) { return nil, 0, false }

// Apply applies p to the named document atomically, bumping its version
// and appending a commit on success.
func (s *Store) Apply(name string, p *udiff.Patch, opts Options) (int, error) {
	return 0, nil
}

// Log returns the ordered commit log.
func (s *Store) Log() []Commit { return nil }
