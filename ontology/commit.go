package ontology

import (
	"fmt"
	"runtime"
	"sync"
)

// commitMu serializes the validate-and-switch critical section of commits.
// The internal duplicate-write check runs before taking it, so a malformed
// batch costs no global contention.
var commitMu sync.Mutex

// Commit attempts to apply one batch atomically.
//
// Fixed decision order:
//  1. duplicate write declarations inside the batch;
//  2. per-instance base-version conflicts;
//  3. per-item validation hooks;
//  4. cardinality on the batch's final state.
//
// Any failure rolls the whole batch back: no version, link or clock change is
// observable. The full decision plus state switch runs while holding the
// commit gate and the state mutex, so the effect is indistinguishable from
// executing batches in some serial order (strict serializability), and readers
// can never observe a partial batch.
func (p *Platform) Commit(b Batch) BatchResult {
	p.mu.Lock()
	p.stats.Submitted++
	p.mu.Unlock()

	res := BatchResult{BatchID: b.ID}

	// Stage 1 (pre-lock): duplicate declarations inside the batch. This check
	// depends only on the batch itself and must precede all other checks.
	opIndex := make(map[InstanceID]int, len(b.Ops))
	for i, op := range b.Ops {
		if prev, dup := opIndex[op.Instance]; dup {
			return p.fail(b, res, FailureDuplicateWrite,
				fmt.Sprintf("instance %s written twice (ops %d and %d)", op.Instance, prev, i), nil)
		}
		opIndex[op.Instance] = i
	}
	linkAdd := make(map[linkKey]bool, len(b.Links))
	for i, lop := range b.Links {
		k := normalizeLink(lop.Link, lop.A, lop.B)
		if add, dup := linkAdd[k]; dup && add != lop.Add {
			return p.fail(b, res, FailureDuplicateWrite,
				fmt.Sprintf("link %s(%s,%s) both added and removed (link op %d)", lop.Link, lop.A, lop.B, i), nil)
		}
		linkAdd[k] = lop.Add
	}

	for !commitMu.TryLock() {
		runtime.Gosched()
	}
	defer commitMu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	result := p.decideAndCommitLocked(b, opIndex)
	if result.OK {
		res.OK = true
		res.NewVersions = result.NewVersions
		res.CommitTick = result.CommitTick
		return res
	}
	return result
}

func (p *Platform) fail(b Batch, res BatchResult, class FailureClass, detail string, base map[string]State) BatchResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failLocked(b, res, class, detail, base)
}

func (p *Platform) failLocked(b Batch, res BatchResult, class FailureClass, detail string, base map[string]State) BatchResult {
	switch class {
	case FailureDuplicateWrite:
		p.stats.DuplicateRejected++
	case FailureVersionConflict:
		p.stats.ConflictRejected++
	case FailureHookRejected:
		p.stats.HookRejected++
	case FailureCardinality:
		p.stats.CardinalityReject++
	}
	p.recordJournal(JournalEntry{
		BatchID: b.ID, Batch: recordBatch(b), Base: base,
		OK: false, Failure: failureName(class), Detail: detail,
	})
	res.Failure = class
	res.Detail = detail
	return res
}

func within(n int, c Cardinality) bool {
	if n < c.Min {
		return false
	}
	if c.Max >= 0 && n > c.Max {
		return false
	}
	return true
}

func (c Cardinality) String() string {
	if c.Max < 0 {
		return fmt.Sprintf("%d..*", c.Min)
	}
	return fmt.Sprintf("%d..%d", c.Min, c.Max)
}

func normalizeLink(link TypeName, a, b InstanceID) linkKey {
	if b < a {
		a, b = b, a
	}
	return linkKey{link: link, a: a, b: b}
}
