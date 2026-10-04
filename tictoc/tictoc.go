// Package tictoc implements a TicToc-style optimistic transaction
// validator with data-driven timestamps: the commit timestamp is
// derived from the read/write sets only at Prepare time, read
// versions are extended on demand, and writes are installed at Finish.
package tictoc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Rejection and abort reasons. Every failed call wraps exactly one of
// these sentinels, so callers can distinguish causes with errors.Is.
var (
	// ErrInvalidK rejects New when K is outside [1, 64].
	ErrInvalidK = errors.New("tictoc: tuple count K out of range [1,64]")
	// ErrTxnNotFound rejects calls naming a transaction id that was
	// never returned by Begin.
	ErrTxnNotFound = errors.New("tictoc: transaction does not exist")
	// ErrInvalidState rejects calls whose transaction is not in the
	// required state (Read/Write/Prepare need Active, Finish needs
	// Prepared, Abort needs Active or Prepared).
	ErrInvalidState = errors.New("tictoc: transaction state does not allow this call")
	// ErrTupleOutOfRange rejects Read/Write with k outside [0, K).
	ErrTupleOutOfRange = errors.New("tictoc: tuple index out of range")
	// ErrAbortLockConflict: Prepare step 1 found a write-set tuple
	// locked by another transaction.
	ErrAbortLockConflict = errors.New("tictoc: prepare aborted: write-set lock held by another transaction")
	// ErrAbortVersionChanged: Prepare step 3 found a read-set tuple
	// whose current w differs from the w observed at read time.
	ErrAbortVersionChanged = errors.New("tictoc: prepare aborted: read version changed")
	// ErrAbortExtendBlocked: Prepare step 3 needed to extend a read
	// version but the tuple is locked by another transaction.
	ErrAbortExtendBlocked = errors.New("tictoc: prepare aborted: read-version extension blocked by another lock")
)

// State is the lifecycle state of a transaction.
type State int

const (
	Active State = iota
	Prepared
	Aborted
	Committed
)

func (s State) String() string {
	switch s {
	case Active:
		return "active"
	case Prepared:
		return "prepared"
	case Aborted:
		return "aborted"
	case Committed:
		return "committed"
	}
	return "unknown"
}

// Tuple is one versioned record. The invariant W <= R always holds.
// Lock is the id of the prepared transaction holding the write lock,
// or 0 when unlocked.
type Tuple struct {
	Value int64
	W     int64
	R     int64
	Lock  int
}

// readRecord is a read-set entry: the value returned at first read
// plus the tuple's (w, r) observed at that moment.
type readRecord struct {
	value int64
	w0    int64
	r0    int64
}

type transaction struct {
	state    State
	reads    map[int]readRecord
	writes   map[int]int64
	commitTS int64
}

// Validator is a TicToc-style optimistic transaction validator over
// K tuples. All methods are safe for concurrent use; the result is
// equivalent to some serial execution of the calls.
type Validator struct {
	mu     sync.Mutex
	tuples []Tuple
	txns   map[int]*transaction
	nextID int

	// prepareTouches counts tuple accesses of the most recent Prepare
	// (unexported, used by tests to check the 2*(|R|+|W|) bound).
	prepareTouches int
}

// New creates a validator with K tuples, all (value 0, w 0, r 0,
// unlocked). K outside [1, 64] is rejected with ErrInvalidK.
func New(k int) (*Validator, error) {
	if k < 1 || k > 64 {
		return nil, fmt.Errorf("%w: got K=%d", ErrInvalidK, k)
	}
	return &Validator{
		tuples: make([]Tuple, k),
		txns:   make(map[int]*transaction),
		nextID: 1,
	}, nil
}

// K returns the tuple count.
func (v *Validator) K() int { return len(v.tuples) }

