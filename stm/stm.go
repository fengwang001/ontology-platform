// Package stm implements a software transactional memory with composable
// transactions over shared integer variables, supporting blocking retry
// and left-biased choice (orElse).
package stm

import "sync"

// STM is an independent transactional memory instance. TVars created by one
// instance must not be used in transactions of another instance.
type STM struct {
	mu   sync.Mutex
	cond *sync.Cond
}

// New returns an empty STM instance.
func New() *STM {
	s := &STM{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// TVar is a transactional integer variable. Its fields are guarded by the
// owning STM's mutex.
type TVar struct {
	owner   *STM
	value   int
	version uint64
}

// NewTVar creates a transactional variable holding val, owned by s.
func (s *STM) NewTVar(val int) *TVar {
	return &TVar{owner: s, value: val}
}

// Atomically runs body as one transaction.
//
// The body may be executed multiple times: if the snapshot it read is
// invalidated by another commit, or if it asks to retry, its reads and
// writes are discarded and it is re-run from scratch. When the body returns
// nil, all of its writes become visible to other transactions at once.
// A non-nil error returned by the body aborts the transaction (no writes
// take effect) and is returned unchanged; a panic aborts the transaction
// and is re-panicked unchanged.
func (s *STM) Atomically(body func(*Txn) error) error {
	enterTxn()
	defer leaveTxn()

	for {
		tx := &Txn{
			stm:      s,
			reads:    make(map[*TVar]uint64),
			writes:   make(map[*TVar]int),
			accessed: make(map[*TVar]struct{}),
		}
		bodyErr, reexecute := s.runBody(tx, body)
		tx.done = true

		if reexecute {
			continue
		}
		if bodyErr != nil && isFatal(bodyErr) {
			return bodyErr
		}
		if bodyErr == errRetry {
			if len(tx.reads) == 0 {
				return ErrBlockedForever
			}
			s.waitForChange(tx.reads)
			continue
		}
		if bodyErr != nil {
			return bodyErr
		}

		s.mu.Lock()
		if tx.validLocked() {
			for v, val := range tx.writes {
				v.value = val
				v.version++
			}
			if len(tx.writes) > 0 {
				s.cond.Broadcast()
			}
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
	}
}

// runBody executes the body, converting internal control-flow panics into
// results. Misuse errors are recovered and returned; any other panic is
// re-panicked unchanged.
func (s *STM) runBody(tx *Txn, body func(*Txn) error) (bodyErr error, reexecute bool) {
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case reexecuteSignal:
				reexecute = true
			default:
				if isFatal(r) {
					bodyErr = r.(error)
				} else {
					panic(r)
				}
			}
		}
	}()
	bodyErr = body(tx)
	return bodyErr, false
}

// waitForChange blocks until some variable in reads has been written by
// another commit since it was read (a write of an equal value counts). A
// write that lands between the read and the blocking also wakes it.
func (s *STM) waitForChange(reads map[*TVar]uint64) {
	s.mu.Lock()
	for !staleLocked(reads) {
		s.cond.Wait()
	}
	s.mu.Unlock()
}

// staleLocked reports whether any read variable has been written since the
// read. Callers must hold s.mu.
func staleLocked(reads map[*TVar]uint64) bool {
	for v, ver := range reads {
		if v.version != ver {
			return true
		}
	}
	return false
}
