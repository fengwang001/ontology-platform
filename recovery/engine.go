// Package recovery implements an ARIES-style transaction rollback and
// crash-restart undo-phase model.
package recovery

import (
	"errors"
	"sort"
	"sync"
)

const (
	MaxTxnID  = 1_000_000
	MaxPageID = 1_000_000
	MaxDelta  = 1_000_000_000
	MaxLmax   = 1_000_000
)

type RecType byte

const (
	RecUpdate       RecType = 'U'
	RecCompensation RecType = 'C'
	RecCommit       RecType = 'K'
	RecEnd          RecType = 'E'
)

type Record struct {
	LSN      int
	Type     RecType
	Txn      int
	Page     int
	Delta    int64
	PrevLSN  int
	UndoNext int
}

var (
	ErrInvalidArg    = errors.New("recovery: invalid argument")
	ErrCrashed       = errors.New("recovery: system crashed")
	ErrNotCrashed    = errors.New("recovery: system not crashed")
	ErrTxnExists     = errors.New("recovery: transaction already exists")
	ErrTxnNotFound   = errors.New("recovery: transaction not found")
	ErrTxnTerminated = errors.New("recovery: transaction terminated")
	ErrBadSavepoint  = errors.New("recovery: invalid savepoint")
	ErrLogFull       = errors.New("recovery: log full")
)

type txnState int

const (
	txnActive txnState = iota
	txnCommitted
	txnAborted
)

type txn struct {
	state   txnState
	lastLSN int
}

type Engine struct {
	mu    sync.Mutex
	lmax  int
	log   []Record
	pages map[int]int64
	txns  map[int]*txn

	crashed        bool
	losers         map[int]bool
	loserList      []int
	next           map[int]int
	restartStarted bool
	pendingEnd     []int
}

func NewEngine(lmax int) (*Engine, error) {
	if lmax < 1 || lmax > MaxLmax {
		return nil, ErrInvalidArg
	}
	return &Engine{
		lmax:  lmax,
		pages: make(map[int]int64),
		txns:  make(map[int]*txn),
	}, nil
}

func validTxn(id int) bool { return id >= 1 && id <= MaxTxnID }
func validPage(p int) bool { return p >= 0 && p <= MaxPageID }
func validDelta(d int64) bool {
	return d != 0 && d >= -MaxDelta && d <= MaxDelta
}

// nextOf computes next(t): the undoNext of the transaction's last record
// when it is a compensation record, otherwise its lastLSN.
func (e *Engine) nextOf(t *txn) int {
	if t.lastLSN == 0 {
		return 0
	}
	r := e.log[t.lastLSN-1]
	if r.Type == RecCompensation {
		return r.UndoNext
	}
	return t.lastLSN
}

// appendLocked appends a record, assigning its LSN, updating the
// transaction's lastLSN and applying the page delta for U and C records.
func (e *Engine) appendLocked(rec Record) {
	rec.LSN = len(e.log) + 1
	e.log = append(e.log, rec)
	t := e.txns[rec.Txn]
	t.lastLSN = rec.LSN
	if rec.Type == RecUpdate || rec.Type == RecCompensation {
		e.pages[rec.Page] += rec.Delta
	}
}

// activeTxnLocked resolves a transaction that must exist and be active.
func (e *Engine) activeTxnLocked(id int) (*txn, error) {
	t, ok := e.txns[id]
	if !ok {
		return nil, ErrTxnNotFound
	}
	if t.state != txnActive {
		return nil, ErrTxnTerminated
	}
	return t, nil
}

func (e *Engine) Begin(id int) error {
	if !validTxn(id) {
		return ErrInvalidArg
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrCrashed
	}
	if _, ok := e.txns[id]; ok {
		return ErrTxnExists
	}
	e.txns[id] = &txn{}
	return nil
}

func (e *Engine) Update(id, page int, d int64) (int, error) {
	if !validTxn(id) || !validPage(page) || !validDelta(d) {
		return 0, ErrInvalidArg
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrCrashed
	}
	t, err := e.activeTxnLocked(id)
	if err != nil {
		return 0, err
	}
	if len(e.log) >= e.lmax {
		return 0, ErrLogFull
	}
	e.appendLocked(Record{Type: RecUpdate, Txn: id, Page: page, Delta: d, PrevLSN: t.lastLSN})
	return t.lastLSN, nil
}

