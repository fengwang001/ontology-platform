package overagg

import "sort"

// bucket aggregates all retained released rows of one key at one timestamp.
type bucket struct {
	ts      int64
	sum     int64
	cnt     int64
	max     int64
	inWin   bool
	heapVer int
}

// keyState holds the per-key released rows as timestamp-ordered buckets plus
// an incremental sliding-window accumulator used by Advance frames.
type keyState struct {
	buckets []*bucket
	byTs    map[int64]*bucket

	lo    int // window covers buckets[lo:hi]
	hi    int
	hasHi bool
	curHi int64

	winSum int64
	winCnt int64
	heap   []heapEntry
}

// heapEntry is one lazy max-heap entry; stale entries are dropped on query.
type heapEntry struct {
	val int64
	b   *bucket
	ver int
}

func newKeyState() *keyState {
	return &keyState{byTs: make(map[int64]*bucket)}
}

// lowerBound returns the first bucket index with ts >= target.
func (ks *keyState) lowerBound(target int64) int {
	return sort.Search(len(ks.buckets), func(i int) bool {
		return ks.buckets[i].ts >= target
	})
}

// addRow merges one released row (advance-released or reissued) into the
// bucket structure and, when its timestamp falls inside the current
// accumulator window, into the window aggregates as well.
func (ks *keyState) addRow(a *Aggregator, ts, val int64) {
	if b := ks.byTs[ts]; b != nil {
		b.sum += val
		b.cnt++
		if b.inWin {
			ks.winSum += val
			ks.winCnt++
			a.frameWork++ // row enters the accumulator
			if val > b.max {
				b.max = val
				b.heapVer++
				ks.heapPush(a, b)
			}
		} else if val > b.max {
			b.max = val
		}
		return
	}
	nb := &bucket{ts: ts, sum: val, cnt: 1, max: val}
	pos := ks.lowerBound(ts)
	ks.buckets = append(ks.buckets, nil)
	copy(ks.buckets[pos+1:], ks.buckets[pos:])
	ks.buckets[pos] = nb
	ks.byTs[ts] = nb
	switch {
	case pos < ks.lo:
		// Below the window lower bound: future frames never reach it.
		ks.lo++
		ks.hi++
	case pos < ks.hi:
		ks.hi++
		ks.enterWindow(a, nb)
	default:
		// Beyond the window high end: picked up by a later advanceWindow.
	}
}

// enterWindow adds one bucket to the accumulator window.
func (ks *keyState) enterWindow(a *Aggregator, b *bucket) {
	b.inWin = true
	ks.winSum += b.sum
	ks.winCnt += b.cnt
	a.frameWork += b.cnt // rows enter the accumulator
	b.heapVer++
	ks.heapPush(a, b)
}

// leaveWindow removes one bucket from the accumulator window.
func (ks *keyState) leaveWindow(a *Aggregator, b *bucket) {
	b.inWin = false
	ks.winSum -= b.sum
	ks.winCnt -= b.cnt
	a.frameWork += b.cnt // rows leave the accumulator
}

// advanceWindow moves the accumulator window to cover [t-R, t].
func (ks *keyState) advanceWindow(a *Aggregator, t int64) {
	loBound := t - a.r
	for ks.lo < ks.hi && ks.buckets[ks.lo].ts < loBound {
		ks.leaveWindow(a, ks.buckets[ks.lo])
		ks.lo++
	}
	if ks.lo == ks.hi {
		// The window is empty: skip out-of-window buckets that sit below
		// the new lower bound; no future frame can ever reach them.
		for ks.hi < len(ks.buckets) && ks.buckets[ks.hi].ts < loBound {
			ks.hi++
		}
		ks.lo = ks.hi
	}
	for ks.hi < len(ks.buckets) && ks.buckets[ks.hi].ts <= t {
		ks.enterWindow(a, ks.buckets[ks.hi])
		ks.hi++
	}
	ks.curHi = t
	ks.hasHi = true
}

// frameQuery scans the buckets inside [ts-R, ts] for a reissued row.
// lateWork counts the distinct timestamp buckets examined by the scan,
// including the first out-of-frame bucket that terminates it.
func (ks *keyState) frameQuery(a *Aggregator, ts int64) (sum, cnt, max int64, ok bool) {
	lo := ts - a.r
	for i := ks.lowerBound(lo); i < len(ks.buckets); i++ {
		b := ks.buckets[i]
		a.lateWork++
		if b.ts > ts {
			break
		}
		sum += b.sum
		cnt += b.cnt
		if !ok || b.max > max {
			max = b.max
		}
		ok = true
	}
	return sum, cnt, max, ok
}

// cleanup drops every bucket with ts <= limit and fixes the window bounds.
func (ks *keyState) cleanup(a *Aggregator, limit int64) {
	k := sort.Search(len(ks.buckets), func(i int) bool {
		return ks.buckets[i].ts > limit
	})
	if k == 0 {
		return
	}
	for _, b := range ks.buckets[:k] {
		if b.inWin {
			ks.leaveWindow(a, b)
		}
		delete(ks.byTs, b.ts)
		a.retained -= b.cnt
	}
	ks.buckets = append(ks.buckets[:0], ks.buckets[k:]...)
	ks.lo -= k
	if ks.lo < 0 {
		ks.lo = 0
	}
	ks.hi -= k
	if ks.hi < 0 {
		ks.hi = 0
	}
}

// maxQuery returns the maximum value inside the accumulator window,
// lazily discarding stale heap entries. The window must be non-empty.
func (ks *keyState) maxQuery(a *Aggregator) int64 {
	for {
		top := ks.heap[0]
		if !top.b.inWin || top.ver != top.b.heapVer {
			ks.heapPop(a)
			continue
		}
		return top.val
	}
}

func (ks *keyState) heapPush(a *Aggregator, b *bucket) {
	a.frameWork++ // monotone-structure enqueue
	ks.heap = append(ks.heap, heapEntry{val: b.max, b: b, ver: b.heapVer})
	for i := len(ks.heap) - 1; i > 0; {
		p := (i - 1) / 2
		if ks.heap[p].val >= ks.heap[i].val {
			break
		}
		ks.heap[p], ks.heap[i] = ks.heap[i], ks.heap[p]
		i = p
	}
}

func (ks *keyState) heapPop(a *Aggregator) {
	a.frameWork++ // monotone-structure dequeue
	n := len(ks.heap) - 1
	ks.heap[0] = ks.heap[n]
	ks.heap = ks.heap[:n]
	for i := 0; ; {
		largest := i
		if l := 2*i + 1; l < n && ks.heap[l].val > ks.heap[largest].val {
			largest = l
		}
		if r := 2*i + 2; r < n && ks.heap[r].val > ks.heap[largest].val {
			largest = r
		}
		if largest == i {
			return
		}
		ks.heap[i], ks.heap[largest] = ks.heap[largest], ks.heap[i]
		i = largest
	}
}
