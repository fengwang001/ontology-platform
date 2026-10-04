// Package search provides key-ordered pagination over a pit.Manager,
// for both the current view and open point-in-time views.
package search

import (
	"ontology/pit"
	"ontology/segstore"
)

// Doc is an input document.
type Doc = segstore.Doc

// Key is the sort triple (sortVal, segment number, in-segment ordinal).
type Key = segstore.Key

// Entry is a returned document with its key.
type Entry = segstore.Entry

// State errors, re-exported so callers can distinguish rejections via errors.Is.
var (
	ErrInvalid       = pit.ErrInvalid
	ErrClockRollback = pit.ErrClockRollback
	ErrPITNotFound   = pit.ErrPITNotFound
	ErrPITLimit      = pit.ErrPITLimit
	ErrIDConflict    = pit.ErrIDConflict
	ErrDocNotFound   = pit.ErrDocNotFound
	ErrSegNotFound   = pit.ErrSegNotFound
)

// Engine wraps a pit.Manager.
type Engine struct {
	m *pit.Manager
}

// NewEngine creates an Engine with PIT capacity pmax.
func NewEngine(pmax int) *Engine { return &Engine{m: pit.NewManager(pmax)} }

// Manager exposes the underlying manager (used by tests and simulations).
func (e *Engine) Manager() *pit.Manager { return e.m }

// AddSegment adds a segment.
func (e *Engine) AddSegment(now int64, docs []Doc) (int, error) {
	return e.m.AddSegment(now, docs)
}

// Delete tombstones a document.
func (e *Engine) Delete(now int64, id string) error { return e.m.Delete(now, id) }

// Merge merges segments.
func (e *Engine) Merge(now int64, segs []int) (int, error) { return e.m.Merge(now, segs) }

// Open opens a PIT with keep-alive ka milliseconds.
func (e *Engine) Open(now int64, ka int64) (pit.PIT, error) { return e.m.Open(now, ka) }

// Close lands a PIT immediately.
func (e *Engine) Close(now int64, pid int) error { return e.m.Close(now, pid) }

// Search returns up to size entries strictly after the given key.
// pid == 0 searches the current view (after must be nil, ka must be 0).
func (e *Engine) Search(now int64, pid, size int, after *Key, ka int64) ([]Entry, error) {
	return e.m.Search(now, pid, size, after, ka)
}

// Released returns the physical release log.
func (e *Engine) Released() []int { return e.m.Released() }
