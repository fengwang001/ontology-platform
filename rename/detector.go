// Package rename detects file renames between a set of deleted files and a
// set of added files by matching them on exact content first and then on
// line-based content similarity.
package rename

import (
	"errors"
	"sync"
)

// Rename is one detected rename from a deleted path to an added path.
type Rename struct {
	From  string
	To    string
	Score int
}

// Result is the output of Detect.
type Result struct {
	Renames         []Rename
	UnpairedDeleted []string
	UnpairedAdded   []string
}

var (
	ErrInvalidThreshold = errors.New("rename: threshold must be between 1 and 100")
	ErrInvalidCapacity  = errors.New("rename: capacity must be at least 1")
	ErrFrozen           = errors.New("rename: session is frozen")
	ErrEmptyPath        = errors.New("rename: path must not be empty")
	ErrPathExists       = errors.New("rename: path already registered")
	ErrCapacityReached  = errors.New("rename: registration capacity reached")
)

// Detector collects deleted and added files and detects renames.
type Detector struct {
	threshold int
	capacity  int

	mu      sync.Mutex
	frozen  bool
	deleted map[string]string
	added   map[string]string

	// detectRuns counts how many times the pairing algorithm actually ran.
	// Detect is memoized: repeated or concurrent calls run it at most once.
	detectRuns int
	// commonComputed counts how many times the common-bytes value C of a
	// (deleted, added) pair was actually computed. Pairs pruned by the
	// size-ratio bound never increment it.
	commonComputed int

	result *Result
}

// New creates a Detector with similarity threshold T (1..100) and a maximum
// total number of registered files cap (>=1).
func New(threshold, capacity int) (*Detector, error) {
	if threshold < 1 || threshold > 100 {
		return nil, ErrInvalidThreshold
	}
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Detector{
		threshold: threshold,
		capacity:  capacity,
		deleted:   make(map[string]string),
		added:     make(map[string]string),
	}, nil
}

// RegisterDeleted records a deleted file.
func (d *Detector) RegisterDeleted(path string, content []byte) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.register(path, content, d.deleted)
}

// RegisterAdded records an added file.
func (d *Detector) RegisterAdded(path string, content []byte) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.register(path, content, d.added)
}

func (d *Detector) register(path string, content []byte, side map[string]string) error {
	if d.frozen {
		return ErrFrozen
	}
	if path == "" {
		return ErrEmptyPath
	}
	if _, ok := d.deleted[path]; ok {
		return ErrPathExists
	}
	if _, ok := d.added[path]; ok {
		return ErrPathExists
	}
	if len(d.deleted)+len(d.added) >= d.capacity {
		return ErrCapacityReached
	}
	side[path] = string(content)
	return nil
}

// Detect runs detection once, freezes the session and returns the result.
func (d *Detector) Detect() *Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.result != nil {
		return d.result
	}
	d.frozen = true
	d.detectRuns++
	d.result = d.compute()
	return d.result
}

func (d *Detector) detectRunCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.detectRuns
}

func (d *Detector) commonComputedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.commonComputed
}
