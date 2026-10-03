package tictoc

import (
	"errors"
	"sort"
	"sync"
)

type TxnStatus int

const (
	StatusActive TxnStatus = iota
	StatusPrepared
	StatusCommitted
	StatusAborted
)

type ErrorCode int

const (
	ErrInvalidConfig ErrorCode = iota
	ErrNoSuchTxn
	ErrWrongState
	ErrKeyOutOfRange
	ErrLockConflict
	ErrVersionChanged
	ErrExtensionBlocked
)

var (
	ErrInvalidConfigErr    = &TicTocError{Code: ErrInvalidConfig}
	ErrNoSuchTxnErr        = &TicTocError{Code: ErrNoSuchTxn}
	ErrWrongStateErr       = &TicTocError{Code: ErrWrongState}
	ErrKeyOutOfRangeErr    = &TicTocError{Code: ErrKeyOutOfRange}
	ErrLockConflictErr     = &TicTocError{Code: ErrLockConflict}
	ErrVersionChangedErr   = &TicTocError{Code: ErrVersionChanged}
	ErrExtensionBlockedErr = &TicTocError{Code: ErrExtensionBlocked}
)

type TicTocError struct {
	Code ErrorCode
}

var errorMessages = map[ErrorCode]string{
	ErrInvalidConfig:    "invalid configuration: K must be in [1,64]",
	ErrNoSuchTxn:        "transaction does not exist",
	ErrWrongState:       "transaction is in the wrong state for this operation",
	ErrKeyOutOfRange:    "tuple key out of range",
	ErrLockConflict:     "prepare aborted: lock conflict",
	ErrVersionChanged:   "prepare aborted: read version changed",
	ErrExtensionBlocked: "prepare aborted: read-timestamp extension blocked by another locker",
}

func (e *TicTocError) Error() string {
	if msg, ok := errorMessages[e.Code]; ok {
		return msg
	}
	return "unknown tictoc error"
}

// Is supports errors.Is so callers can distinguish each abort/rejection reason.
func (e *TicTocError) Is(target error) bool {
	var other *TicTocError
	if errors.As(target, &other) {
		return e.Code == other.Code
	}
	return false
}

type readRecord struct {
	key      int
	writeTS  int64
	readTS   int64
	firstVal int64
}

type txn struct {
	id       int64
	status   TxnStatus
	commitTS int64
	reads    []readRecord
	readSeen map[int]int
	writes   map[int]int64
}

type tuple struct {
	value   int64
	writeTS int64
	readTS  int64
	holder  int64
}

type DB struct {
	mu             sync.Mutex
	k              int
	tuples         []tuple
	txns           map[int64]*txn
	nextID         int64
	prepareTouches int64
}

const noHolder = int64(0)

// NewDB creates a database of K tuples (keys 0..K-1). K outside [1,64] is
// rejected and no database is constructed.
func NewDB(k int) (*DB, error) {
	if k < 1 || k > 64 {
		return nil, ErrInvalidConfigErr
	}
	db := &DB{
		k:      k,
		tuples: make([]tuple, k),
		txns:   make(map[int64]*txn),
		nextID: 1,
	}
	for i := range db.tuples {
		db.tuples[i].holder = noHolder
	}
	return db, nil
}

// Begin starts a new active transaction and returns its strictly increasing id.
func (db *DB) Begin() int64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	id := db.nextID
	db.nextID++
	db.txns[id] = &txn{
		id:       id,
		status:   StatusActive,
		readSeen: make(map[int]int),
		writes:   make(map[int]int64),
	}
	return id
}

// Read returns the value visible to t for key. A prior write wins over a prior
// read, and a prior read is never refreshed. Locks held by others do not block
// reads.
func (db *DB) Read(t int64, key int) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	tr, err := db.lookupActive(t)
	if err != nil {
		return 0, err
	}
	if key < 0 || key >= db.k {
		return 0, ErrKeyOutOfRangeErr
	}
	if x, wrote := tr.writes[key]; wrote {
		return x, nil
	}
	if idx, seen := tr.readSeen[key]; seen {
		return tr.reads[idx].firstVal, nil
	}
	tp := &db.tuples[key]
	tr.reads = append(tr.reads, readRecord{
		key:      key,
		writeTS:  tp.writeTS,
		readTS:   tp.readTS,
		firstVal: tp.value,
	})
	tr.readSeen[key] = len(tr.reads) - 1
	return tp.value, nil
}

// Write buffers x under t's write set; the last write to a key wins. A prior
// read is not required.
func (db *DB) Write(t int64, key int, x int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	tr, err := db.lookupActive(t)
	if err != nil {
		return err
	}
	if key < 0 || key >= db.k {
		return ErrKeyOutOfRangeErr
	}
	tr.writes[key] = x
	return nil
}

func (db *DB) lookupActive(t int64) (*txn, error) {
	tr, ok := db.txns[t]
	if !ok {
		return nil, ErrNoSuchTxnErr
	}
	if tr.status != StatusActive {
		return nil, ErrWrongStateErr
	}
	return tr, nil
}

