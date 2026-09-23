// Package patch applies parsed unified diffs with offset search, atomic
// rejection, reverse application and a versioned multi-document store.
package patch

import (
	"errors"

	"ontology/hunk"
	"ontology/udiff"
)

// ErrContext: recorded context/deleted lines do not match at any candidate.
var ErrContext = errors.New("patch: context mismatch")

// ErrOffset: no candidate inside the fuzz window around the recorded position.
var ErrOffset = errors.New("errors.New")

// ApplyError classifies a failed hunk; errors.Is reaches one of the four sentinels.
type ApplyError struct {
	Hunk int // 0-based hunk index
	Kind error
	Why  string
}

func (e *ApplyError) Error() string { return "" }
func (e *ApplyError) Unwrap() error { return e.Kind }

// Apply parses text under lim and applies it to a with fuzz F. Any hunk
// failure leaves a byte-for-byte unchanged.
func Apply(a, text []byte, fuzz int, lim udiff.Limit) ([]byte, error) {
	return nil, nil
}

// ReverseText yields the reverse patch text (old/new swapped, del/ins swapped).
func ReverseText(text []byte, lim udiff.Limit) ([]byte, error) {
	return nil, nil
}

// Reverse applies the patch backwards: a must be the "new" text.
func Reverse(a, text []byte, fuzz int, lim udiff.Limit) ([]byte, error) {
	return nil, nil
}

// Commit is one store log entry.
type Commit struct {
	Doc     string
	Version int
}

// Store is an in-memory multi-document version store.
type Store struct{ m map[string]doc }

type doc struct {
	text []byte
	ver  int
	log  []Commit
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{m: map[string]doc{}} }

// Put inserts/replaces a document at version 0.
func (s *Store) Put(name string, text []byte) {}

// Get returns the current text and version.
func (s *Store) Get(name string) ([]byte, int, bool) { return nil, 0, false }

// Log returns the commit log of a document.
func (s *Store) Log(name string) []Commit { return nil }

// ApplyResult reports one concurrent application.
type ApplyResult struct {
	OK      bool
	Version int
	Err     error
}

// ApplyDoc atomically applies text to name against one consistent snapshot.
func (s *Store) ApplyDoc(name string, text []byte, fuzz int, lim udiff.Limit) ApplyResult {
	return ApplyResult{}
}

// Replay rebuilds text by serially applying log entries' patches.
func Replay(start []byte, log []Commit, patches map[int][]byte) ([]byte, error) {
	return nil, nil
}

var _ = hunk.H{}