// Save returns the transaction's current lastLSN as a savepoint (may be 0).
func (e *Engine) Save(id int) (int, error) {
	if !validTxn(id) {
		return 0, ErrInvalidArg
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrCrashed
	}
	t, err := e.activeTxnLocked(id)
	if err != nil {
		return 0, err
	}
	return t.lastLSN, nil
}

func (e *Engine) Commit(id int) error {
	if !validTxn(id) {
		return ErrInvalidArg
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrCrashed
	}
	t, err := e.activeTxnLocked(id)
	if err != nil {
		return err
	}
	if len(e.log) >= e.lmax {
		return ErrLogFull
	}
	e.appendLocked(Record{Type: RecCommit, Txn: id, PrevLSN: t.lastLSN})
	t.state = txnCommitted
	return nil
}

// countUndoLocked counts how many update records a rollback to savepoint s
// would compensate, following the same chain walk as the rollback itself.
func (e *Engine) countUndoLocked(t *txn, s int) int {
	k := 0
	for q := e.nextOf(t); q > s; {
		r := e.log[q-1]
		if r.Type == RecCompensation {
			q = r.UndoNext
		} else {
			k++
			q = r.PrevLSN
		}
	}
	return k
}

// rollbackLocked performs the rollback loop, appending one compensation
// record per undone update. Capacity must be checked by the caller.
func (e *Engine) rollbackLocked(t *txn, id, s int) int {
	n := 0
	for q := e.nextOf(t); q > s; {
		r := e.log[q-1]
		if r.Type == RecCompensation {
			q = r.UndoNext
			continue
		}
		e.appendLocked(Record{
			Type:     RecCompensation,
			Txn:      id,
			Page:     r.Page,
			Delta:    -r.Delta,
			PrevLSN:  t.lastLSN,
			UndoNext: r.PrevLSN,
		})
		n++
		q = r.PrevLSN
	}
	return n
}

// validSavepointLocked reports whether s is 0 or the LSN of a record of t.
func (e *Engine) validSavepointLocked(id, s int) bool {
	if s == 0 {
		return true
	}
	if s < 0 || s > len(e.log) {
		return false
	}
	return e.log[s-1].Txn == id
}

// Rollback undoes updates of transaction id back to savepoint s, appending
// one compensation record per undone update. It returns the number of
// compensation records appended.
func (e *Engine) Rollback(id, s int) (int, error) {
	if !validTxn(id) {
		return 0, ErrInvalidArg
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrCrashed
	}
	t, err := e.activeTxnLocked(id)
	if err != nil {
		return 0, err
	}
	if !e.validSavepointLocked(id, s) {
		return 0, ErrBadSavepoint
	}
	k := e.countUndoLocked(t, s)
	if len(e.log)+k > e.lmax {
		return 0, ErrLogFull
	}
	return e.rollbackLocked(t, id, s), nil
}

// Abort rolls the transaction back to 0 and appends an end record,
// terminating the transaction. It returns the total number of records
// appended (k compensation records plus the end record).
func (e *Engine) Abort(id int) (int, error) {
	if !validTxn(id) {
		return 0, ErrInvalidArg
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return 0, ErrCrashed
	}
	t, err := e.activeTxnLocked(id)
	if err != nil {
		return 0, err
	}
	k := e.countUndoLocked(t, 0)
	if len(e.log)+k+1 > e.lmax {
		return 0, ErrLogFull
	}
	n := e.rollbackLocked(t, id, 0)
	e.appendLocked(Record{Type: RecEnd, Txn: id, PrevLSN: t.lastLSN})
	t.state = txnAborted
	return n + 1, nil
}

