// Package api is the public entry point to the grouped multi-column exact
// distinct counter. It depends only on dd and exposes no internal types.
package api

import "ontology/dd"

// Re-exported sentinel errors; reject a call with errors.Is(err, ...).
var (
	ErrRowIDNotPositive = dd.ErrRowIDNotPositive
	ErrEmptyKey         = dd.ErrEmptyKey
	ErrRowNotFound      = dd.ErrRowNotFound
)

// Counter maintains exact distinct (Col1, Col2) tuple counts per group key
// over the currently active rows. The zero value is not usable; use New.
type Counter struct {
	e *dd.Engine
}

// New returns an empty counter.
func New() *Counter { return &Counter{e: dd.New()} }

// Upsert inserts rowID, or moves an existing row to (key, col1, col2): the
// old tuple is retracted exactly once first. Invalid input fails whole.
func (c *Counter) Upsert(rowID int, key, col1 string, col2 int) error {
	return c.e.Upsert(rowID, key, col1, col2)
}

// Delete retracts the row's current tuple and removes the row.
func (c *Counter) Delete(rowID int) error { return c.e.Delete(rowID) }

// Distinct returns the number of distinct tuples among active rows in key.
func (c *Counter) Distinct(key string) int { return c.e.Distinct(key) }

// Total returns the sum of per-group distinct counts.
func (c *Counter) Total() int { return c.e.Total() }

// SelfCheck replays the built-in operation sequences and verifies the four
// invariants, rejection handling and the constant-work complexity bound.
func (c *Counter) SelfCheck() error { return c.e.SelfCheck() }
