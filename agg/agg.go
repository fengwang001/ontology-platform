// Package agg maintains the single-key aggregate: the distinct-Seq→Val
// set, the running sum, and the finite correction window ordered by first
// arrival time. It depends on no other package of the module.
package agg

import "sync"

// Kind classifies an event.
type Kind uint8

const (
	NewSeq Kind = iota + 1
	Late
	Correction
	Duplicate
	Stale
)

// Outcome describes one Apply call.
type Outcome struct {
	Kind    Kind
	OldSum  int64 // sum before the event
	NewSum  int64 // sum after the event
	Changed bool  // whether the sum moved
	Had     bool  // whether the key already held at least one Seq
	Stale   bool  // a correction of an already evicted Seq was rejected
}

// Aggregator is the state of one key. Use New; the zero value is not usable.
type Aggregator struct {
	mu sync.Mutex
	w  int

	vals map[int64]int64    // every distinct Seq ever seen -> current Val
	win  map[int64]struct{} // Seqs currently inside the correction window
	ring []int64            // first-arrival FIFO of window members, cap w
	head int                // index of the oldest member in ring
	size int

	sum    int64
	maxSeq int64
	staleN int64

	// probes counts the window entries inspected by the most recent Apply
	// when deciding whether a Seq is still in the correction window.
	// Unexported on purpose: membership is O(1) via win, never a scan.
	probes int
}

// New creates an Aggregator whose window holds w members.
func New(w int) *Aggregator {
	return &Aggregator{
		w:    w,
		vals: map[int64]int64{},
		win:  map[int64]struct{}{},
		ring: make([]int64, w),
	}
}

// Sum returns the current sum.
func (a *Aggregator) Sum() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sum
}

// Stale returns the number of rejected expired corrections.
func (a *Aggregator) Stale() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.staleN
}

// Window returns window members in first-arrival order.
func (a *Aggregator) Window() []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]int64, a.size)
	for i := range out {
		out[i] = a.ring[(a.head+i)%a.w]
	}
	return out
}

// Apply processes one event. Input validity (seq > 0) is the caller's job.
func (a *Aggregator) Apply(seq, val int64) Outcome {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.probes = 0
	old := a.sum
	o := Outcome{OldSum: old, NewSum: old, Had: len(a.vals) > 0}

	if cur, seen := a.vals[seq]; seen {
		if cur == val {
			o.Kind = Duplicate // idempotent: nothing is inspected or touched
			return o
		}
		a.probes = 1 // one map entry is inspected, independent of window size
		if _, in := a.win[seq]; !in {
			a.staleN++
			o.Kind, o.Stale = Stale, true
			return o
		}
		a.sum += val - cur
		a.vals[seq] = val // correction does not change first-arrival order
		o.Kind, o.Changed, o.NewSum = Correction, a.sum != old, a.sum
		return o
	}

	// A never-seen Seq is new data and is always accepted, even when late.
	late := a.maxSeq != 0 && seq < a.maxSeq
	a.vals[seq] = val
	a.sum += val
	if seq > a.maxSeq {
		a.maxSeq = seq
	}
	a.enqueue(seq)
	if late {
		o.Kind = Late
	} else {
		o.Kind = NewSeq
	}
	o.Changed, o.NewSum = a.sum != old, a.sum
	return o
}

// enqueue inserts a brand-new Seq into the first-arrival FIFO, evicting the
// oldest member when full. Eviction removes window membership only; the Val
// stays in vals and keeps contributing to sum.
func (a *Aggregator) enqueue(seq int64) {
	if a.size == a.w {
		delete(a.win, a.ring[a.head])
		a.head = (a.head + 1) % a.w
		a.size--
	}
	a.ring[(a.head+a.size)%a.w] = seq
	a.win[seq] = struct{}{}
	a.size++
}
