// Package aries implements an ARIES-style transaction rollback and
// crash-restart undo-phase model.
//
// The system maintains a log (LSN starts at 1, +1 per appended record,
// capacity Lmax), page values (all start at 0), and per-transaction
// state. Record types:
//
//	U update:       txn, page, delta d
//	C compensation: txn, page, delta, undoNext
//	K commit
//	E end
//
// Every record carries prevLSN = the transaction's lastLSN at append
// time (0 if none). Appending a U or C immediately applies its delta to
// the page (redo is assumed done; this model only handles undo).
package aries

import (
	"fmt"
	"sort"
	"sync"
)

// RecType is a log record type: 'U', 'C', 'K' or 'E'.
type RecType byte

const (
	RecUpdate RecType = 'U'
	RecComp   RecType = 'C'
	RecCommit RecType = 'K'
	RecEnd    RecType = 'E'
)

// Record is one log record. Page and Delta are meaningful for U and C;
// UndoNext is meaningful for C only.
type Record struct {
	LSN      int
	Type     RecType
	Txn      int
	Page     int
	Delta    int64
	Prev     int
	UndoNext int
}

// ErrCode distinguishes rejection reasons.
type ErrCode int

const (
	ErrInvalidParam     ErrCode = iota // txn/page/d/Lmax/n out of range
	ErrCrashed                         // normal op while crashed
	ErrNotCrashed                      // Restart/RestartStep while not crashed
	ErrTxnExists                       // Begin with an existing txn id
	ErrTxnNotFound                     // op on unknown txn id
	ErrTxnTerminated                   // op on a terminated txn
	ErrInvalidSavepoint                // savepoint not 0 and not a record of the txn
	ErrLogFull                         // not enough remaining log capacity
)

// Error is a rejected operation. Code is the first applicable reason in
// the mandated check order.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

const (
	minLSNCap = 1
	maxLSNCap = 1_000_000
	minTxn    = 1
	maxTxn    = 1_000_000
	minPage   = 0
	maxPage   = 1_000_000
	maxDelta  = 1_000_000_000
)

type txn struct {
	last   int
	active bool
}

// loser tracks one losing transaction during the restart undo phase.
type loser struct {
	next  int
	ended bool
}

// restartState makes the undo phase resumable across RestartStep calls.
type restartState struct {
	losers   map[int]*loser
	order    []int // loser txn ids, ascending, for the initial E pass
	initIdx  int
	initDone bool
	pendingE []int // losers whose E is due, in the order they zeroed out
}

// System is the undo-phase model. All methods are safe for concurrent
// use; the result is equivalent to some serial order.
type System struct {
	mu      sync.Mutex
	lmax    int
	log     []Record
	pages   map[int]int64
	txns    map[int]*txn
	crashed bool
	rs      *restartState
}

// NewSystem creates a system with log capacity lmax (1..10^6).
func NewSystem(lmax int) (*System, error) {
	if lmax < minLSNCap || lmax > maxLSNCap {
		return nil, newError(ErrInvalidParam, "Lmax %d out of range [%d,%d]", lmax, minLSNCap, maxLSNCap)
	}
	return &System{
		lmax:  lmax,
		pages: make(map[int]int64),
		txns:  make(map[int]*txn),
	}, nil
}

// Begin registers transaction t.
func (s *System) Begin(t int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.begin(t)
}

func (s *System) begin(t int) error {
	if err := checkTxnID(t); err != nil {
		return err
	}
	if s.crashed {
		return newError(ErrCrashed, "system is crashed")
	}
	if _, ok := s.txns[t]; ok {
		return newError(ErrTxnExists, "txn %d already exists", t)
	}
	s.txns[t] = &txn{active: true}
	return nil
}

// Update appends a U record and applies d to page p. Returns the LSN.
func (s *System) Update(t, p int, d int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTxnID(t); err != nil {
		return 0, err
	}
	if p < minPage || p > maxPage {
		return 0, newError(ErrInvalidParam, "page %d out of range [%d,%d]", p, minPage, maxPage)
	}
	if d == 0 || d < -maxDelta || d > maxDelta {
		return 0, newError(ErrInvalidParam, "delta %d out of range", d)
	}
	tx, err := s.activeTxn(t)
	if err != nil {
		return 0, err
	}
	if len(s.log)+1 > s.lmax {
		return 0, newError(ErrLogFull, "log is full (%d/%d)", len(s.log), s.lmax)
	}
	lsn := s.append(Record{Type: RecUpdate, Txn: t, Page: p, Delta: d, Prev: tx.last})
	tx.last = lsn
	s.pages[p] += d
	return lsn, nil
}

