// Package verify maintains the segment sums and global total of the
// change log, verifies ranges, and locates the first corrupt record.
package verify

import (
	"errors"
	"sync"

	"ontology/cksum"
)

var (
	ErrBadSegSize = errors.New("verify: segSize must be > 0")
	ErrSeqGap     = errors.New("verify: seq must equal previous seq + 1")
	ErrBadRange   = errors.New("verify: range outside [1, appended]")
)

type rec struct{ seq, val, e int64 }

// Engine holds the in-memory log state. Safe for concurrent use.
type Engine struct {
	mu      sync.Mutex
	segSize int64
	recs    []rec
	segs    []cksum.Seg
	total   int64
	lastCmp int64 // records recomputed and compared in the last Verify
}

// New returns an Engine whose segments span segSize records each.
func New(segSize int) (*Engine, error) {
	if segSize <= 0 {
		return nil, ErrBadSegSize
	}
	return &Engine{segSize: int64(segSize)}, nil
}

// Append stores (seq, val); seq must equal the previous seq + 1 (the
// first record has seq 1). On error nothing changes.
func (e *Engine) Append(seq, val int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if seq != int64(len(e.recs))+1 {
		return ErrSeqGap
	}
	el := cksum.Elem(seq, val)
	e.recs = append(e.recs, rec{seq, val, el})
	if k := (seq - 1) / e.segSize; int(k) == len(e.segs) {
		e.segs = append(e.segs, cksum.Seg{})
	}
	e.segs[(seq-1)/e.segSize].Add(el)
	e.total += el
	return nil
}

// Verify scans the closed range [from, to] ascending and returns the
// seq of the first record whose stored checksum mismatches, else ok.
func (e *Engine) Verify(from, to int64) (corrupt int64, ok bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if from < 1 || from > to || to > int64(len(e.recs)) {
		return 0, false, ErrBadRange
	}
	var n int64
	for i := from - 1; i < to; i++ {
		n++
		if r := e.recs[i]; cksum.Elem(r.seq, r.val) != r.e {
			e.lastCmp = n
			return r.seq, false, nil
		}
	}
	e.lastCmp = n
	return 0, true, nil
}

// Recompute rebuilds the sums of the segments covering [from, to] from
// the records and realigns the total with the segment sums.
func (e *Engine) Recompute(from, to int64) (segSums []int64, total int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if from < 1 || from > to || to > int64(len(e.recs)) {
		return nil, 0, ErrBadRange
	}
	for k := (from - 1) / e.segSize; k <= (to-1)/e.segSize; k++ {
		var sum int64
		for i := k * e.segSize; i < int64(len(e.recs)) && i < (k+1)*e.segSize; i++ {
			sum += cksum.Elem(e.recs[i].seq, e.recs[i].val)
		}
		e.segs[k].Set(sum)
		segSums = append(segSums, sum)
	}
	for k := range e.segs {
		total += e.segs[k].Sum()
	}
	e.total = total
	return segSums, total, nil
}

// Total returns the global checksum total.
func (e *Engine) Total() int64 { e.mu.Lock(); defer e.mu.Unlock(); return e.total }

// SegSum returns the sum of 1-based segment k, or 0 if out of range.
func (e *Engine) SegSum(k int) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if k < 1 || k > len(e.segs) {
		return 0
	}
	return e.segs[k-1].Sum()
}

// Report summarizes SelfCheck: the 8-step table of NOTES.md, the trap
// values, and the invariant and probe verdicts.
type Report struct {
	Seg1, Seg2, Tot     [8]int64 // per-step segment sums and total
	First, HalfMiss     int64    // first corrupt ascending; corrupt a [from,to) scan misses
	Desc                int64    // "first" corrupt a descending scan reports
	WrongTot, WrongSeg2 int64    // total and segment-2 sum if e were Seq+Val
	HalfSaysClean       bool     // half-open scan wrongly reports clean
	Inv                 [4]bool  // invariants 1..4
	ScaleOK, ConcOK     bool     // range-local cost; concurrent Verify agrees
}

// SelfCheck runs the built-in 6-record scenario of NOTES.md and checks
// the four invariants plus range-local cost and concurrent agreement.
func SelfCheck() (r Report) {
	eng, _ := New(3)
	for i := int64(1); i <= 6; i++ {
		_ = eng.Append(i, 10*i)
		r.Seg1[i-1], r.Seg2[i-1], r.Tot[i-1] = eng.SegSum(1), eng.SegSum(2), eng.Total()
	}
	eng.recs[3].val, eng.recs[4].val = 70, 80 // corrupt Seq 4 and 5
	sums, tot, _ := eng.Recompute(1, 6)
	r.Seg1[6], r.Seg2[6], r.Tot[6] = sums[0], sums[1], tot
	r.First, _, _ = eng.Verify(1, 6)
	r.Seg1[7], r.Seg2[7], r.Tot[7] = eng.SegSum(1), eng.SegSum(2), eng.Total()
	r.HalfMiss, r.HalfSaysClean = halfOpen()
	for i := int64(1); i <= 6; i++ {
		r.WrongTot += i + 10*i
		if i > 3 {
			r.WrongSeg2 += i + 10*i
		}
	}
	r.Desc = descFirst()
	r.Inv = [4]bool{naiveOK(), totalOK(), locateOK(), rejectOK()}
	r.ScaleOK, r.ConcOK = scaleOK(), concOK()
	return r
}
