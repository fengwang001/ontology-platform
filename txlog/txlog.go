// Package txlog owns appends, record-to-txn attribution, HW advancement and LSO.
package txlog

import (
	"container/heap"
	"errors"
	"sync"
)

type Kind int

const KindData, KindCommit, KindAbort Kind = 0, 1, 2

type Record struct {
	Kind Kind
	PID  int
	Val  string
}

var (
	ErrInvalidRecord       = errors.New("txlog: pid must be a positive integer")
	ErrNoActiveTransaction = errors.New("txlog: producer has no active transaction")
	ErrHWOutOfRange        = errors.New("txlog: high-water mark out of range")
)

type txn struct {
	pid, first, end, idx int // idx: position in pend, -1 when absent
	committed            bool
}

type Log struct {
	mu         sync.Mutex
	recs       []Record
	own        []*txn       // txn owning each offset, markers included
	active     map[int]*txn // open txn per producer
	ord, ended []*txn       // ordered by first / by marker offset
	io, ie     int          // cursors: txns already in the HW window
	pend       pendHeap     // unresolved txns, smallest first on top
	hw, lso    int
	// checked counts txns inspected by the last AdvanceHW for the LSO;
	// unexported so no public/test path can read it.
	checked int
}

// New returns an empty log with HW = LSO = 0.
func New() *Log { return &Log{active: map[int]*txn{}} }

// AppendData appends Data(pid, val), opening a new transaction when needed.
func (l *Log) AppendData(pid int, val string) (int, error) {
	if pid <= 0 {
		return 0, ErrInvalidRecord
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t, off := l.active[pid], len(l.recs)
	if t == nil {
		t = &txn{pid: pid, first: off, end: -1, idx: -1}
		l.active[pid], l.ord = t, append(l.ord, t)
	}
	l.recs = append(l.recs, Record{KindData, pid, val})
	l.own = append(l.own, t)
	return off, nil
}

// marker appends Commit/Abort(pid).
func (l *Log) marker(pid int, k Kind) (int, error) {
	if pid <= 0 {
		return 0, ErrInvalidRecord
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.active[pid]
	if t == nil {
		return 0, ErrNoActiveTransaction
	}
	off := len(l.recs)
	t.end, t.committed = off, k == KindCommit
	delete(l.active, pid)
	l.recs = append(l.recs, Record{Kind: k, PID: pid})
	l.own, l.ended = append(l.own, t), append(l.ended, t)
	return off, nil
}

func (l *Log) AppendCommit(pid int) (int, error) { return l.marker(pid, KindCommit) }
func (l *Log) AppendAbort(pid int) (int, error)  { return l.marker(pid, KindAbort) }

// AdvanceHW: h == HW is a legal no-op; only txns whose window membership
// changes are inspected, so LSO work is independent of the total txn count.
func (l *Log) AdvanceHW(h int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h < l.hw || h > len(l.recs) { // range check before any mutation
		return ErrHWOutOfRange
	}
	l.checked = 0
	for l.ie < len(l.ended) && l.ended[l.ie].end < h { // markers settle txns
		t := l.ended[l.ie]
		l.ie, l.checked = l.ie+1, l.checked+1
		if t.idx >= 0 {
			heap.Remove(&l.pend, t.idx) // -1: never entered the window unresolved
		}
	}
	for l.io < len(l.ord) && l.ord[l.io].first < h { // first offsets enter
		t := l.ord[l.io]
		l.io, l.checked = l.io+1, l.checked+1
		if t.end == -1 || t.end >= h { // still open at the new HW: unresolved
			heap.Push(&l.pend, t)
		}
	}
	l.hw = h
	if len(l.pend) > 0 {
		l.lso, l.checked = l.pend[0].first, l.checked+1
	} else {
		l.lso = h
	}
	return nil
}

func (l *Log) HW() int  { l.mu.Lock(); defer l.mu.Unlock(); return l.hw }
func (l *Log) LSO() int { l.mu.Lock(); defer l.mu.Unlock(); return l.lso }

type Snapshot struct {
	l       *Log
	HW, LSO int
}

func (l *Log) OpenSnapshot() *Snapshot { l.mu.Lock(); return &Snapshot{l, l.hw, l.lso} }
func (s *Snapshot) Close()             { s.l.mu.Unlock() }

// At returns record i and whether its txn is committed with marker < HW.
func (s *Snapshot) At(i int) (Record, bool) {
	t := s.l.own[i]
	return s.l.recs[i], t.end >= 0 && t.end < s.HW && t.committed
}

type pendHeap []*txn

func (h pendHeap) Len() int           { return len(h) }
func (h pendHeap) Less(i, j int) bool { return h[i].first < h[j].first }
func (h pendHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx, h[j].idx = i, j
}
func (h *pendHeap) Push(x any) { t := x.(*txn); t.idx = len(*h); *h = append(*h, t) }
func (h *pendHeap) Pop() any {
	t := (*h)[len(*h)-1]
	*h, t.idx = (*h)[:len(*h)-1], -1
	return t
}
