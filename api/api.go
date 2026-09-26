// Package api is the public facade over the cuckoo hash table.
package api

import "ontology/cuckoo"

// Table is the exported cuckoo hash handle.
type Table struct {
	inner *cuckoo.Table
}

// New creates a table with n slots per table and an eviction bound maxKicks.
func New(n, maxKicks int) (*Table, error) {
	t, err := cuckoo.New(n, maxKicks)
	if err != nil {
		return nil, err
	}
	return &Table{inner: t}, nil
}

// Insert adds x; it rejects duplicates and over-full tables.
func (t *Table) Insert(x int) error { return t.inner.Insert(x) }

// Lookup reports whether x is present (false with ErrNotFound when absent).
func (t *Table) Lookup(x int) (bool, error) { return t.inner.Lookup(x) }

// Delete removes x; it errors when x is absent.
func (t *Table) Delete(x int) error { return t.inner.Delete(x) }

// Len returns the number of stored keys.
func (t *Table) Len() int { return t.inner.Len() }

// SelfCheck verifies the built-in invariants without mutating the table.
func (t *Table) SelfCheck() error { return t.inner.SelfCheck() }
