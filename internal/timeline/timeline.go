package timeline

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Kind classifies what plays at a queried instant.
type Kind int

const (
	KindFiller Kind = iota
	KindProgram
	KindOverride
)

// Segment kinds.
const (
	kindProgram = 0
	kindPreempt = 1
	kindShift   = 2
)

type seg struct {
	id    string
	start int64
	end   int64
	off   int64 // in-program offset at segment start (program segments only)
	fixed bool
	kind  int
}

// Result is the playout decision at one instant.
type Result struct {
	Kind   Kind
	ID     string
	Offset int64
}

// Timeline is the shared channel state.
type Timeline struct {
	mu        sync.RWMutex
	fillerLen int64
	maxNow    int64
	ov        []seg // override segments, sorted by start, pairwise disjoint
	blk       []seg // program segments and shift overrides, sorted, pairwise disjoint

	// instrumentation counters
	probes atomic.Int64
	moved  atomic.Int64
}

// New creates a timeline with filler material of length f seconds.
func New(f int64) *Timeline {
	return &Timeline{fillerLen: f}
}

// Schedule places a regular program segment.
func (t *Timeline) Schedule(now int64, id string, start, dur int64, fixed bool) error {
	if !validTime(now) || id == "" || !validTime(start) || !validDur(dur) || start+dur > 1_000_000_000_000 {
		return ErrBadArg
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if now < t.maxNow {
		return ErrClockBack
	}
	if t.hasID(id) {
		return ErrDuplicate
	}
	if start < now {
		return ErrPast
	}
	ns := seg{id: id, start: start, end: start + dur, fixed: fixed, kind: kindProgram}
	for _, s := range t.blk {
		if s.start < ns.end && ns.start < s.end {
			return ErrOverlap
		}
	}
	t.maxNow = now
	t.insertBlk(ns)
	return nil
}

// Override places a preemptive (preempt=true) or shifting (preempt=false) override.
func (t *Timeline) Override(now int64, id string, start, dur int64, preempt bool) error {
	if !validTime(now) || id == "" || !validTime(start) || !validDur(dur) || start+dur > 1_000_000_000_000 {
		return ErrBadArg
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if now < t.maxNow {
		return ErrClockBack
	}
	if t.hasID(id) {
		return ErrDuplicate
	}
	if start < now {
		return ErrPast
	}
	end := start + dur
	for _, s := range t.ov {
		if s.start < end && start < s.end {
			return ErrOverride
		}
	}
	if preempt {
		t.maxNow = now
		t.insertOv(seg{id: id, start: start, end: end, kind: kindPreempt})
		return nil
	}
	// shifting override: any override segment extending past s conflicts
	for _, s := range t.ov {
		if s.end > start {
			return ErrOverride
		}
	}
	for _, s := range t.blk {
		if s.kind == kindShift && s.end > start {
			return ErrOverride
		}
	}
	// s strictly inside a fixed program segment?
	for _, s := range t.blk {
		if s.kind == kindProgram && s.fixed && s.start <= start && start < s.end {
			return ErrInFixed
		}
	}

	orig := append([]seg(nil), t.blk...)
	i0 := sort.Search(len(orig), func(i int) bool { return orig[i].start >= start })
	work := append([]seg(nil), orig[:i0]...)
	remain := dur
	origPos := start
	movedCount := int64(0)
	// split a floating segment strictly containing s: its tail always moves by d
	if i0 > 0 && orig[i0-1].end > start {
		c := orig[i0-1] // fixed containment already rejected above
		work[len(work)-1] = seg{id: c.id, start: c.start, end: start, off: c.off, fixed: false, kind: kindProgram}
		work = append(work, seg{id: c.id, start: start + dur, end: c.end + dur, off: c.off + (start - c.start), fixed: false, kind: kindProgram})
		origPos = c.end
		movedCount++
	}
	// walk later content in original coordinates: an accumulated gap at least
	// remain absorbs the delay; otherwise the whole segment carries remain.
	k := i0
	for ; k < len(orig) && remain > 0; k++ {
		t.moved.Add(1) // one boundary inspection per original segment reached
		g := orig[k]
		if g.start-origPos >= remain {
			remain = 0
			break
		}
		if g.kind == kindProgram && g.fixed {
			// all-or-nothing: discard the working copy entirely
			return ErrCrowdFixed
		}
		g.start += remain
		g.end += remain
		work = append(work, g)
		origPos = orig[k].end
		movedCount++
	}
	t.moved.Add(movedCount)
	// untouched suffix keeps its original positions
	work = append(work, orig[k:]...)
	// remaining delay (if any) dissolves into the infinite trailing gap
	sh := seg{id: id, start: start, end: start + dur, kind: kindShift}
	pos := sort.Search(len(work), func(i int) bool { return work[i].start >= start })
	work = append(work, seg{})
	copy(work[pos+1:], work[pos:])
	work[pos] = sh
	// commit only after every check has succeeded
	t.blk = work
	t.maxNow = now
	t.insertOv(sh)
	return nil
}

// Cancel truncates every segment of id at now.
func (t *Timeline) Cancel(now int64, id string) error {
	if !validTime(now) || id == "" {
		return ErrBadArg
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if now < t.maxNow {
		return ErrClockBack
	}
	if !t.hasID(id) {
		return ErrNotFound
	}
	allEnded := true
	hasPast := false
	truncate := func(list []seg) []seg {
		out := list[:0]
		for _, s := range list {
			if s.id != id {
				out = append(out, s)
				continue
			}
			if s.start >= now {
				// wholly in the future: removed entirely, no backfill
				continue
			}
			hasPast = true
			if s.end <= now {
				// historical segment: untouched, proves the id aired
				out = append(out, s)
				continue
			}
			// live segment crossing now: truncate at now, id stays live
			allEnded = false
			s.end = now
			out = append(out, s)
		}
		return out
	}
	t.ov = truncate(t.ov)
	t.blk = truncate(t.blk)
	t.maxNow = now
	if allEnded && hasPast {
		return ErrEnded
	}
	if allEnded {
		// only wholly-future segments existed: silently removed, id released
		return nil
	}
	return nil
}

// At reports what plays at time t.
func (t *Timeline) At(tm int64) (Result, error) {
	if !validTime(tm) {
		return Result{}, ErrBadTime
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	// 1) override segment wins
	if i, ok := t.find(t.ov, tm); ok {
		s := t.ov[i]
		return Result{Kind: KindOverride, ID: s.id, Offset: tm - s.start}, nil
	}
	// 2) regular program segment
	if i, ok := t.find(t.blk, tm); ok && t.blk[i].kind == kindProgram {
		s := t.blk[i]
		return Result{Kind: KindProgram, ID: s.id, Offset: s.off + tm - s.start}, nil
	}
	// 3) looping filler; origin = latest program/shift segment ending <= t
	origin := int64(0)
	i := sort.Search(len(t.blk), func(i int) bool { return t.blk[i].start > tm })
	if i > 0 {
		origin = t.blk[i-1].end
	}
	return Result{Kind: KindFiller, Offset: ((tm-origin)%t.fillerLen + t.fillerLen) % t.fillerLen}, nil
}

// SegmentCount returns the total number of stored segments.
func (t *Timeline) SegmentCount() int { return len(t.ov) + len(t.blk) }

// ResetCounters zeroes the instrumentation counters.
func (t *Timeline) ResetCounters() {
	t.probes.Store(0)
	t.moved.Store(0)
}

// Probes reports segment comparisons made by At since the last reset.
func (t *Timeline) Probes() int64 { return t.probes.Load() }

// Moved reports segment checks made by the latest shifting override.
func (t *Timeline) Moved() int64 { return t.moved.Load() }

func validTime(v int64) bool   { return v >= 0 && v <= 1_000_000_000_000 }
func validDur(v int64) bool    { return v >= 1 && v <= 1_000_000_000 }
func validFiller(v int64) bool { return v >= 1 && v <= 1_000_000 }

// find returns the index of the half-open segment containing tm.
func (t *Timeline) find(list []seg, tm int64) (int, bool) {
	lo, hi := 0, len(list)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		t.probes.Add(1)
		if list[mid].end <= tm {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(list) {
		t.probes.Add(1)
		if list[lo].start <= tm && tm < list[lo].end {
			return lo, true
		}
	}
	return 0, false
}

func (t *Timeline) hasID(id string) bool {
	for _, s := range t.ov {
		if s.id == id {
			return true
		}
	}
	for _, s := range t.blk {
		if s.id == id {
			return true
		}
	}
	return false
}

func (t *Timeline) insertBlk(s seg) {
	i := sort.Search(len(t.blk), func(i int) bool { return t.blk[i].start >= s.start })
	t.blk = append(t.blk, seg{})
	copy(t.blk[i+1:], t.blk[i:])
	t.blk[i] = s
}

func (t *Timeline) insertOv(s seg) {
	i := sort.Search(len(t.ov), func(i int) bool { return t.ov[i].start >= s.start })
	t.ov = append(t.ov, seg{})
	copy(t.ov[i+1:], t.ov[i:])
	t.ov[i] = s
}
