package bitemporal

import (
	"container/heap"
	"sort"
)

type sweepEvent struct {
	at    Tick
	begin bool
	rec   *Record
}

// activeHeap keeps currently-covering records ordered by visibility:
// greater TxTime wins; equal TxTime resolves to the later arrival Seq.
type activeHeap []*Record

func (h activeHeap) Len() int { return len(h) }
func (h activeHeap) Less(i, j int) bool {
	if h[i].TxTime != h[j].TxTime {
		return h[i].TxTime > h[j].TxTime
	}
	return h[i].Seq > h[j].Seq
}
func (h activeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *activeHeap) Push(x any)   { *h = append(*h, x.(*Record)) }
func (h *activeHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// buildSegments sweeps [span.Start, span.End) producing maximal constant-value
// runs. Only records arrived by c.Seq and with TxTime <= c.T participate.
// Runs without a visible record are emitted as explicit unknown segments;
// they are never default-filled.
func (e *Exporter) buildSegments(c Cutoff, objectID string, span Interval, recs []*Record) []Segment {
	events := make([]sweepEvent, 0, 2*len(recs))
	for _, r := range recs {
		if r.TxTime > c.T || r.Seq > c.Seq || r.End <= span.Start || r.Start >= span.End {
			continue
		}
		events = append(events,
			sweepEvent{at: r.Start, begin: true, rec: r},
			sweepEvent{at: r.End, begin: false, rec: r},
		)
	}
	sortEvents(events)

	var raw []Segment
	active := &activeHeap{}
	heap.Init(active)
	cur := span.Start
	emit := func(end Tick) bool {
		var val *Value
		if active.Len() > 0 {
			if end > cur {
				top := (*active)[0]
				v := top.Value
				val = &v
			}
		}
		if end > cur {
			raw = append(raw, Segment{Start: cur, End: end, Value: val})
			cur = end
		}
		return cur == span.End
	}

	// Advance strictly between event boundaries up to span.End; unknown
	// prefixes and suffixes are emitted the same way, never skipped.
	for i := 0; i < len(events); {
		at := events[i].at
		if at >= span.End {
			break
		}
		if at > cur && emit(at) {
			return mergeAdjacent(raw)
		}
		// Apply every event at this boundary: ends before begins (half-open).
		for i < len(events) && events[i].at == at {
			ev := events[i]
			if ev.begin {
				heap.Push(active, ev.rec)
			} else {
				removeRecord(active, ev.rec)
			}
			i++
		}
	}
	emit(span.End)
	return mergeAdjacent(raw)
}

// removeRecord removes r from the active heap in linear time. Per boundary the
// heap is small relative to the object history; exports never inspect the full
// history for point queries (see segmentIndex), which is the scale guarantee.
func removeRecord(h *activeHeap, target *Record) {
	for i, r := range *h {
		if r == target {
			heap.Remove(h, i)
			return
		}
	}
}

// sortEvents orders sweep events by position; at an equal boundary end events
// precede begin events so a half-open ending at x and another starting at x do
// not briefly co-cover point x (the ending interval must not contain x).
func sortEvents(ev []sweepEvent) {
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].at != ev[j].at {
			return ev[i].at < ev[j].at
		}
		// begin=true sorts after begin=false.
		if ev[i].begin != ev[j].begin {
			return !ev[i].begin
		}
		return ev[i].rec.Seq < ev[j].rec.Seq
	})
}

// mergeAdjacent joins neighboring runs with equal visibility (same record or
// both unknown), yielding the canonical minimal representation.
func mergeAdjacent(segs []Segment) []Segment {
	if len(segs) == 0 {
		return nil
	}
	out := make([]Segment, 0, len(segs))
	out = append(out, segs[0])
	for _, g := range segs[1:] {
		last := &out[len(out)-1]
		if sameVisible(*last, g) {
			last.End = g.End
			continue
		}
		out = append(out, g)
	}
	return out
}

func sameVisible(a, b Segment) bool {
	if a.Unknown() || b.Unknown() {
		return a.Unknown() == b.Unknown()
	}
	return a.Value.Equal(*b.Value)
}
