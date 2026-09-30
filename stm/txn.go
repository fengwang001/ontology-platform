package stm

// Txn is a handle to a single execution of a transaction body. It becomes
// invalid once the body returns; using it afterwards panics with
// ErrTxnClosed.
type Txn struct {
	stm      *STM
	reads    map[*TVar]uint64 // variable -> version observed at read time
	writes   map[*TVar]int    // buffered writes, invisible to others
	accessed map[*TVar]struct{}
	done     bool
}

// Get reads v. A variable written earlier by this same execution returns
// the written value (read-your-writes). Every value returned by Get is
// validated against the rest of the read set, so all values observed during
// one execution come from a single consistent moment.
func (tx *Txn) Get(v *TVar) int {
	tx.checkUsable(v)
	tx.access(v)

	s := tx.stm
	s.mu.Lock()
	defer s.mu.Unlock()
	if staleLocked(tx.reads) {
		panic(reexecuteSignal{})
	}
	if val, ok := tx.writes[v]; ok {
		return val
	}
	tx.reads[v] = v.version
	return v.value
}

// Set buffers a write of val to v. The write is visible only to this
// execution until the transaction commits.
func (tx *Txn) Set(v *TVar, val int) {
	tx.checkUsable(v)
	tx.access(v)
	tx.writes[v] = val
}

// Retry asks the transaction to discard all writes of this execution and
// block until some variable read by this execution is written by another
// transaction's commit (writing an equal value counts), then re-run from
// scratch. The returned error must be returned by the transaction body
// (directly or through OrElse) for the retry to take effect.
func (tx *Txn) Retry() error {
	tx.checkOpen()
	return errRetry
}

// OrElse runs left first. If left returns nil, its writes are kept and
// right is not run. If left retries, its writes are discarded but the
// variables it read stay in the read set, and right runs without seeing
// left's writes. If both retry, the whole transaction retries with the
// union of both read sets. If left returns any other error or panics, the
// transaction aborts and right is never run.
func (tx *Txn) OrElse(left, right func(*Txn) error) error {
	tx.checkOpen()

	outer := tx.writes
	tx.writes = cloneWrites(outer)
	err := left(tx)
	if err == nil {
		return nil
	}
	if err != errRetry {
		tx.writes = outer
		return err
	}

	tx.writes = cloneWrites(outer)
	err = right(tx)
	if err == nil {
		return nil
	}
	tx.writes = outer
	return err
}

// checkUsable enforces the misuse rules in their reporting order:
// closed handle first, then foreign TVar.
func (tx *Txn) checkUsable(v *TVar) {
	tx.checkOpen()
	if v.owner != tx.stm {
		panic(ErrForeignTVar)
	}
}

// checkOpen panics with ErrTxnClosed if the transaction has ended.
func (tx *Txn) checkOpen() {
	if tx.done {
		panic(ErrTxnClosed)
	}
}

// access records v in the accessed set, panicking with ErrTooManyVars when
// the execution touches more than maxVars distinct variables.
func (tx *Txn) access(v *TVar) {
	if _, ok := tx.accessed[v]; ok {
		return
	}
	if len(tx.accessed) >= maxVars {
		panic(ErrTooManyVars)
	}
	tx.accessed[v] = struct{}{}
}

// validLocked reports whether every read variable still holds the version
// observed at read time. Callers must hold the STM mutex.
func (tx *Txn) validLocked() bool {
	return !staleLocked(tx.reads)
}

func cloneWrites(w map[*TVar]int) map[*TVar]int {
	c := make(map[*TVar]int, len(w))
	for v, val := range w {
		c[v] = val
	}
	return c
}
