// Package align merges two event-time streams behind the aligned watermark
// W = min(wA, wB). Accepted events are buffered in a min-heap keyed by
// (TS, arrival order); after every feed all buffered events with TS <= W are
// released in non-decreasing TS order. It depends only on package wm.
package align

import (
	"container/heap"
	"math"

	"ontology/wm"
)

// PosInf is the watermark value applied to both streams at Close: no finite
// event TS can exceed it, so every buffered event becomes releasable.
const PosInf = int64(math.MaxInt64)

// Event is one stream record. Stream is 'A' or 'B' (validated by package api).
type Event struct {
	Stream byte
	TS     int64
}

type item struct {
	ev  Event
	seq int64 // global arrival order among accepted events
}

type minHeap []item

func (h minHeap) Len() int { return len(h) }
func (h minHeap) Less(i, j int) bool {
	if h[i].ev.TS != h[j].ev.TS {
		return h[i].ev.TS < h[j].ev.TS
	}
	return h[i].seq < h[j].seq
}
func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)   { *h = append(*h, x.(item)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// Aligner is the two-stream alignment state. Zero value is not ready; use New.
type Aligner struct {
	wm  [2]wm.Watermark // index 0 = A, 1 = B
	buf minHeap
	seq int64

	// cmpProbe counts buffered events examined as minimum candidates while
	// the most recent drain located the minimum-TS event. A heap exposes the
	// minimum at its root, so each decision examines one candidate; a linear
	// scan would examine len(buf) of them. Unexported on purpose.
	cmpProbe int
}

// New returns an empty aligner: both streams unseen, W = negative infinity.
func New() *Aligner {
	al := &Aligner{}
	heap.Init(&al.buf)
	return al
}

func idx(stream byte) int {
	if stream == 'B' {
		return 1
	}
	return 0
}

// AlignedW returns W = min(wA, wB). ok is false while either stream has not
// yet seen an event (W = negative infinity).
func (al *Aligner) AlignedW() (w int64, ok bool) {
	ta, oa := al.wm[0].Get()
	tb, ob := al.wm[1].Get()
	if !oa || !ob {
		return 0, false
	}
	if ta < tb {
		return ta, true
	}
	return tb, true
}

// Feed applies one caller-validated event. late is true when the event is
// strictly behind its own stream's watermark; it is then dropped without
// touching the buffer or watermarks. Otherwise it is accepted, buffered and
// every buffered event with TS <= new W is returned, smallest first.
func (al *Aligner) Feed(ev Event) (emitted []Event, late bool) {
	w := &al.wm[idx(ev.Stream)]
	if w.Late(ev.TS) {
		return nil, true
	}
	w.Advance(ev.TS)
	al.seq++
	heap.Push(&al.buf, item{ev: ev, seq: al.seq})
	return al.drain(), false
}

// drain releases buffered events with TS <= W in (TS, arrival) order. It
// resets cmpProbe and locates each minimum via the heap root directly.
func (al *Aligner) drain() []Event {
	al.cmpProbe = 0
	w, ok := al.AlignedW()
	if !ok {
		return nil
	}
	var out []Event
	for al.buf.Len() > 0 {
		min := al.locateMin() // heap root: the one minimum candidate examined
		al.cmpProbe++
		if min.ev.TS > w {
			break
		}
		out = append(out, heap.Pop(&al.buf).(item).ev)
	}
	return out
}

// locateMin returns the buffered item with smallest (TS, arrival order). In
// heap order it is the root, found without scanning the rest of the buffer.
func (al *Aligner) locateMin() item {
	return al.buf[0]
}

// Close pushes both watermarks to positive infinity, releases the whole
// buffer in non-decreasing order and leaves the buffer empty.
func (al *Aligner) Close() []Event {
	al.wm[0].Advance(PosInf)
	al.wm[1].Advance(PosInf)
	return al.drain()
}