// Save returns the transaction's current lastLSN as a savepoint (may be 0).
func (s *System) Save(t int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTxnID(t); err != nil {
		return 0, err
	}
	tx, err := s.activeTxn(t)
	if err != nil {
		return 0, err
	}
	return tx.last, nil
}

// Commit appends K and terminates the transaction.
func (s *System) Commit(t int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTxnID(t); err != nil {
		return err
	}
	tx, err := s.activeTxn(t)
	if err != nil {
		return err
	}
	if len(s.log)+1 > s.lmax {
		return newError(ErrLogFull, "log is full (%d/%d)", len(s.log), s.lmax)
	}
	lsn := s.append(Record{Type: RecCommit, Txn: t, Prev: tx.last})
	tx.last = lsn
	tx.active = false
	return nil
}

// Rollback undoes updates of t back to savepoint sp, appending one C
// per undone U. Returns the number of C records appended.
func (s *System) Rollback(t, sp int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTxnID(t); err != nil {
		return 0, err
	}
	tx, err := s.activeTxn(t)
	if err != nil {
		return 0, err
	}
	if !s.validSavepoint(t, sp) {
		return 0, newError(ErrInvalidSavepoint, "savepoint %d is not a record of txn %d", sp, t)
	}
	k := countUndo(s.log, s.nextOf(tx), sp)
	if len(s.log)+k > s.lmax {
		return 0, newError(ErrLogFull, "log is full: need %d, have %d", k, s.lmax-len(s.log))
	}
	s.undoTo(tx, sp)
	return k, nil
}

// Abort is Rollback(t, 0) followed by an E record; it terminates the
// transaction. Returns the number of records appended (k+1).
func (s *System) Abort(t int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkTxnID(t); err != nil {
		return 0, err
	}
	tx, err := s.activeTxn(t)
	if err != nil {
		return 0, err
	}
	k := countUndo(s.log, s.nextOf(tx), 0)
	if len(s.log)+k+1 > s.lmax {
		return 0, newError(ErrLogFull, "log is full: need %d, have %d", k+1, s.lmax-len(s.log))
	}
	s.undoTo(tx, 0)
	lsn := s.append(Record{Type: RecEnd, Txn: t, Prev: tx.last})
	tx.last = lsn
	tx.active = false
	return k + 1, nil
}

// activeTxn applies the shared check order: crashed, not found,
// terminated. Parameter checks happen before this is called.
func (s *System) activeTxn(t int) (*txn, error) {
	if s.crashed {
		return nil, newError(ErrCrashed, "system is crashed")
	}
	tx, ok := s.txns[t]
	if !ok {
		return nil, newError(ErrTxnNotFound, "txn %d does not exist", t)
	}
	if !tx.active {
		return nil, newError(ErrTxnTerminated, "txn %d is terminated", t)
	}
	return tx, nil
}

// validSavepoint reports whether sp is 0 or the LSN of a record of t.
func (s *System) validSavepoint(t, sp int) bool {
	if sp == 0 {
		return true
	}
	if sp < 0 || sp > len(s.log) {
		return false
	}
	return s.log[sp-1].Txn == t
}

// nextOf computes next(t): if lastLSN points at a C, its undoNext,
// otherwise lastLSN itself.
func (s *System) nextOf(tx *txn) int {
	if tx.last == 0 {
		return 0
	}
	rec := s.log[tx.last-1]
	if rec.Type == RecComp {
		return rec.UndoNext
	}
	return tx.last
}

// countUndo counts how many U records the walk from q down to savepoint
// sp would undo, following the rollback rules without mutating anything.
func countUndo(log []Record, q, sp int) int {
	k := 0
	for q > sp {
		rec := log[q-1]
		if rec.Type == RecUpdate {
			k++
			q = rec.Prev
		} else { // C: jump only
			q = rec.UndoNext
		}
	}
	return k
}

// undoTo performs the rollback walk from next(t) down to savepoint sp,
// appending one C per undone U and applying the inverse deltas.
func (s *System) undoTo(tx *txn, sp int) {
	q := s.nextOf(tx)
	for q > sp {
		rec := s.log[q-1]
		if rec.Type == RecUpdate {
			lsn := s.append(Record{
				Type:     RecComp,
				Txn:      rec.Txn,
				Page:     rec.Page,
				Delta:    -rec.Delta,
				Prev:     tx.last,
				UndoNext: rec.Prev,
			})
			tx.last = lsn
			s.pages[rec.Page] += -rec.Delta
			q = rec.Prev
		} else { // C: jump only, no record appended
			q = rec.UndoNext
		}
	}
}

// append appends a record and returns its LSN.
func (s *System) append(rec Record) int {
	rec.LSN = len(s.log) + 1
	s.log = append(s.log, rec)
	return rec.LSN
}

