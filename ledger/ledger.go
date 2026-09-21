// Package ledger coordinates a WAL and a shard set into atomic,
// crash-recoverable transfers.
package ledger

import (
	"errors"
	"sync"

	"ontology/shard"
	"ontology/wal"
)

// CrashPoint selects where the next Transfer is interrupted.
type CrashPoint int

const (
	// CrashNone disables crash injection.
	CrashNone CrashPoint = iota
	// CrashAfterAppend interrupts after the WAL append, before any Apply.
	CrashAfterAppend
	// CrashAfterDebit interrupts after the debit shard is applied,
	// before the credit shard.
	CrashAfterDebit
	// CrashBeforeMeta interrupts after both Applies, before the
	// commit metadata is updated.
	CrashBeforeMeta
)

var (
	ErrInvalidAmount = errors.New("ledger: amount must be positive")
	ErrSameShard     = errors.New("ledger: from and to must differ")
	ErrShardRange    = errors.New("ledger: shard index out of range")
	ErrInsufficient  = errors.New("ledger: insufficient balance")
	ErrCrashed       = errors.New("ledger: transfer interrupted by crash")
)

// Ledger coordinates a WAL and a shard set.
//
// Concurrency tradeoff: one mutex serializes Transfer, Recover and
// Checkpoint. Recover is therefore fully exclusive rather than
// concurrent with transfers — simpler and obviously correct, at the
// cost of blocking transfers while recovery runs.
type Ledger struct {
	mu      sync.Mutex
	log     *wal.Log
	shards  *shard.Set
	nextTxn uint64 // last transaction id handed out
	meta    uint64 // last transaction id whose commit metadata was written
	crash   CrashPoint
}

// New creates a Ledger on top of l and s.
func New(l *wal.Log, s *shard.Set) *Ledger {
	return &Ledger{log: l, shards: s}
}

// CrashAt arms a crash point; the next Transfer is interrupted there
// exactly once.
func (g *Ledger) CrashAt(p CrashPoint) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.crash = p
}

// Transfer moves amount from shard from to shard to atomically: the
// record pair is appended to the WAL before any shard is touched, so a
// WAL failure leaves the shard state untouched and consumes no Seq.
func (g *Ledger) Transfer(from, to int, amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if from == to {
		return ErrSameShard
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	n := g.shards.Len()
	if from < 0 || from >= n || to < 0 || to >= n {
		return ErrShardRange
	}
	if g.shards.Balance(from) < amount {
		return ErrInsufficient
	}
	txn := g.nextTxn + 1
	recs := []wal.Record{
		{Txn: txn, Shard: from, Delta: -amount},
		{Txn: txn, Shard: to, Delta: amount},
	}
	if err := g.log.Append(recs...); err != nil {
		return err // nothing applied, no Seq consumed
	}
	g.nextTxn = txn // txn id is burned once its records are durable
	if g.trip(CrashAfterAppend) {
		return ErrCrashed
	}
	g.shards.Apply(recs[0])
	if g.trip(CrashAfterDebit) {
		return ErrCrashed
	}
	g.shards.Apply(recs[1])
	if g.trip(CrashBeforeMeta) {
		return ErrCrashed
	}
	g.meta = txn
	return nil
}

// trip reports whether the armed crash point matches p, disarming it.
func (g *Ledger) trip(p CrashPoint) bool {
	if g.crash == p {
		g.crash = CrashNone
		return true
	}
	return false
}
