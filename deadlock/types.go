// Package deadlock implements a cross-site distributed deadlock detector.
//
// Locks live on sites and come in shared/exclusive modes. Each site only
// knows its local lock table and local wait-for edges. When a transaction
// starts waiting it launches edge-chasing probes that are forwarded along
// wait-for edges through a simulated network with injected, out-of-order
// delays. A probe that returns to its initiator confirms a deadlock only
// after re-validating that every traversed edge still exists.
package deadlock

import "errors"

// Mode is a lock mode.
type Mode int

const (
	Shared Mode = iota
	Exclusive
)

func (m Mode) String() string {
	if m == Shared {
		return "S"
	}
	return "X"
}

// TxnState is the lifecycle state of a transaction.
type TxnState int

const (
	StateActive TxnState = iota
	StateWaiting
	StateCommitted
	StateAborted
)

func (s TxnState) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateWaiting:
		return "waiting"
	case StateCommitted:
		return "committed"
	case StateAborted:
		return "aborted"
	}
	return "unknown"
}

// Distinguishable rejection reasons for invalid operations. A rejected
// operation never mutates the lock table or the wait-for graph.
var (
	ErrUnknownSite      = errors.New("unknown site")
	ErrUnknownLock      = errors.New("unknown lock")
	ErrUnknownTxn       = errors.New("unknown transaction")
	ErrDuplicateTxn     = errors.New("duplicate transaction id")
	ErrDuplicateStartTS = errors.New("duplicate start timestamp")
	ErrTxnFinished      = errors.New("transaction already finished")
	ErrTxnWaiting       = errors.New("transaction is waiting")
	ErrLockNotHeld      = errors.New("lock not held by transaction")
)

// compatible reports whether two lock modes may be held concurrently.
func compatible(a, b Mode) bool {
	return a == Shared && b == Shared
}
