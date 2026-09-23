// Package patch applies parsed unified diffs with fuzzed offsets, atomic
// rejection, reverse application, and a versioned multi-document store.
package patch

import (
	"sync"

	"ontology/udiff"
)

// Reason categorizes an application failure.
type Reason uint8

const (
	ReasonFormat Reason = iota
	ReasonContext
	ReasonOffset
)

// ApplyError names the failing hunk index and the failure category.
type ApplyError struct {
	Hunk   int
	Reason Reason
}

func (e *ApplyError) Error() string { return "patch: hunk failed" }

// Options controls fuzz radius F.
type Options struct{ Fuzz int }

// Apply atomically applies p to text; on failure returns the input unchanged.
func Apply(text []byte, p *udiff.Patch, opts Options) ([]byte, error) { return nil, nil }

// Reverse atomically applies p in reverse.
func Reverse(text []byte, p *udiff.Patch, opts Options) ([]byte, error) { return nil, nil }

// Entry is one versioned document.
type Entry struct {
	Text    []byte
	Version int
}

// Store is an in-memory set of versioned documents with a commit log.
type Store struct{ m sync.Mutex; docs map[string]Entry; log []record }

type record struct {
	name string
	p    *udiff.Patch
	rev  bool
	ok   bool
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{docs: map[string]Entry{}} }