// Begin starts a transaction and returns its id. Ids are strictly
// increasing from 1; the new transaction is Active.
func (v *Validator) Begin() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	id := v.nextID
	v.nextID++
	v.txns[id] = &transaction{
		state:  Active,
		reads:  make(map[int]readRecord),
		writes: make(map[int]int64),
	}
	return id
}

// findTxn resolves the id; the "transaction does not exist" reason is
// always reported before any state or tuple-index reason.
func (v *Validator) findTxn(t int) (*transaction, error) {
	tx, ok := v.txns[t]
	if !ok {
		return nil, fmt.Errorf("%w: txn %d", ErrTxnNotFound, t)
	}
	return tx, nil
}

// Read returns t's buffered value if t wrote k, else the value of the
// first read of k if t already read k (the read-set record is not
// updated again), else the tuple's current value, recording
// (k, w, r) in the read set. A tuple locked by someone else is read
// anyway.
func (v *Validator) Read(t, k int) (int64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	tx, err := v.findTxn(t)
	if err != nil {
		return 0, err
	}
	if tx.state != Active {
		return 0, fmt.Errorf("%w: Read on txn %d in state %s", ErrInvalidState, t, tx.state)
	}
	if k < 0 || k >= len(v.tuples) {
		return 0, fmt.Errorf("%w: tuple %d (K=%d)", ErrTupleOutOfRange, k, len(v.tuples))
	}
	if x, ok := tx.writes[k]; ok {
		return x, nil
	}
	if rec, ok := tx.reads[k]; ok {
		return rec.value, nil
	}
	tp := v.tuples[k]
	tx.reads[k] = readRecord{value: tp.Value, w0: tp.W, r0: tp.R}
	return tp.Value, nil
}

// Write buffers x for tuple k; the last write to the same tuple wins.
// Writing does not require a prior read.
func (v *Validator) Write(t, k int, x int64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	tx, err := v.findTxn(t)
	if err != nil {
		return err
	}
	if tx.state != Active {
		return fmt.Errorf("%w: Write on txn %d in state %s", ErrInvalidState, t, tx.state)
	}
	if k < 0 || k >= len(v.tuples) {
		return fmt.Errorf("%w: tuple %d (K=%d)", ErrTupleOutOfRange, k, len(v.tuples))
	}
	tx.writes[k] = x
	return nil
}