// Prepare runs the four TicToc validation steps and, on success, leaves t in the
// prepared state holding the write-set locks and returns the commit timestamp.
// On any abort all locks taken and read-timestamp extensions performed by this
// call are rolled back.
func (db *DB) Prepare(t int64) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.prepareTouches = 0

	tr, ok := db.txns[t]
	if !ok {
		return 0, ErrNoSuchTxnErr
	}
	if tr.status != StatusActive {
		return 0, ErrWrongStateErr
	}

	writeKeys := sortedKeys(tr.writes)

	// Step 1: lock the write set in ascending key order.
	locked := make([]int, 0, len(writeKeys))
	for _, key := range writeKeys {
		db.prepareTouches++
		tp := &db.tuples[key]
		if tp.holder != noHolder && tp.holder != t {
			db.rollbackPrepare(locked, nil)
			tr.status = StatusAborted
			return 0, ErrLockConflictErr
		}
		tp.holder = t
		locked = append(locked, key)
	}

	// Step 2: derive the commit timestamp from live tuple data.
	var commitTS int64
	for _, key := range writeKeys {
		db.prepareTouches++
		if c := db.tuples[key].readTS + 1; c > commitTS {
			commitTS = c
		}
	}
	for _, rec := range tr.reads {
		if rec.writeTS > commitTS {
			commitTS = rec.writeTS
		}
	}

	// Step 3: validate each read record in ascending key order, extending
	// unlocked read timestamps as needed.
	extended := make(map[int]int64)
	readOrder := make([]int, len(tr.reads))
	for i := range readOrder {
		readOrder[i] = i
	}
	sort.Slice(readOrder, func(a, b int) bool {
		return tr.reads[readOrder[a]].key < tr.reads[readOrder[b]].key
	})
	for _, idx := range readOrder {
		rec := tr.reads[idx]
		if rec.readTS >= commitTS {
			continue
		}
		tp := &db.tuples[rec.key]
		db.prepareTouches++
		if tp.writeTS != rec.writeTS {
			db.rollbackPrepare(locked, extended)
			tr.status = StatusAborted
			return 0, ErrVersionChangedErr
		}
		if tp.readTS >= commitTS {
			continue
		}
		switch {
		case tp.holder == t:
			// Own lock on a read key that is also written: Finish installs the
			// final timestamps, so r is not extended here.
			continue
		case tp.holder != noHolder:
			db.rollbackPrepare(locked, extended)
			tr.status = StatusAborted
			return 0, ErrExtensionBlockedErr
		default:
			if _, done := extended[rec.key]; !done {
				extended[rec.key] = tp.readTS
			}
			tp.readTS = commitTS
		}
	}

	// Step 4: success.
	tr.status = StatusPrepared
	tr.commitTS = commitTS
	return commitTS, nil
}

// rollbackPrepare releases every lock acquired by this Prepare call and
// restores every extended read timestamp. Locks are released in reverse
// acquisition order; fields end up byte-identical to before the call.
func (db *DB) rollbackPrepare(locked []int, extended map[int]int64) {
	for i := len(locked) - 1; i >= 0; i-- {
		db.tuples[locked[i]].holder = noHolder
	}
	for key, oldReadTS := range extended {
		db.tuples[key].readTS = oldReadTS
	}
}

// Finish installs the buffered writes and the commit timestamp, releases the
// write-set locks and commits t.
func (db *DB) Finish(t int64) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	tr, ok := db.txns[t]
	if !ok {
		return 0, ErrNoSuchTxnErr
	}
	if tr.status != StatusPrepared {
		return 0, ErrWrongStateErr
	}
	for key, x := range tr.writes {
		tp := &db.tuples[key]
		tp.value = x
		tp.writeTS = tr.commitTS
		tp.readTS = tr.commitTS
		tp.holder = noHolder
	}
	tr.status = StatusCommitted
	return tr.commitTS, nil
}

// Abort releases the write-set locks held by an active or prepared transaction
// and moves it to the aborted state. Read-timestamp extensions made by a
// successful Prepare persist (only Prepare-internal failures roll them back).
func (db *DB) Abort(t int64) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	tr, ok := db.txns[t]
	if !ok {
		return ErrNoSuchTxnErr
	}
	if tr.status != StatusActive && tr.status != StatusPrepared {
		return ErrWrongStateErr
	}
	if tr.status == StatusPrepared {
		for key := range tr.writes {
			db.tuples[key].holder = noHolder
		}
	}
	tr.status = StatusAborted
	return nil
}

// Status reports the lifecycle state of t.
func (db *DB) Status(t int64) (TxnStatus, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	tr, ok := db.txns[t]
	if !ok {
		return 0, ErrNoSuchTxnErr
	}
	return tr.status, nil
}

// CommitTS reports the commit timestamp assigned to a prepared or committed t.
func (db *DB) CommitTS(t int64) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	tr, ok := db.txns[t]
	if !ok {
		return 0, ErrNoSuchTxnErr
	}
	if tr.status != StatusPrepared && tr.status != StatusCommitted {
		return 0, ErrWrongStateErr
	}
	return tr.commitTS, nil
}

// SnapshotTuple is an exported view of a tuple for inspection and tests.
type SnapshotTuple struct {
	Value   int64
	WriteTS int64
	ReadTS  int64
	Holder  int64
}

// Snapshot returns a point-in-time copy of all tuples.
func (db *DB) Snapshot() []SnapshotTuple {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make([]SnapshotTuple, db.k)
	for i, tp := range db.tuples {
		out[i] = SnapshotTuple{Value: tp.value, WriteTS: tp.writeTS, ReadTS: tp.readTS, Holder: tp.holder}
	}
	return out
}

// lastPrepareTouches exposes the non-exported touch counter of the most recent
// Prepare call for verification.
func (db *DB) lastPrepareTouches() int64 {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.prepareTouches
}

func sortedKeys(m map[int]int64) []int {
	keys := make([]int, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}
