package fence

import "container/heap"

// platformEvent is one platform shrink event with a half-open interval
// [Start, End). gen is bumped by termination; snapshots carrying an older
// generation are lazy tombstones.
type platformEvent struct {
	ID         string
	Region     string
	Start      int64
	End        int64
	Level      int
	TermAt     int64
	Terminated bool
	gen        int
}

// liveEnd is the exclusive live boundary (truncated once terminated).
func (e *platformEvent) liveEnd() int64 {
	if e.Terminated {
		return e.TermAt
	}
	return e.End
}

// liveAt reports whether the (possibly truncated) interval covers t.
func (e *platformEvent) liveAt(t int64) bool {
	return e.Start <= t && t < e.liveEnd()
}

// activeEntry is an immutable snapshot inside a per-level min-heap. It is
// invalid once its generation is superseded by termination or once its live
// boundary has passed; both conditions are detectable at the heap top.
type activeEntry struct {
	end int64
	gen int
	ev  *platformEvent
}

func (e activeEntry) invalid(now int64) bool {
	return e.gen != e.ev.gen || e.end <= now
}

type startHeap []*platformEvent

func (h startHeap) Len() int           { return len(h) }
func (h startHeap) Less(i, j int) bool { return h[i].Start < h[j].Start }
func (h startHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *startHeap) Push(x any)        { *h = append(*h, x.(*platformEvent)) }
func (h *startHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// endMinHeap orders activeEntry snapshots by live end within one level.
type endMinHeap []activeEntry

func (h endMinHeap) Len() int           { return len(h) }
func (h endMinHeap) Less(i, j int) bool { return h[i].end < h[j].end }
func (h endMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *endMinHeap) Push(x any)        { *h = append(*h, x.(activeEntry)) }
func (h *endMinHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// endDesc is an immutable boundary descriptor for the recovery scan.
type endDesc struct {
	end int64
	gen int
	ev  *platformEvent
}

// stale reports a superseded generation. For a promoted event that is later
// terminated, its generation is bumped, so the original-boundary descriptor
// is detected this way. For an event terminated *before promotion*, the copy
// in the ends heap was published by the primary index at promotion with the
// then-current generation (equal to ev.gen), but its end is the truncated
// boundary; there is no older descriptor for it in a fresh view.
func (d endDesc) stale() bool {
	if d.gen != d.ev.gen {
		return true
	}
	if d.ev.Terminated && d.end != d.ev.TermAt {
		return true
	}
	return false
}

type endDescHeap []endDesc

func (h endDescHeap) Len() int           { return len(h) }
func (h endDescHeap) Less(i, j int) bool { return h[i].end < h[j].end }
func (h endDescHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *endDescHeap) Push(x any)        { *h = append(*h, x.(endDesc)) }
func (h *endDescHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// regionState indexes events of one region.
//
//   - starts holds events whose Start has not arrived.
//   - buckets[level] holds promoted events of one level as a min-heap of live
//     ends; a level contributes iff its earliest valid end is still live.
//     Min-heap order makes every expired/superseded entry safe to discard,
//     regardless of the levels of other events.
//   - ends holds all live boundaries chronologically for the recovery query.
//
// Every entry is pushed once and popped once (termination adds at most one
// more pair). Read work is therefore independent of the number of already
// finished historical events.
type regionState struct {
	starts   startHeap
	buckets  map[int]*endMinHeap
	ends     endDescHeap
	advanced int64
}

// regionView is a cheap, independent copy used by read-ahead scans (the
// next-recovery query). It shares the immutable event pointers but owns
// copies of every heap, so advancing its clock never mutates live state.
type regionView struct {
	starts  startHeap
	buckets map[int]*endMinHeap
	ends    endDescHeap
}

func (s *regionState) view() *regionView {
	v := &regionView{
		starts:  append(startHeap(nil), s.starts...),
		buckets: make(map[int]*endMinHeap, len(s.buckets)),
		ends:    append(endDescHeap(nil), s.ends...),
	}
	for level, b := range s.buckets {
		cp := append(endMinHeap(nil), (*b)...)
		v.buckets[level] = &cp
	}
	heap.Init(&v.starts)
	heap.Init(&v.ends)
	for _, b := range v.buckets {
		heap.Init(b)
	}
	return v
}

func (v *regionView) bucket(level int) *endMinHeap {
	b, ok := v.buckets[level]
	if !ok {
		b = &endMinHeap{}
		v.buckets[level] = b
	}
	return b
}

func (v *regionView) ensure(now int64) {
	for v.starts.Len() > 0 && v.starts[0].Start <= now {
		ev := heap.Pop(&v.starts).(*platformEvent)
		entry := activeEntry{end: ev.liveEnd(), gen: ev.gen, ev: ev}
		heap.Push(v.bucket(ev.Level), entry)
		heap.Push(&v.ends, endDesc{end: ev.liveEnd(), gen: ev.gen, ev: ev})
	}
}

func (v *regionView) levelAt(t int64) int {
	v.ensure(t)
	best := 0
	for level, b := range v.buckets {
		for b.Len() > 0 && (*b)[0].invalid(t) {
			heap.Pop(b)
		}
		if b.Len() > 0 && level > best {
			best = level
		}
	}
	return best
}

func (v *regionView) nextEnd(now int64) (int64, bool) {
	v.ensure(now)
	for v.ends.Len() > 0 {
		d := v.ends[0]
		if d.stale() || d.end <= now {
			heap.Pop(&v.ends)
			continue
		}
		return d.end, true
	}
	return 0, false
}

func newRegionState() *regionState {
	return &regionState{buckets: make(map[int]*endMinHeap), advanced: -1}
}

func (s *regionState) bucket(level int) *endMinHeap {
	b, ok := s.buckets[level]
	if !ok {
		b = &endMinHeap{}
		s.buckets[level] = b
	}
	return b
}

// addEvent queues a fresh event and promotes up to the observed time.
func (s *regionState) addEvent(ev *platformEvent, now int64) {
	// Promote immediately when the interval has already started; otherwise
	// queue for later. ensure is a no-op once `now` has been observed, so an
	// already-due event cannot rely on it for promotion.
	if ev.Start <= now {
		ev.gen = 1
		heap.Push(s.bucket(ev.Level), activeEntry{end: ev.liveEnd(), gen: 1, ev: ev})
		heap.Push(&s.ends, endDesc{end: ev.liveEnd(), gen: 1, ev: ev})
		if now > s.advanced {
			s.ensure(now)
		}
	} else {
		heap.Push(&s.starts, ev)
		if now > s.advanced {
			s.ensure(now)
		}
	}
}

// terminate truncates the live window at at (half-open: the event is gone at
// at). The current generation is invalidated. For a promoted event the
// truncated boundary is published to the end heap so recovery scans before
// `at` can see it; the stale bucket entry is pruned on read.
func (s *regionState) terminate(ev *platformEvent, now, at int64) {
	s.ensure(now)
	ev.Terminated = true
	ev.TermAt = at
	ev.gen++
	if ev.Start <= s.advanced {
		heap.Push(&s.ends, endDesc{end: at, gen: ev.gen, ev: ev})
	}
}

// ensure monotonically promotes due events and publishes their snapshots.
func (s *regionState) ensure(now int64) {
	if now < 0 || now <= s.advanced {
		return
	}
	for s.starts.Len() > 0 && s.starts[0].Start <= now {
		ev := heap.Pop(&s.starts).(*platformEvent)
		// gen==0: ordinary promotion. gen>=1 means the event was terminated
		// before its start; the truncated boundary is liveEnd(). Publish it
		// only if that boundary is still ahead of now (otherwise it is born
		// expired and must not occupy a level bucket).
		if ev.gen == 0 {
			ev.gen = 1
			entry := activeEntry{end: ev.liveEnd(), gen: 1, ev: ev}
			heap.Push(s.bucket(ev.Level), entry)
			heap.Push(&s.ends, endDesc{end: ev.liveEnd(), gen: 1, ev: ev})
		} else if ev.liveEnd() > now {
			entry := activeEntry{end: ev.liveEnd(), gen: ev.gen, ev: ev}
			heap.Push(s.bucket(ev.Level), entry)
			heap.Push(&s.ends, endDesc{end: ev.liveEnd(), gen: ev.gen, ev: ev})
		}
	}
	for s.ends.Len() > 0 {
		d := s.ends[0]
		// Only boundaries that have passed are pruned here. A stale
		// descriptor from a *retroactive* termination may still describe a
		// future boundary that an already-open recovery view must see, so
		// staleness alone does not trigger primary-state pruning; the view
		// itself handles stale entries during its own scan.
		if d.end <= now {
			heap.Pop(&s.ends)
			continue
		}
		break
	}
	s.advanced = now
}

// platformLevelAt returns the max level among events live at t. Bucket tops
// are pruned lazily; finished events have already left their buckets.
func (s *regionState) platformLevelAt(t int64) int {
	s.ensure(t)
	best := 0
	for level, b := range s.buckets {
		for b.Len() > 0 && (*b)[0].invalid(t) {
			heap.Pop(b)
		}
		if b.Len() > 0 && level > best {
			best = level
		}
	}
	return best
}

// nextEnd returns the earliest boundary strictly after now, consuming stale
// descriptors and boundaries at or before now.
func (s *regionState) nextEnd(now int64) (int64, bool) {
	s.ensure(now)
	for s.ends.Len() > 0 {
		d := s.ends[0]
		if d.stale() || d.end <= now {
			heap.Pop(&s.ends)
			continue
		}
		return d.end, true
	}
	return 0, false
}

type shrinkEngine struct {
	regions map[string]*regionState
}

func newShrinkEngine() *shrinkEngine {
	return &shrinkEngine{regions: make(map[string]*regionState)}
}

func (e *shrinkEngine) region(id string) *regionState {
	s, ok := e.regions[id]
	if !ok {
		s = newRegionState()
		e.regions[id] = s
	}
	return s
}
