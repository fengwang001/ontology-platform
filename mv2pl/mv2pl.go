// Package mv2pl implements a multi-version two-phase locking (MV2PL)
// certification lock manager.
//
// Readers always read the last committed value of a key while writers
// first take an X lock and stage their writes in a transaction-local
// buffer. At commit time every X lock is converted, in ascending key
// order, into a C (certification) lock that conflicts with every lock
// of any other transaction; the transaction commits only after it holds
// C on every key it wrote. Conversion requests are queued ahead of all
// ordinary requests on the same key so that certifying transactions are
// never starved by newly arriving readers or writers.
//
// All methods are safe for concurrent use; their effect is equivalent
// to some serial execution of the calls.
package mv2pl

import (
	"errors"
	"fmt"
	"sync"
)

// MaxKeys is the maximum number of keys a Manager can be built with.
const MaxKeys = 64

// ErrInvalidKeyCount is returned by New when K is outside [1, MaxKeys].
var ErrInvalidKeyCount = errors.New("mv2pl: key count must be between 1 and 64")

// Mode is a lock mode.
type Mode int

const (
	// S is a shared read lock on the committed value of a key.
	S Mode = iota
	// X is an exclusive write lock; the writer stages values in its buffer.
	X
	// C is a certification lock; it conflicts with every lock of other
	// transactions, including another C.
	C
)

func (m Mode) String() string {
	switch m {
	case S:
		return "S"
	case X:
		return "X"
	case C:
		return "C"
	}
	return "?"
}

// compatible reports whether locks of modes a and b held by two different
// transactions may coexist on the same key. Only S/S and S/X pairs are
// compatible. Locks of a single transaction never conflict with each other.
func compatible(a, b Mode) bool {
	if a == C || b == C {
		return false
	}
	if a == X && b == X {
		return false
	}
	return true
}

// State is the lifecycle state of a transaction.
type State int

const (
	// Active means the transaction may issue new operations.
	Active State = iota
	// Waiting means the transaction is blocked on a queued S/X request.
	Waiting
	// Committing means the transaction is converting its write locks to C.
	Committing
	// Committed means the transaction has committed.
	Committed
	// Aborted means the transaction was aborted (explicitly or by deadlock).
	Aborted
)

func (s State) String() string {
	switch s {
	case Active:
		return "active"
	case Waiting:
		return "waiting"
	case Committing:
		return "committing"
	case Committed:
		return "committed"
	case Aborted:
		return "aborted"
	}
	return "?"
}

// RejectReason explains why a call was rejected. Rejected calls never
// mutate any state.
type RejectReason int

const (
	// RejectNone means the call was accepted.
	RejectNone RejectReason = iota
	// RejectNoSuchTxn means the transaction id does not exist.
	RejectNoSuchTxn
	// RejectBadState means the transaction is not in a state that allows
	// the requested operation.
	RejectBadState
	// RejectBadKey means the key is outside [0, K).
	RejectBadKey
)

func (r RejectReason) String() string {
	switch r {
	case RejectNone:
		return "none"
	case RejectNoSuchTxn:
		return "no such transaction"
	case RejectBadState:
		return "bad transaction state"
	case RejectBadKey:
		return "key out of range"
	}
	return "?"
}

// EventKind classifies events emitted by calls.
type EventKind int

const (
	// EventGrant is emitted whenever a lock request is granted, whether
	// immediately or out of the wait queue. For an S grant Value is the
	// committed value read; for an X grant Value is the value staged in
	// the transaction's buffer.
	EventGrant EventKind = iota
	// EventCommit is emitted when a transaction commits; Writes holds the
	// key/value pairs that became the new committed values, in ascending
	// key order.
	EventCommit
)

// WriteEntry is one committed key/value pair of a commit event.
type WriteEntry struct {
	Key   int
	Value int64
}

// Event is a single state-change notification. Events of one call are
// returned in the exact order in which they happened.
type Event struct {
	Kind   EventKind
	Txn    int
	Key    int          // EventGrant only
	Mode   Mode         // EventGrant only
	Value  int64        // EventGrant only (see EventGrant doc)
	Writes []WriteEntry // EventCommit only
}