// Crash marks all active transactions as losers. Afterwards only Restart,
// RestartStep and queries are accepted until the undo phase completes.
func (e *Engine) Crash() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.crashed {
		return ErrCrashed
	}
	e.crashed = true
	e.restartStarted = false
	e.pendingEnd = nil
	e.losers = make(map[int]bool)
	e.loserList = e.loserList[:0]
	e.next = make(map[int]int)
	for id, t := range e.txns {
		if t.state == txnActive {
			e.losers[id] = true
			e.loserList = append(e.loserList, id)
			e.next[id] = e.nextOf(t)
		}
	}
	sort.Ints(e.loserList)
	return nil
}

// appendEndLocked appends the end record of a loser and removes it from
// the loser set.
func (e *Engine) appendEndLocked(id int) {
	t := e.txns[id]
	e.appendLocked(Record{Type: RecEnd, Txn: id, PrevLSN: t.lastLSN})
	t.state = txnAborted
	delete(e.losers, id)
	delete(e.next, id)
}

// restartOneLocked appends exactly one record of the restart undo phase
// following the global order: pending end records first (an end record
// must immediately follow the compensation record that zeroed a loser,
// even across RestartStep calls), then the initial end records of losers
// whose next is already 0 (ascending transaction id), then the loser with
// the largest next pointer. It returns false when the phase is complete.
func (e *Engine) restartOneLocked() bool {
	if len(e.pendingEnd) > 0 {
		id := e.pendingEnd[0]
		e.pendingEnd = e.pendingEnd[1:]
		e.appendEndLocked(id)
		return true
	}
	if !e.restartStarted {
		e.restartStarted = true
		for _, id := range e.loserList {
			if e.losers[id] && e.next[id] == 0 {
				e.pendingEnd = append(e.pendingEnd, id)
			}
		}
		if len(e.pendingEnd) > 0 {
			return e.restartOneLocked()
		}
	}
	best, bestNext := -1, 0
	for _, id := range e.loserList {
		if !e.losers[id] {
			continue
		}
		if e.next[id] > bestNext {
			best, bestNext = id, e.next[id]
		}
	}
	if best < 0 {
		e.crashed = false
		e.losers = nil
		e.loserList = nil
		e.next = nil
		e.pendingEnd = nil
		return false
	}
	q := bestNext
	for {
		r := e.log[q-1]
		if r.Type == RecCompensation {
			q = r.UndoNext
			e.next[best] = q
			if q == 0 {
				e.appendEndLocked(best)
				return true
			}
			continue
		}
		t := e.txns[best]
		e.appendLocked(Record{
			Type:     RecCompensation,
			Txn:      best,
			Page:     r.Page,
			Delta:    -r.Delta,
			PrevLSN:  t.lastLSN,
			UndoNext: r.PrevLSN,
		})
		e.next[best] = r.PrevLSN
		if r.PrevLSN == 0 {
			e.pendingEnd = append(e.pendingEnd, best)
		}
		return true
	}
}

// Restart runs the whole undo phase and returns the number of records
// appended. It is not bounded by Lmax.
func (e *Engine) Restart() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.crashed {
		return 0, ErrNotCrashed
	}
	n := 0
	for e.restartOneLocked() {
		n++
	}
	return n, nil
}

// RestartStep runs the undo phase until n records have been appended by
// this call (or the phase completes) and returns the number appended.
// Splitting the undo phase into RestartStep calls followed by a final
// Restart produces exactly the same log as a single Restart.
func (e *Engine) RestartStep(n int) (int, error) {
	if n < 1 {
		return 0, ErrInvalidArg
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.crashed {
		return 0, ErrNotCrashed
	}
	c := 0
	for c < n && e.restartOneLocked() {
		c++
	}
	return c, nil
}

// Log returns a copy of the log.
func (e *Engine) Log() []Record {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Record, len(e.log))
	copy(out, e.log)
	return out
}

// LogLen returns the current number of log records.
func (e *Engine) LogLen() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.log)
}

// Page returns the current value of a page (sum of all U and C deltas).
func (e *Engine) Page(p int) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pages[p]
}

// LastLSN returns the transaction's lastLSN and whether it exists.
func (e *Engine) LastLSN(id int) (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.txns[id]
	if !ok {
		return 0, false
	}
	return t.lastLSN, true
}

// Crashed reports whether the system is between Crash and the completion
// of the restart undo phase.
func (e *Engine) Crashed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.crashed
}
