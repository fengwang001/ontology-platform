// Package uniquetable implements a single-key table with a unique
// constraint whose checks can be deferred to commit time.
//
// The table maps a non-empty row id to a key. A key is either NULL or a
// string. NULL keys are never equal to each other (any number of rows may
// hold NULL), the empty string is distinct from NULL, and non-NULL keys
// compare by byte equality.
//
// At most one transaction may be active at a time. All methods are safe
// for concurrent use; concurrent calls behave as if executed in some
// serial order.
package uniquetable

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Mode is the constraint checking mode inside a transaction.
type Mode int

const (
	// Immediate checks uniqueness as soon as the constraint requires it.
	Immediate Mode = iota
	// Deferred postpones uniqueness checks to SetMode(Immediate) or Commit.
	Deferred
)

func (m Mode) String() string {
	switch m {
	case Immediate:
		return "IMMEDIATE"
	case Deferred:
		return "DEFERRED"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// OpKind identifies the kind of a statement operation.
type OpKind int

const (
	OpInsert OpKind = iota
	OpUpdate
	OpDelete
)

// Op is a single operation inside a statement passed to Apply.
type Op struct {
	Kind OpKind
	Row  string
	Key  *string // nil means NULL; only used by OpInsert and OpUpdate
}

// Insert returns an Op inserting row with the given key (nil = NULL).
// The row id must not already exist.
func Insert(row string, key *string) Op { return Op{Kind: OpInsert, Row: row, Key: key} }

// Update returns an Op setting the key of an existing row.
func Update(row string, key *string) Op { return Op{Kind: OpUpdate, Row: row, Key: key} }

// Delete returns an Op removing an existing row.
func Delete(row string) Op { return Op{Kind: OpDelete, Row: row} }

// Str is a helper taking the address of a string literal.
func Str(s string) *string { return &s }

var (
	// ErrTxActive is returned by Begin when a transaction is in progress.
	ErrTxActive = errors.New("uniquetable: transaction already in progress")
	// ErrNoTx is returned by Apply, SetMode, Commit and Rollback when no
	// transaction is active.
	ErrNoTx = errors.New("uniquetable: no active transaction")
	// ErrInvalidMode is returned by SetMode for a mode that is neither
	// Immediate nor Deferred.
	ErrInvalidMode = errors.New("uniquetable: invalid mode")
	// ErrNotDeferrable is returned by SetMode when the constraint is not
	// deferrable.
	ErrNotDeferrable = errors.New("uniquetable: constraint is not deferrable")
	// ErrEmptyRow is returned by Apply for an operation with an empty row id.
	ErrEmptyRow = errors.New("uniquetable: row id is empty")
	// ErrRowExists is returned by Apply for an Insert of an existing row id.
	ErrRowExists = errors.New("uniquetable: row already exists")
	// ErrRowNotFound is returned by Apply for an Update or Delete of a
	// missing row id.
	ErrRowNotFound = errors.New("uniquetable: row not found")
	// ErrInvalidInitialMode is returned by New when initiallyDeferred is
	// true but the constraint is not deferrable.
	ErrInvalidInitialMode = errors.New("uniquetable: initially deferred requires a deferrable constraint")
)

// ViolationError reports a unique-constraint violation. When several
// checked rows violate, Key holds the smallest key in byte order.
type ViolationError struct {
	Key string
}

func (e *ViolationError) Error() string {
	return fmt.Sprintf("uniquetable: unique constraint violation on key %q", e.Key)
}

// Entry is one (row id, key) pair returned by Keys.
type Entry struct {
	Row string
	Key *string // nil means NULL
}

// Table is a row-id -> key map guarded by one unique constraint.
type Table struct {
	mu                sync.Mutex
	deferrable        bool
	initiallyDeferred bool

	committed map[string]*string

	txActive bool
	mode     Mode
	data     map[string]*string
	touched  map[string]bool // rows Inserted/Updated in the current tx
}

// New builds a table. initiallyDeferred requires deferrable.
func New(deferrable, initiallyDeferred bool) (*Table, error) {
	if initiallyDeferred && !deferrable {
		return nil, ErrInvalidInitialMode
	}
	return &Table{
		deferrable:        deferrable,
		initiallyDeferred: initiallyDeferred,
		committed:         make(map[string]*string),
	}, nil
}

// Begin starts a transaction. The mode is reset to the initial mode:
// Deferred when the constraint is deferrable and initially deferred,
// Immediate otherwise.
func (t *Table) Begin() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.txActive {
		return ErrTxActive
	}
	t.txActive = true
	t.mode = Immediate
	if t.deferrable && t.initiallyDeferred {
		t.mode = Deferred
	}
	t.data = cloneData(t.committed)
	t.touched = make(map[string]bool)
	return nil
}

// Apply executes ops as one statement, in order. On any failure the whole
// statement is undone and the transaction stays valid.
func (t *Table) Apply(ops []Op) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.txActive {
		return ErrNoTx
	}
	savedData := cloneData(t.data)
	savedTouched := cloneTouched(t.touched)
	stmtTouched := make([]string, 0, len(ops))
	fail := func(err error) error {
		t.data = savedData
		t.touched = savedTouched
		return err
	}
	for _, op := range ops {
		if op.Row == "" {
			return fail(ErrEmptyRow)
		}
		switch op.Kind {
		case OpInsert:
			if _, ok := t.data[op.Row]; ok {
				return fail(ErrRowExists)
			}
			t.data[op.Row] = op.Key
			t.touched[op.Row] = true
			stmtTouched = append(stmtTouched, op.Row)
		case OpUpdate:
			if _, ok := t.data[op.Row]; !ok {
				return fail(ErrRowNotFound)
			}
			t.data[op.Row] = op.Key
			t.touched[op.Row] = true
			stmtTouched = append(stmtTouched, op.Row)
		case OpDelete:
			if _, ok := t.data[op.Row]; !ok {
				return fail(ErrRowNotFound)
			}
			delete(t.data, op.Row)
		}
		if !t.deferrable && op.Kind != OpDelete {
			if verr := t.checkRows([]string{op.Row}); verr != nil {
				return fail(verr)
			}
		}
	}
	if t.deferrable && t.mode == Immediate {
		if verr := t.checkRows(stmtTouched); verr != nil {
			return fail(verr)
		}
	}
	return nil
}

