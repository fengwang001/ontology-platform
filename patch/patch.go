// Package patch applies unified diffs in memory, with offset search and a
// versioned concurrent multi-document store.
package patch

import "ontology/udiff"

// Sentinel error categories.
var (
	ErrSyntax      = errCat("patch: malformed patch")
	ErrContext     = errCat("patch: context mismatch")
	ErrOutOfRange  = errCat("patch: hunk outside offset search range")
	ErrTooDifferent = errCat("patch: differences too large")
)

type errCat string

func (e errCat) Error() string { return string(e) }

// HunkError identifies the failing hunk (0-based) and reason category.
type HunkError struct {
	Hunk  int
	Cause error
}

func (e *HunkError) Error() string { return "" }
func (e *HunkError) Unwrap() error { return e.Cause }

// Apply applies p to text with +/-F lines of offset search.
func Apply(text string, p *udiff.Patch, fuzz int) (string, error) { return "", nil }

// Reverse returns the reverse patch (b -> a).
func Reverse(p *udiff.Patch) *udiff.Patch { return nil }

// Commit records one successful store application.
type Commit struct {
	Doc  string
	From int
	To   int
}

// Store is an in-memory versioned multi-document store.
type Store struct{}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{} }

// Put creates or replaces a document at version 0.
func (s *Store) Put(name, text string) {}

// Apply atomically applies p to the current snapshot; on failure nothing changes.
func (s *Store) Apply(name string, p *udiff.Patch, fuzz int) (int, error) { return 0, nil }

// Text returns the current text of a document.
func (s *Store) Text(name string) string { return "" }

// Version returns the current version of a document.
func (s *Store) Version(name string) int { return 0 }

// Log returns the ordered successful commits for a document.
func (s *Store) Log(name string) []Commit { return nil }