// Crash turns all active transactions into losers. Afterwards only
// Restart, RestartStep and queries are accepted.
func (s *System) Crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.crashed {
		return
	}
	s.crashed = true
	rs := &restartState{losers: make(map[int]*loser)}
	for id, tx := range s.txns {
		if tx.active {
			rs.losers[id] = &loser{next: s.nextOf(tx)}
		}
	}
	for id := range rs.losers {
		rs.order = append(rs.order, id)
	}
	sort.Ints(rs.order)
	s.rs = rs
}

// Restart runs the whole undo phase and returns the number of records
// appended by this call.
func (s *System) Restart() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.crashed {
		return 0, newError(ErrNotCrashed, "system is not crashed")
	}
	return s.runRestart(-1), nil
}

// RestartStep runs the undo phase but returns immediately after this
// call has appended n records (C or E).
func (s *System) RestartStep(n int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n < 1 {
		return 0, newError(ErrInvalidParam, "n %d must be >= 1", n)
	}
	if !s.crashed {
		return 0, newError(ErrNotCrashed, "system is not crashed")
	}
	return s.runRestart(n), nil
}

// runRestart executes the undo phase until it finishes or the append
// budget (number of C/E records, -1 = unlimited) is exhausted. It is
// not limited by Lmax. Returns the number of records appended.
func (s *System) runRestart(budget int) int {
	appended := 0
	for {
		if s.restartFinished() {
			s.finishRestart()
			return appended
		}
		if budget == 0 {
			return appended
		}
		// Initial pass: losers whose next is already 0 get an E each,
		// in ascending txn id order.
		if !s.rs.initDone {
			stepped := false
			for s.rs.initIdx < len(s.rs.order) {
				id := s.rs.order[s.rs.initIdx]
				s.rs.initIdx++
				lz := s.rs.losers[id]
				if lz.next == 0 && !lz.ended {
					s.appendEnd(id)
					appended++
					if budget > 0 {
						budget--
					}
					stepped = true
					break
				}
			}
			if stepped {
				continue
			}
			s.rs.initDone = true
			continue
		}
		// A loser whose next just became 0 gets its E before any other
		// record, even across RestartStep calls.
		if len(s.rs.pendingE) > 0 {
			id := s.rs.pendingE[0]
			s.rs.pendingE = s.rs.pendingE[1:]
			s.appendEnd(id)
			appended++
			if budget > 0 {
				budget--
			}
			continue
		}
		// Pick the loser with the largest next > 0.
		best, bestNext := -1, 0
		for id, lz := range s.rs.losers {
			if !lz.ended && lz.next > bestNext {
				best, bestNext = id, lz.next
			}
		}
		lz := s.rs.losers[best]
		rec := s.log[lz.next-1]
		if rec.Type == RecUpdate {
			tx := s.txns[best]
			lsn := s.append(Record{
				Type:     RecComp,
				Txn:      best,
				Page:     rec.Page,
				Delta:    -rec.Delta,
				Prev:     tx.last,
				UndoNext: rec.Prev,
			})
			tx.last = lsn
			s.pages[rec.Page] += -rec.Delta
			lz.next = rec.Prev
			appended++
			if budget > 0 {
				budget--
			}
		} else { // C: jump only, no record appended
			lz.next = rec.UndoNext
		}
		if lz.next == 0 {
			s.rs.pendingE = append(s.rs.pendingE, best)
		}
	}
}

// restartFinished reports whether every loser has its E record.
func (s *System) restartFinished() bool {
	for _, lz := range s.rs.losers {
		if !lz.ended {
			return false
		}
	}
	return true
}

// appendEnd appends an E record for a loser.
func (s *System) appendEnd(id int) {
	tx := s.txns[id]
	lsn := s.append(Record{Type: RecEnd, Txn: id, Prev: tx.last})
	tx.last = lsn
	s.rs.losers[id].ended = true
}

// finishRestart ends the undo phase: losers terminate and normal
// operations are accepted again.
func (s *System) finishRestart() {
	for id := range s.rs.losers {
		s.txns[id].active = false
	}
	s.rs = nil
	s.crashed = false
}

// Log returns a copy of the current log.
func (s *System) Log() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.log))
	copy(out, s.log)
	return out
}

// Page returns the current value of page p.
func (s *System) Page(p int) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pages[p]
}

// LastLSN returns the transaction's lastLSN.
func (s *System) LastLSN(t int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.txns[t]
	if !ok {
		return 0, false
	}
	return tx.last, true
}

// Crashed reports whether the system is in the crashed state.
func (s *System) Crashed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.crashed
}

func checkTxnID(t int) error {
	if t < minTxn || t > maxTxn {
		return newError(ErrInvalidParam, "txn %d out of range [%d,%d]", t, minTxn, maxTxn)
	}
	return nil
}
