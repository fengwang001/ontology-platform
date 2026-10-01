package recovery

import (
	"errors"
	"fmt"
	"sync"
)

// Append validation errors, reported in the fixed order below;
// only the first matching reason is returned per rejected record.
var (
	// ErrLSNNotIncreasing: appended LSN is not greater than the current max LSN.
	ErrLSNNotIncreasing = errors.New("recovery: LSN not greater than current max LSN")
	// ErrEndCkptUnknownBegin: EndCkpt.begin is not the LSN of an existing BeginCkpt.
	ErrEndCkptUnknownBegin = errors.New("recovery: EndCkpt begin is not an existing BeginCkpt LSN")
	// ErrEndCkptDuplicate: the referenced BeginCkpt already has an EndCkpt.
	ErrEndCkptDuplicate = errors.New("recovery: BeginCkpt already has an EndCkpt")
	// ErrTxnAlreadyEnded: the record references a transaction that has ended.
	ErrTxnAlreadyEnded = errors.New("recovery: record references an already ended transaction")
	// ErrTxnNeverSeen: Commit/Abort/End references a transaction that never appeared.
	ErrTxnNeverSeen = errors.New("recovery: Commit/Abort/End references a transaction that never appeared")
)

// DirtyPageEntry is one sorted DPT row: page -> recLSN.
type DirtyPageEntry struct {
	Page   PageID
	RecLSN LSN
}

// ActiveTxnEntry is one sorted ATT row: txn -> (status, lastLSN).
type ActiveTxnEntry struct {
	Txn   TxnID
	Entry ATTEntry
}

// Result is the outcome of the analysis phase.
type Result struct {
	// NeedRedo is false when the DPT is empty (no redo required).
	NeedRedo bool
	// RedoLSN is the smallest recLSN in the DPT; valid only when NeedRedo.
	RedoLSN LSN
	// DPT holds the dirty page table sorted by page ascending.
	DPT []DirtyPageEntry
	// ATT holds the active transaction table sorted by txn ascending.
	ATT []ActiveTxnEntry
	// Failed holds txns whose ATT status is Running or Aborting, ascending.
	Failed []TxnID
}

// Log is an append-only ARIES log safe for concurrent Append/Analyze.
type Log struct {
	mu      sync.RWMutex
	records []Record
	maxLSN  LSN
	hasAny  bool

	begunCkpt map[LSN]bool // LSNs of BeginCkpt records
	endedCkpt map[LSN]bool // BeginCkpt LSNs that already have an EndCkpt
	txnSeen   map[TxnID]bool
	txnEnded  map[TxnID]bool
}

// NewLog returns an empty log.
func NewLog() *Log {
	return &Log{
		begunCkpt: make(map[LSN]bool),
		endedCkpt: make(map[LSN]bool),
		txnSeen:   make(map[TxnID]bool),
		txnEnded:  make(map[TxnID]bool),
	}
}

// Append validates and appends a record. On rejection the log is unchanged.
// Validation reasons are checked in a fixed order and only the first is
// reported:
//  1. LSN not greater than the current max LSN.
//  2. EndCkpt whose begin is not an existing BeginCkpt LSN, or whose
//     BeginCkpt already has an EndCkpt.
//  3. The record references an already ended transaction.
//  4. Commit/Abort/End references a transaction that never appeared
//     (transactions in checkpoint snapshots count as appeared).
func (l *Log) Append(rec Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// 1. LSN must be strictly increasing.
	if l.hasAny && rec.LSN <= l.maxLSN {
		return fmt.Errorf("%w: got %d, max is %d", ErrLSNNotIncreasing, rec.LSN, l.maxLSN)
	}

	// 2. EndCkpt must reference an existing, not-yet-ended BeginCkpt.
	if rec.Kind == KindEndCkpt {
		if !l.begunCkpt[rec.Begin] {
			return fmt.Errorf("%w: begin=%d", ErrEndCkptUnknownBegin, rec.Begin)
		}
		if l.endedCkpt[rec.Begin] {
			return fmt.Errorf("%w: begin=%d", ErrEndCkptDuplicate, rec.Begin)
		}
	}

	// 3. No record may reference an already ended transaction.
	if rec.referencesTxn() && l.txnEnded[rec.Txn] {
		return fmt.Errorf("%w: txn=%d kind=%s", ErrTxnAlreadyEnded, rec.Txn, rec.Kind)
	}

	// 4. Commit/Abort/End must reference a transaction that has appeared.
	switch rec.Kind {
	case KindCommit, KindAbort, KindEnd:
		if !l.txnSeen[rec.Txn] {
			return fmt.Errorf("%w: txn=%d kind=%s", ErrTxnNeverSeen, rec.Txn, rec.Kind)
		}
	}

	// All checks passed: apply the record.
	l.records = append(l.records, rec)
	l.maxLSN = rec.LSN
	l.hasAny = true

	switch rec.Kind {
	case KindBeginCkpt:
		l.begunCkpt[rec.LSN] = true
	case KindEndCkpt:
		l.endedCkpt[rec.Begin] = true
		for txn := range rec.ATT {
			l.txnSeen[txn] = true
		}
	}
	if rec.referencesTxn() {
		l.txnSeen[rec.Txn] = true
		if rec.Kind == KindEnd {
			l.txnEnded[rec.Txn] = true
		}
	}
	return nil
}

// Analyze runs the ARIES analysis phase over the current log.
// It does not modify the log.
func (l *Log) Analyze() Result {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return analyzeRecords(l.records)
}

// Records returns a copy of the current log records (for tests/replay).
func (l *Log) Records() []Record {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Record, len(l.records))
	copy(out, l.records)
	return out
}