// SetMode switches the checking mode of the current transaction. It is
// only available for deferrable constraints. Switching to Immediate
// checks the rows Inserted/Updated so far in this transaction (excluding
// since-deleted rows); on violation the switch is rejected and neither
// the mode nor the state changes.
func (t *Table) SetMode(mode Mode) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.txActive {
		return ErrNoTx
	}
	if mode != Immediate && mode != Deferred {
		return ErrInvalidMode
	}
	if !t.deferrable {
		return ErrNotDeferrable
	}
	if mode == Immediate {
		if verr := t.checkTouched(); verr != nil {
			return verr
		}
	}
	t.mode = mode
	return nil
}

// Commit ends the transaction. In Deferred mode it first checks the rows
// Inserted/Updated in this transaction (excluding since-deleted rows); on
// violation the whole transaction is rolled back, the committed state is
// unchanged, and a *ViolationError is returned. In Immediate mode it
// commits directly.
func (t *Table) Commit() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.txActive {
		return ErrNoTx
	}
	if t.mode == Deferred {
		if verr := t.checkTouched(); verr != nil {
			t.endTx()
			return verr
		}
	}
	t.committed = t.data
	t.endTx()
	return nil
}

// Rollback discards the current transaction.
func (t *Table) Rollback() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.txActive {
		return ErrNoTx
	}
	t.endTx()
	return nil
}

// Get returns the key of row and whether the row exists, reading the
// transactional view when a transaction is active and the committed state
// otherwise.
func (t *Table) Get(row string) (key *string, found bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	data := t.committed
	if t.txActive {
		data = t.data
	}
	k, ok := data[row]
	if !ok {
		return nil, false
	}
	return cloneKey(k), true
}

// Keys returns the (row id, key) pairs of the current view ordered by row
// id in byte order.
func (t *Table) Keys() []Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	data := t.committed
	if t.txActive {
		data = t.data
	}
	rows := make([]string, 0, len(data))
	for r := range data {
		rows = append(rows, r)
	}
	sort.Strings(rows)
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, Entry{Row: r, Key: cloneKey(data[r])})
	}
	return out
}

func (t *Table) endTx() {
	t.txActive = false
	t.data = nil
	t.touched = nil
}

// checkTouched checks the current keys of rows Inserted/Updated in this
// transaction that still exist.
func (t *Table) checkTouched() *ViolationError {
	rows := make([]string, 0, len(t.touched))
	for r := range t.touched {
		rows = append(rows, r)
	}
	sort.Strings(rows)
	return t.checkRows(rows)
}

// checkRows returns the violation with the smallest key among the given
// rows, or nil. Missing rows and NULL keys are skipped.
func (t *Table) checkRows(rows []string) *ViolationError {
	var min *string
	for _, r := range rows {
		k, ok := t.data[r]
		if !ok || k == nil {
			continue
		}
		if t.hasDuplicate(r, *k) && (min == nil || *k < *min) {
			key := *k
			min = &key
		}
	}
	if min == nil {
		return nil
	}
	return &ViolationError{Key: *min}
}

// hasDuplicate reports whether a row other than row holds key.
func (t *Table) hasDuplicate(row, key string) bool {
	for r, k := range t.data {
		if r != row && k != nil && *k == key {
			return true
		}
	}
	return false
}

func cloneData(src map[string]*string) map[string]*string {
	out := make(map[string]*string, len(src))
	for r, k := range src {
		out[r] = cloneKey(k)
	}
	return out
}

func cloneTouched(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src))
	for r := range src {
		out[r] = true
	}
	return out
}

func cloneKey(k *string) *string {
	if k == nil {
		return nil
	}
	c := *k
	return &c
}