func (e Event) String() string {
	if e.Kind == EventCommit {
		return fmt.Sprintf("commit(t%d,%v)", e.Txn, e.Writes)
	}
	return fmt.Sprintf("grant(t%d,k%d,%s,v%d)", e.Txn, e.Key, e.Mode, e.Value)
}

// Result is the outcome of one Read/Write/Commit/Abort call.
type Result struct {
	// OK is false when the call was rejected; Reject then tells why.
	OK     bool
	Reject RejectReason
	// Value is the value returned by Read when it completes within the
	// call. A Read that is queued delivers its value later through an
	// EventGrant event.
	Value int64
	// Deadlock is true when the call's request was enqueued and the
	// resulting deadlock check aborted the calling transaction.
	Deadlock bool
	// Events lists every grant/commit caused by this call, in order.
	Events []Event
}

// grant is one granted lock on a key.
type grant struct {
	txn  int
	mode Mode
}

// request is one queued lock request on a key.
type request struct {
	txn  int
	mode Mode
	conv bool  // conversion (C) request
	val  int64 // value to stage in the buffer when an X request is granted
}

// txn is the per-transaction bookkeeping.
type txn struct {
	state  State
	locks  map[int]Mode  // key -> held lock mode
	buffer map[int]int64 // staged writes, only for keys locked in X
}

// Manager is an MV2PL certification lock manager.
type Manager struct {
	mu        sync.Mutex
	keys      int
	nextTxn   int
	committed []int64
	granted   [][]grant   // per key
	queues    [][]request // per key
	txns      map[int]*txn
}

// New builds a Manager for K keys. K must be in [1, MaxKeys]; any other
// value is rejected as an illegal configuration.
func New(K int) (*Manager, error) {
	if K < 1 || K > MaxKeys {
		return nil, ErrInvalidKeyCount
	}
	m := &Manager{
		keys:      K,
		committed: make([]int64, K),
		granted:   make([][]grant, K),
		queues:    make([][]request, K),
		txns:      make(map[int]*txn),
	}
	return m, nil
}

// Begin starts a new transaction and returns its id. Ids are handed out
// in increasing order starting at 1.
func (m *Manager) Begin() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextTxn++
	m.txns[m.nextTxn] = &txn{
		state:  Active,
		locks:  make(map[int]Mode),
		buffer: make(map[int]int64),
	}
	return m.nextTxn
}

func rejected(reason RejectReason) Result {
	return Result{OK: false, Reject: reason}
}

// lookup validates the transaction id and, for Read/Write/Commit, that
// the transaction is active. Rejection reasons are reported in the
// order: unknown transaction, bad state, bad key.
func (m *Manager) lookup(txnID int) (*txn, Result, bool) {
	tx, ok := m.txns[txnID]
	if !ok {
		return nil, rejected(RejectNoSuchTxn), false
	}
	return tx, Result{}, true
}

func (m *Manager) checkKey(key int) (Result, bool) {
	if key < 0 || key >= m.keys {
		return rejected(RejectBadKey), false
	}
	return Result{}, true
}

// Read returns the value of key visible to txn. A transaction holding X
// on key reads its own buffered value without taking another lock; a
// transaction already holding S reads the committed value. Otherwise an
// S lock is requested: it is granted immediately only when the key's
// queue is empty and no other transaction holds a conflicting lock;
// otherwise the request is appended to the queue and the transaction
// turns waiting. Every enqueue is followed by a deadlock check that may
// abort the caller.
func (m *Manager) Read(txnID, key int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, res, ok := m.lookup(txnID)
	if !ok {
		return res
	}
	if tx.state != Active {
		return rejected(RejectBadState)
	}
	if res, ok := m.checkKey(key); !ok {
		return res
	}
	if mode, held := tx.locks[key]; held {
		if mode == X {
			return Result{OK: true, Value: tx.buffer[key]}
		}
		return Result{OK: true, Value: m.committed[key]}
	}
	if len(m.queues[key]) == 0 && m.grantable(key, txnID, S) {
		m.setGrant(key, txnID, S)
		tx.locks[key] = S
		value := m.committed[key]
		return Result{OK: true, Value: value, Events: []Event{
			{Kind: EventGrant, Txn: txnID, Key: key, Mode: S, Value: value},
		}}
	}
	m.queues[key] = append(m.queues[key], request{txn: txnID, mode: S})
	tx.state = Waiting
	res = Result{OK: true}
	if m.deadlocked(txnID) {
		m.abortTxn(txnID, tx, &res.Events)
		res.Deadlock = true
	}
	return res
}

