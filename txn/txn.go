// Package txn holds the lifecycle state and per-transaction row buffer
// of a single CDC transaction. It depends on no other package.
package txn

import "errors"

// Kind is the kind of an upstream CDC event.
type Kind int

const (
	BEGIN Kind = iota + 1
	ROW
	COMMIT
	ROLLBACK
)

// Event is one upstream CDC record.
type Event struct {
	Kind Kind
	Tx   int64
	Data string
}

// Txn is one atomically delivered, committed transaction.
type Txn struct {
	Tx   int64
	Rows []string
}

// State is the lifecycle state of a single transaction.
type State int

const (
	Unbegun State = iota
	Active
	Committed
	Aborted
)

// ErrDuplicateBegin is returned when a tx that already BEGINed BEGINs again.
var ErrDuplicateBegin = errors.New("duplicate BEGIN for transaction")

// Transaction is one transaction's lifecycle and buffered rows.
type Transaction struct {
	state State
	rows  []string
}

// New returns an unbegun transaction.
func New() *Transaction { return &Transaction{} }

// State reports the current lifecycle state.
func (t *Transaction) State() State { return t.state }

// Begin moves an unbegun transaction to active. A tx that has already
// begun (active, committed or aborted) yields ErrDuplicateBegin and
// leaves the state untouched.
func (t *Transaction) Begin() error {
	if t.state != Unbegun {
		return ErrDuplicateBegin
	}
	t.state = Active
	return nil
}

// Add buffers one row on an active transaction, in arrival order.
// The caller must have verified the transaction is active.
func (t *Transaction) Add(data string) { t.rows = append(t.rows, data) }

// Active reports whether the transaction is in progress.
func (t *Transaction) Active() bool { return t.state == Active }

// Commit finalizes an active transaction and returns all its rows in
// arrival order. The buffer is released; a committed or aborted
// transaction can never become active again.
func (t *Transaction) Commit() []string {
	rows := t.rows
	t.rows = nil
	t.state = Committed
	return rows
}

// Abort discards all buffered rows and finalizes the transaction
// without emitting anything.
func (t *Transaction) Abort() {
	t.rows = nil
	t.state = Aborted
}

// Len reports the number of currently buffered rows.
func (t *Transaction) Len() int { return len(t.rows) }