// Prepare runs the four commit-validation steps:
//
//  1. lock the write set in ascending tuple order; a lock held by
//     someone else aborts with ErrAbortLockConflict;
//  2. derive the commit timestamp c = max(r+1 over the write set,
//     w0 over the read set), or 0 when both sets are empty;
//  3. validate each read-set record (k, w0, r0) in ascending tuple
//     order: r0 >= c passes without touching the tuple; otherwise a
//     changed w aborts with ErrAbortVersionChanged; a current r >= c
//     passes; a foreign lock aborts with ErrAbortExtendBlocked; t's
//     own lock passes unchanged; an unlocked tuple gets r extended
//     to c;
//  4. on success t becomes Prepared, keeps its write locks until
//     Finish or Abort, and c is returned.
//
// Any abort rolls back the locks and extensions of this call so the
// tuples are field-identical to before the call, and t becomes
// Aborted.
func (v *Validator) Prepare(t int) (int64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.prepareTouches = 0
	tx, err := v.findTxn(t)
	if err != nil {
		return 0, err
	}
	if tx.state != Active {
		return 0, fmt.Errorf("%w: Prepare on txn %d in state %s", ErrInvalidState, t, tx.state)
	}

	writeKeys := sortedKeys(tx.writes)
	readKeys := sortedKeys(tx.reads)

	// Step 1: lock the write set in ascending tuple order.
	locked := make([]int, 0, len(writeKeys))
	for _, k := range writeKeys {
		v.prepareTouches++
		holder := v.tuples[k].Lock
		if holder != 0 && holder != t {
			for _, j := range locked {
				v.tuples[j].Lock = 0
			}
			tx.state = Aborted
			return 0, fmt.Errorf("%w: tuple %d held by txn %d", ErrAbortLockConflict, k, holder)
		}
		v.tuples[k].Lock = t
		locked = append(locked, k)
	}

	// Step 2: derive the commit timestamp from both sets.
	var c int64
	if len(writeKeys) > 0 || len(readKeys) > 0 {
		first := true
		for _, k := range writeKeys {
			if cand := v.tuples[k].R + 1; first || cand > c {
				c, first = cand, false
			}
		}
		for _, k := range readKeys {
			if cand := tx.reads[k].w0; first || cand > c {
				c, first = cand, false
			}
		}
	}

	// rollback undoes this call's locks and read-version extensions
	// so every tuple is field-identical to before the call.
	type extension struct {
		k    int
		oldR int64
	}
	extended := make([]extension, 0, len(readKeys))
	rollback := func() {
		for i := len(extended) - 1; i >= 0; i-- {
			v.tuples[extended[i].k].R = extended[i].oldR
		}
		for _, j := range locked {
			v.tuples[j].Lock = 0
		}
		tx.state = Aborted
	}

	// Step 3: validate the read set in ascending tuple order.
	for _, k := range readKeys {
		rec := tx.reads[k]
		if rec.r0 >= c {
			continue // still valid; the tuple is not even touched.
		}
		v.prepareTouches++
		tp := &v.tuples[k]
		if tp.W != rec.w0 {
			rollback()
			return 0, fmt.Errorf("%w: tuple %d has w=%d, read w=%d", ErrAbortVersionChanged, k, tp.W, rec.w0)
		}
		if tp.R >= c {
			continue
		}
		switch holder := tp.Lock; {
		case holder != 0 && holder != t:
			rollback()
			return 0, fmt.Errorf("%w: tuple %d held by txn %d", ErrAbortExtendBlocked, k, holder)
		case holder == t:
			// Own write-set lock: passes without extending r.
		default:
			extended = append(extended, extension{k: k, oldR: tp.R})
			tp.R = c
		}
	}

	// Step 4: success.
	tx.state = Prepared
	tx.commitTS = c
	return c, nil
}

// Finish installs the write set: each written tuple gets the buffered
// value and w = r = c, and is unlocked. t becomes Committed and c is
// returned.
func (v *Validator) Finish(t int) (int64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	tx, err := v.findTxn(t)
	if err != nil {
		return 0, err
	}
	if tx.state != Prepared {
		return 0, fmt.Errorf("%w: Finish on txn %d in state %s", ErrInvalidState, t, tx.state)
	}
	for _, k := range sortedKeys(tx.writes) {
		tp := &v.tuples[k]
		tp.Value = tx.writes[k]
		tp.W = tx.commitTS
		tp.R = tx.commitTS
		tp.Lock = 0
	}
	tx.state = Committed
	return tx.commitTS, nil
}

// Abort releases the locks of an Active or Prepared transaction and
// moves it to Aborted.
func (v *Validator) Abort(t int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	tx, err := v.findTxn(t)
	if err != nil {
		return err
	}
	if tx.state != Active && tx.state != Prepared {
		return fmt.Errorf("%w: Abort on txn %d in state %s", ErrInvalidState, t, tx.state)
	}
	for k := range tx.writes {
		if v.tuples[k].Lock == t {
			v.tuples[k].Lock = 0
		}
	}
	tx.state = Aborted
	return nil
}

// Snapshot returns a copy of all tuples and transaction states.
func (v *Validator) Snapshot() ([]Tuple, map[int]State) {
	v.mu.Lock()
	defer v.mu.Unlock()
	tuples := make([]Tuple, len(v.tuples))
	copy(tuples, v.tuples)
	states := make(map[int]State, len(v.txns))
	for id, tx := range v.txns {
		states[id] = tx.state
	}
	return tuples, states
}

func sortedKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