// Write stages value for key in the transaction's buffer. A transaction
// already holding X on key simply overwrites its buffer. Otherwise an X
// lock is requested (holding S does not turn this into a conversion):
// it is granted immediately only when the key's queue is empty and no
// other transaction holds a conflicting lock; otherwise the request is
// appended to the queue, the transaction turns waiting, and a deadlock
// check runs.
func (m *Manager) Write(txnID, key int, value int64) Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, res, ok := m.lookup(txnID)
	if !ok {
		return res
	}
	if tx.state != Active {
		return rejected(RejectBadState)
	}
	if res, ok := m.checkKey(key); !ok {
		return res
	}
	if tx.locks[key] == X {
		tx.buffer[key] = value
		return Result{OK: true}
	}
	if len(m.queues[key]) == 0 && m.grantable(key, txnID, X) {
		m.setGrant(key, txnID, X)
		tx.locks[key] = X
		tx.buffer[key] = value
		return Result{OK: true, Events: []Event{
			{Kind: EventGrant, Txn: txnID, Key: key, Mode: X, Value: value},
		}}
	}
	m.queues[key] = append(m.queues[key], request{txn: txnID, mode: X, val: value})
	tx.state = Waiting
	res = Result{OK: true}
	if m.deadlocked(txnID) {
		m.abortTxn(txnID, tx, &res.Events)
		res.Deadlock = true
	}
	return res
}

// Commit commits the transaction. Without X locks it commits at once
// and releases everything. Otherwise its X locks are converted to C in
// ascending key order: a conversion compatible with the locks of other
// transactions is granted immediately (ignoring the queue, replacing
// the X lock); otherwise the conversion is queued after the last
// conversion request and before all ordinary requests of that key, the
// transaction turns committing, and a deadlock check runs per enqueued
// conversion. A deadlock abort stops the commit; remaining keys are not
// processed. Once the transaction holds C on every key it wrote, the
// buffered values become committed and all locks are released.
func (m *Manager) Commit(txnID int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, res, ok := m.lookup(txnID)
	if !ok {
		return res
	}
	if tx.state != Active {
		return rejected(RejectBadState)
	}
	res = Result{OK: true}
	var pending keySet
	hasX := false
	for key := 0; key < m.keys; key++ {
		if tx.locks[key] == X {
			hasX = true
			break
		}
	}
	if !hasX {
		m.finishCommit(txnID, tx, &pending, &res.Events)
		m.drain(&pending, &res.Events)
		return res
	}
	for key := 0; key < m.keys; key++ {
		if tx.locks[key] != X {
			continue
		}
		if m.grantable(key, txnID, C) {
			m.setGrant(key, txnID, C)
			tx.locks[key] = C
			res.Events = append(res.Events, Event{Kind: EventGrant, Txn: txnID, Key: key, Mode: C})
			continue
		}
		m.queues[key] = insertConversion(m.queues[key], request{txn: txnID, mode: C, conv: true})
		tx.state = Committing
		if m.deadlocked(txnID) {
			m.abortTxn(txnID, tx, &res.Events)
			res.Deadlock = true
			return res
		}
	}
	if tx.state == Active {
		m.finishCommit(txnID, tx, &pending, &res.Events)
		m.drain(&pending, &res.Events)
	}
	return res
}

// Abort aborts the transaction exactly like a deadlock abort does, but
// without running a deadlock check: every queued request is removed,
// every lock released, the buffer discarded. The grants and commits
// triggered by the release are returned in order. The transaction must
// be active, waiting or committing.
func (m *Manager) Abort(txnID int) Result {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, res, ok := m.lookup(txnID)
	if !ok {
		return res
	}
	if tx.state != Active && tx.state != Waiting && tx.state != Committing {
		return rejected(RejectBadState)
	}
	res = Result{OK: true}
	m.abortTxn(txnID, tx, &res.Events)
	return res
}
