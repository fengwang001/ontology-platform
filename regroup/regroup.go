// Package regroup manages all transactions by tx id, the global buffered-row
// ledger, per-COMMIT atomic output, ROLLBACK discarding and the output log.
// It depends only on package txn.
package regroup

import (
	"errors"
	"sync"

	"ontology/txn"
)

var (
	// ErrUnknownTxn is returned when an event targets a tx that is not
	// in progress (never begun, already committed or rolled back).
	ErrUnknownTxn = errors.New("event for unknown transaction")
	// ErrBufferFull is returned when a ROW would exceed maxRows.
	ErrBufferFull = errors.New("buffered row limit exceeded")
)

// Regrouper feeds interleaved events and emits whole committed transactions.
type Regrouper struct {
	mu          sync.Mutex
	txns        map[int64]*txn.Transaction
	output      []txn.Txn
	buffered    int
	lastChecked int // rows inspected by the most recent COMMIT/ROLLBACK
	maxRows     int
}

// New creates a Regrouper bounded by maxRows total buffered rows.
func New(maxRows int) *Regrouper {
	return &Regrouper{txns: map[int64]*txn.Transaction{}, maxRows: maxRows}
}

// Begin starts a transaction. A tx that has ever BEGINed before — still
// active, committed or rolled back — yields txn.ErrDuplicateBegin.
func (r *Regrouper) Begin(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.txns[id]; ok {
		return t.Begin() // active/ended -> ErrDuplicateBegin
	}
	t := txn.New()
	if err := t.Begin(); err != nil {
		return err
	}
	r.txns[id] = t
	return nil
}

// Row buffers one row for an active transaction. It returns
// ErrUnknownTxn when the tx is not in progress and ErrBufferFull when
// accepting the row would make the ledger exceed maxRows. On rejection
// nothing changes: the tx stays active with its prior rows.
func (r *Regrouper) Row(id int64, data string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.txns[id]
	if !ok || !t.Active() {
		return ErrUnknownTxn
	}
	if r.buffered+1 > r.maxRows {
		return ErrBufferFull // checked before any mutation
	}
	t.Add(data)
	r.buffered++
	return nil
}

// Commit emits the transaction atomically: it returns a Txn holding all
// of that tx's accepted rows in arrival order (possibly zero) and
// releases the rows from the ledger immediately.
func (r *Regrouper) Commit(id int64) (txn.Txn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.txns[id]
	if !ok || !t.Active() {
		return txn.Txn{}, ErrUnknownTxn
	}
	r.lastChecked = t.Len() // only this tx's buffer is inspected
	rows := t.Commit()
	r.buffered -= len(rows)
	r.output = append(r.output, txn.Txn{Tx: id, Rows: rows})
	// Hand back an independent copy so callers cannot mutate the log.
	return txn.Txn{Tx: id, Rows: append([]string(nil), rows...)}, nil
}

// Rollback discards an active transaction's rows, releases them from
// the ledger and emits nothing.
func (r *Regrouper) Rollback(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.txns[id]
	if !ok || !t.Active() {
		return ErrUnknownTxn
	}
	r.lastChecked = t.Len()
	n := t.Len()
	t.Abort()
	r.buffered -= n
	return nil
}

// Output returns all transactions emitted so far, in COMMIT arrival
// order. The returned slice (and its row slices) is a copy.
func (r *Regrouper) Output() []txn.Txn {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]txn.Txn, len(r.output))
	for i, t := range r.output {
		out[i] = txn.Txn{Tx: t.Tx, Rows: append([]string(nil), t.Rows...)}
	}
	return out
}

// Buffered returns the current total number of buffered rows across all
// in-progress transactions.
func (r *Regrouper) Buffered() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buffered
}
