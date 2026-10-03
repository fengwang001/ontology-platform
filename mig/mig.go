// Package mig performs an online migration of an ordered store from the old
// key encoding E1 (store A) to the new order-preserving encoding E2 (store B),
// driven by a single logical watermark w.
package mig

import (
	"sync"

	"ontology/keyenc"
	"ontology/store"
)

// finishWater is the watermark after Finish: every key routes to B.
const finishWater = keyenc.MaxKey + 1

// Hybrid routes logical keys by a watermark w between A (old encoding E1)
// and B (new encoding E2): k < w lives on B, k >= w lives on A.
//
// The single mutex serializes every operation (a whole Step is one critical
// section), making concurrent calls equivalent to some serial order.
type Hybrid struct {
	mu      sync.Mutex
	a       *store.Store
	b       *store.Store
	w       int64
	crashed bool

	// scanPhysicalOut counts physical records produced by all Scan calls.
	scanPhysicalOut uint64
	// scanLogicalOut is the total logical record count returned by all Scans.
	scanLogicalOut int
	// lastScanOut is the logical length returned by the most recent Scan.
	lastScanOut int
	// stepMoved is the number of keys migrated by the most recent Step.
	stepMoved int
	// stepProbed is the number of A entries examined by the most recent Step;
	// it must satisfy stepProbed <= 2*stepMoved regardless of |A|.
	stepProbed int
}

// NewHybrid creates a hybrid store at watermark MinKey.
func NewHybrid() *Hybrid {
	return &Hybrid{
		a: store.New("A-E1"),
		b: store.New("B-E2"),
		w: keyenc.MinKey,
	}
}

// Watermark returns the current watermark (safe for test inspection).
func (h *Hybrid) Watermark() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.w
}

// Crashed reports whether the store is in the crashed state.
func (h *Hybrid) Crashed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.crashed
}

// counters is for same-package tests: cumulative physical records produced by
// Scans, cumulative and last returned logical lengths, and the last Step's
// migrated/probed counts.
func (h *Hybrid) counters() (physOut, logicalOut, lastLogical, stepMoved, stepProbed int) {
	return int(h.scanPhysicalOut), h.scanLogicalOut, h.lastScanOut, h.stepMoved, h.stepProbed
}

// countersStep returns the last Step's (moved, probed) counts. Test-only.
func (h *Hybrid) countersStep() (moved, probed int, w int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stepMoved, h.stepProbed, h.w
}

// snapshotCounts returns physical residue counts relative to w: B keys >= w
// and A keys < w. After Recover both must be zero. Test-only.
func (h *Hybrid) snapshotCounts() (bResidue, aResidue int, w int64, crashed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.w <= keyenc.MaxKey {
		for _, r := range keyenc.E2Ranges(h.w, keyenc.MaxKey+1) {
			bResidue += len(h.b.Scan(r.Low, r.High, nil))
		}
	}
	if h.w > keyenc.MinKey {
		for _, r := range keyenc.E1Ranges(keyenc.MinKey, h.w) {
			aResidue += len(h.a.Scan(r.Low, r.High, nil))
		}
	}
	return bResidue, aResidue, h.w, h.crashed
}

func validKey(k int64) bool { return k >= keyenc.MinKey && k <= keyenc.MaxKey }

