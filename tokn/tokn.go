// Package tokn maintains per-domain value→token tables and hands out
// sequential 1-based numbers. Allocations are event-scoped: a Tx can be
// rolled back, undoing every allocation made through it.
package tokn

import (
	"errors"
	"fmt"
	"sync"
)

// ErrTokenLimit is returned when a new allocation would make the total
// number of token entries exceed the configured maximum.
var ErrTokenLimit = errors.New("tokn: token table limit exceeded")

// Tables holds the shared token tables of all domains.
type Tables struct {
	mu      sync.Mutex
	max     int
	dom     map[string]map[string]string // domain -> original value -> token
	total   int
	lookups int64 // entries inspected during the most recent transaction
}

// NewTables creates empty tables capped at max entries.
func NewTables(max int) *Tables {
	return &Tables{
		max: max,
		dom: map[string]map[string]string{},
	}
}

// Size returns the total number of token entries across all domains.
func (t *Tables) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

// Tx is a single event's allocation session.
type Tx struct {
	t     *Tables
	added []pair
}

type pair struct {
	domain string
	value  string
}

// WithTx runs fn while holding the tables lock. If fn returns an error, all
// allocations made inside the transaction are undone before WithTx returns.
func (t *Tables) WithTx(fn func(tx *Tx) error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lookups = 0
	tx := &Tx{t: t}
	if err := fn(tx); err != nil {
		tx.rollback()
		return err
	}
	return nil
}

// Get returns the token for value in domain, allocating a new sequential one
// on first sight. A map lookup is exactly one direct probe regardless of
// table size. NULL must never reach Get: the caller skips nil values.
func (tx *Tx) Get(domain, value string) (string, error) {
	m := tx.t.dom[domain]
	if m == nil {
		m = map[string]string{}
		tx.t.dom[domain] = m
	}
	tx.t.lookups++ // one hash probe, no linear scan
	if tok, ok := m[value]; ok {
		return tok, nil
	}
	if tx.t.total+1 > tx.t.max {
		return "", ErrTokenLimit
	}
	tok := fmt.Sprintf("%s#%d", domain, len(m)+1)
	m[value] = tok
	tx.t.total++
	tx.added = append(tx.added, pair{domain, value})
	return tok, nil
}

// rollback removes every entry allocated by this transaction, restoring the
// tables to their pre-event state. Numbers derive from len(domain table)+1,
// so deleting the newest entries restores a gap-free 1..k sequence.
func (tx *Tx) rollback() {
	for _, p := range tx.added {
		if m := tx.t.dom[p.domain]; m != nil {
			delete(m, p.value)
		}
	}
	tx.t.total -= len(tx.added)
	tx.added = nil
}
