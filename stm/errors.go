package stm

import "errors"

// Misuse errors. When several misuses happen in one transaction, only the
// first one in the order below is reported; the transaction aborts and none
// of its writes take effect.
var (
	// ErrTxnClosed is reported when a transaction handle is used after its
	// transaction has ended.
	ErrTxnClosed = errors.New("stm: transaction handle used after transaction ended")
	// ErrNested is reported when a transaction is started inside a
	// transaction body.
	ErrNested = errors.New("stm: nested transaction")
	// ErrForeignTVar is reported when a TVar belonging to another STM
	// instance is accessed.
	ErrForeignTVar = errors.New("stm: TVar belongs to a different STM instance")
	// ErrTooManyVars is reported when one execution accesses more than
	// maxVars distinct variables.
	ErrTooManyVars = errors.New("stm: transaction accessed more than 64 distinct variables")
	// ErrBlockedForever is reported when Retry is invoked with an empty
	// read set, which could never be woken.
	ErrBlockedForever = errors.New("stm: retry with empty read set would block forever")
)

// maxVars bounds the number of distinct variables one execution may access.
const maxVars = 64

// errRetry is the internal sentinel signalling a retry. It is only obtainable
// through Txn.Retry and must be returned by the transaction body.
var errRetry = errors.New("stm: retry")

// reexecuteSignal is panicked internally when a read detects that the
// snapshot went stale; the execution is discarded and re-run from scratch.
type reexecuteSignal struct{}

// isFatal reports whether r is one of the exported misuse errors.
func isFatal(r any) bool {
	err, ok := r.(error)
	if !ok {
		return false
	}
	return errors.Is(err, ErrTxnClosed) ||
		errors.Is(err, ErrNested) ||
		errors.Is(err, ErrForeignTVar) ||
		errors.Is(err, ErrTooManyVars)
}