// Put routes k by the watermark and overwrites any existing value.
func (h *Hybrid) Put(k, v int64) error {
	if !validKey(k) || v < -1_000_000_000 || v > 1_000_000_000 {
		return ErrInvalidArg
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.crashed {
		return ErrCrashed
	}
	if k < h.w {
		h.b.Put(keyenc.Encode2(k), v)
	} else {
		h.a.Put(keyenc.Encode1(k), v)
	}
	return nil
}

// Delete removes k on the routed side and reports whether it existed.
func (h *Hybrid) Delete(k int64) (bool, error) {
	if !validKey(k) {
		return false, ErrInvalidArg
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.crashed {
		return false, ErrCrashed
	}
	if k < h.w {
		return h.b.Delete(keyenc.Encode2(k)), nil
	}
	return h.a.Delete(keyenc.Encode1(k)), nil
}

// Get returns (value, ok) on the routed side. Get also works while crashed:
// watermark filtering hides both kinds of migration residues.
func (h *Hybrid) Get(k int64) (int64, bool, error) {
	if !validKey(k) {
		return 0, false, ErrInvalidArg
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if k < h.w {
		v, ok := h.b.Get(keyenc.Encode2(k))
		return v, ok, nil
	}
	v, ok := h.a.Get(keyenc.Encode1(k))
	return v, ok, nil
}

// smallestA returns up to limit smallest logical keys of A, in logical
// ascending order, scanning the negative E1 half before the non-negative half.
// Each examined physical record is counted in probed.
func (h *Hybrid) smallestA(limit int) []store.Entry {
	if limit <= 0 {
		return nil
	}
	out := make([]store.Entry, 0, limit)
	for _, r := range keyenc.E1Ranges(keyenc.MinKey, keyenc.MaxKey+1) {
		need := limit - len(out)
		var probed uint64
		out = append(out, h.a.ScanN(r.Low, r.High, need, &probed)...)
		h.stepProbed += int(probed)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Step migrates at most n smallest logical keys from A to B, in logical
// ascending order. For each key: write B, advance w to k+1, delete A.
func (h *Hybrid) Step(n int) (int, error) {
	if n < 1 || n > 10_000 {
		return 0, ErrInvalidArg
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.crashed {
		return 0, ErrCrashed
	}
	h.stepMoved = 0
	h.stepProbed = 0
	entries := h.smallestA(n)
	for _, e := range entries {
		k := keyenc.Decode1(e.Key)
		h.b.Put(keyenc.Encode2(k), e.Value) // 1) write B
		h.w = k + 1                         // 2) watermark commit point
		h.a.Delete(e.Key)                   // 3) delete A
		h.stepMoved++
	}
	return h.stepMoved, nil
}

// StepPartial performs the first p migration steps on A's smallest key, then
// enters the crashed state: p=1 only writes B (rollback residue on B);
// p=2 also advances w (roll-forward residue on A).
func (h *Hybrid) StepPartial(p int) error {
	if p != 1 && p != 2 {
		return ErrInvalidArg
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.crashed {
		return ErrCrashed
	}
	h.stepMoved = 0
	h.stepProbed = 0
	entries := h.smallestA(1)
	if len(entries) == 0 {
		// Empty A is not a crash: state and watermark stay untouched.
		return ErrDrained
	}
	e := entries[0]
	k := keyenc.Decode1(e.Key)
	h.b.Put(keyenc.Encode2(k), e.Value)
	if p == 2 {
		h.w = k + 1
	}
	h.crashed = true
	return nil
}

// Finish requires A to be empty and advances the watermark past MaxKey, after
// which every read and write is served by B.
func (h *Hybrid) Finish() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.crashed {
		return ErrCrashed
	}
	if h.a.Len() != 0 {
		return ErrNotDrained
	}
	h.w = finishWater
	return nil
}

// Scan returns logical entries in [lo, hi) ascending by logical key: B's
// [lo, min(hi,w)) first, then A's [max(lo,w), hi) (negative E1 half before
// the non-negative half). Works while crashed; residues stay invisible.
func (h *Hybrid) Scan(lo, hi int64) ([]KV, error) {
	if lo < keyenc.MinKey || hi > keyenc.MaxKey+1 || lo > hi {
		return nil, ErrInvalidArg
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var produced uint64
	out := make([]KV, 0)

	bHi := hi
	if h.w < bHi {
		bHi = h.w
	}
	if lo < bHi {
		for _, r := range keyenc.E2Ranges(lo, bHi) {
			es := h.b.Scan(r.Low, r.High, &produced)
			for _, e := range es {
				out = append(out, KV{Key: keyenc.Decode2(e.Key), Value: e.Value})
			}
		}
	}

	aLo := lo
	if h.w > aLo {
		aLo = h.w
	}
	if aLo < hi {
		for _, r := range keyenc.E1Ranges(aLo, hi) {
			es := h.a.Scan(r.Low, r.High, &produced)
			for _, e := range es {
				out = append(out, KV{Key: keyenc.Decode1(e.Key), Value: e.Value})
			}
		}
	}

	h.scanPhysicalOut += produced
	h.lastScanOut = len(out)
	h.scanLogicalOut += len(out)
	return out, nil
}

// Recover leaves the crashed state by deleting residues: B keys >= w
// (p=1 rollback) and A keys < w (p=2 roll-forward). It returns
// (B deletions, A deletions).
func (h *Hybrid) Recover() (int, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.crashed {
		return 0, 0, ErrNotCrashed
	}
	bDeleted := 0
	if w := h.w; w <= keyenc.MaxKey {
		for _, r := range keyenc.E2Ranges(w, keyenc.MaxKey+1) {
			for _, e := range h.b.Scan(r.Low, r.High, nil) {
				if h.b.Delete(e.Key) {
					bDeleted++
				}
			}
		}
	}
	aDeleted := 0
	if h.w > keyenc.MinKey {
		for _, r := range keyenc.E1Ranges(keyenc.MinKey, h.w) {
			for _, e := range h.a.Scan(r.Low, r.High, nil) {
				if h.a.Delete(e.Key) {
					aDeleted++
				}
			}
		}
	}
	h.crashed = false
	return bDeleted, aDeleted, nil
}
