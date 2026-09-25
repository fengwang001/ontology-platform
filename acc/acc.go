// Package acc holds the multi-key accumulator: durable sum and
// checkpoint cp, volatile pending buffer, contiguous-prefix Commit
// and crash-simulating Restore. Depends only on ckpt.
package acc

import (
	"errors"
	"sort"
	"sync"

	"ontology/ckpt"
)

// ErrPendingFull is returned when a new offset would push the pending
// buffer past maxPending. The apply leaves no trace.
var ErrPendingFull = errors.New("acc: pending buffer full")

// Record is one inflight delivery.
type Record struct {
	Key   string
	Delta int64
}

// Acc is the accumulator. sum and cp are durable (survive Restore);
// pending is volatile (dropped by Restore).
type Acc struct {
	mu         sync.RWMutex
	maxPending int
	sum        map[string]int64 // durable
	cp         int64            // durable
	pending    map[int64]Record // volatile
	probes     int              // pending entries checked by last Apply dedup
}

// New returns an empty accumulator with checkpoint -1.
func New(maxPending int) *Acc {
	return &Acc{
		maxPending: maxPending,
		sum:        map[string]int64{},
		cp:         -1,
		pending:    map[int64]Record{},
	}
}

// Apply buffers one record. Offsets already persisted (<= cp) or
// already inflight are idempotent no-ops. A brand-new offset that
// would exceed maxPending fails wholesale with ErrPendingFull and
// changes nothing.
func (a *Acc) Apply(key string, offset, delta int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probes = 0
	if offset > a.cp {
		a.probes = 1 // one map lookup, independent of len(pending)
	}
	switch ckpt.Decide(offset, a.cp, offset > a.cp && a.has(offset)) {
	case ckpt.Persisted, ckpt.Inflight:
		return nil
	}
	if len(a.pending) >= a.maxPending {
		return ErrPendingFull
	}
	a.pending[offset] = Record{Key: key, Delta: delta}
	return nil
}

func (a *Acc) has(offset int64) bool {
	_, ok := a.pending[offset]
	return ok
}

// Commit folds the contiguous prefix from cp+1 into sum and advances
// cp. It stops at the first gap; cp never skips a hole.
func (a *Acc) Commit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	ncp := ckpt.Advance(a.cp, a.has)
	for o := a.cp + 1; o <= ncp; o++ {
		r := a.pending[o]
		a.sum[r.Key] += r.Delta
		delete(a.pending, o)
	}
	a.cp = ncp
}

// Restore simulates a crash: volatile pending is lost, durable sum
// and cp are kept.
func (a *Acc) Restore() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = map[int64]Record{}
}

// Checkpoint returns the committed checkpoint.
func (a *Acc) Checkpoint() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cp
}

// Sum returns the committed sum for key.
func (a *Acc) Sum(key string) int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sum[key]
}

// Pending returns the inflight offsets, sorted.
func (a *Acc) Pending() []int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]int64, 0, len(a.pending))
	for o := range a.pending {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
