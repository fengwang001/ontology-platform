package merge

import (
	"fmt"
	"sync"
)

// Table is a concurrency-safe row store keyed by primary key.
//
// Concurrency model: row queries (Get, Snapshot, Len) and SelfCheck take a
// read lock, so many executors may run them concurrently, and they may run
// concurrently with a single in-flight Commit/Merge/Apply (which take the
// write lock). A rejected batch never mutates the table.
type Table struct {
	mu     sync.RWMutex
	schema ColumnSet
	rows   map[string]Row
	log    Logger
}

// NewTable creates an empty table with the given schema columns. A nil logger
// disables logging.
func NewTable(schema []string, log Logger) *Table {
	if log == nil {
		log = noopLogger{}
	}
	return &Table{
		schema: NewColumnSet(schema...),
		rows:   make(map[string]Row),
		log:    log,
	}
}

// Schema returns the table's column set.
func (t *Table) Schema() ColumnSet {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(ColumnSet, len(t.schema))
	for c := range t.schema {
		out[c] = struct{}{}
	}
	return out
}

// Merge validates and merges a batch against the current table WITHOUT
// mutating it. It returns the merged result, or a *BatchError if the batch is
// rejected. Use Apply or Commit to persist.
func (t *Table) Merge(events []Event) (*Result, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	res, berr := mergeBatch(t.schema, t.rows, events, t.log)
	if berr != nil {
		return nil, berr
	}
	return res, nil
}

// Commit validates, merges and applies a batch atomically. On any violation
// the whole batch is rejected and the table is left unchanged.
func (t *Table) Commit(events []Event) (*Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	res, berr := mergeBatch(t.schema, t.rows, events, t.log)
	if berr != nil {
		t.log.Logf("commit: rejected: %v", berr)
		return nil, berr
	}
	applyResult(t.rows, res)
	t.log.Logf("commit: applied %d change(s) keys=%v", len(res.Changes), res.Keys())
	return res, nil
}

// Apply persists a Result previously produced by Merge, using optimistic
// concurrency: it re-checks that inserts' keys are still absent and updates'
// before-images still match. If the table changed since the merge, it returns
// a *BatchError with Kind ErrConflict and applies nothing.
func (t *Table) Apply(res *Result) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, ch := range res.Changes {
		switch ch.Kind {
		case EventInsert:
			if _, exists := t.rows[ch.Key]; exists {
				return &BatchError{Kind: ErrConflict, Key: ch.Key, EventIx: -1,
					Detail: "insert key now exists; table changed since merge"}
			}
		case EventUpdate:
			cur, exists := t.rows[ch.Key]
			if !exists {
				return &BatchError{Kind: ErrConflict, Key: ch.Key, EventIx: -1,
					Detail: "update key no longer exists; table changed since merge"}
			}
			for _, col := range ColumnsOf(ch.Before).Sorted() {
				if !cur[col].Equal(ch.Before[col]) {
					return &BatchError{Kind: ErrConflict, Key: ch.Key, Column: col, EventIx: -1,
						Detail: fmt.Sprintf("before-image %s no longer matches actual %s", ch.Before[col], cur[col])}
				}
			}
		}
	}
	applyResult(t.rows, res)
	t.log.Logf("apply: applied %d change(s) keys=%v", len(res.Changes), res.Keys())
	return nil
}

// Get returns a copy of the row for key, or false if absent.
func (t *Table) Get(key string) (Row, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.rows[key]
	if !ok {
		return nil, false
	}
	return r.Clone(), true
}

// Snapshot returns a deep copy of all rows.
func (t *Table) Snapshot() map[string]Row {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Row, len(t.rows))
	for k, v := range t.rows {
		out[k] = v.Clone()
	}
	return out
}

// Len returns the number of rows.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.rows)
}

// SelfCheck validates table invariants: every row has exactly the schema
// columns and every column value is present (null or string, never absent).
func (t *Table) SelfCheck() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for key, row := range t.rows {
		cols := ColumnsOf(row)
		if !cols.EqualSet(t.schema) {
			return fmt.Errorf("self-check: row %q columns %v != schema %v", key, cols.Sorted(), t.schema.Sorted())
		}
		for _, c := range t.schema.Sorted() {
			if row[c].IsAbsent() {
				return fmt.Errorf("self-check: row %q column %q is absent", key, c)
			}
		}
	}
	return nil
}
