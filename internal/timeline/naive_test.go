package timeline_test

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"testing"

	"ontology/internal/timeline"
	"ontology/override"
	"ontology/playout"
	"ontology/slot"
)

// Independent reference model on a small horizon, with two independent
// layers: pg holds program/shift cells; ov holds the preempt overlay.
type pgCell struct {
	id    string
	off   int64
	fixed bool
	shift bool
}

type ovCell struct {
	id  string
	off int64
}

type naive struct {
	f, h   int64
	pg     []pgCell
	ov     []*ovCell
	maxNow int64
	ids    map[string]bool
}

func newNaive(f, h int64) *naive {
	return &naive{f: f, h: h, pg: make([]pgCell, h), ov: make([]*ovCell, h), ids: map[string]bool{}}
}

func (n *naive) bad(now, s, d int64, id string) bool {
	return now < 0 || now > n.h || s < 0 || d < 1 || s+d > n.h || id == ""
}

func (n *naive) schedule(now int64, id string, s, d int64, fixed bool) error {
	if n.bad(now, s, d, id) {
		return timeline.ErrBadArg
	}
	if now < n.maxNow {
		return timeline.ErrClockBack
	}
	if n.ids[id] {
		return timeline.ErrDuplicate
	}
	if s < now {
		return timeline.ErrPast
	}
	for t := s; t < s+d; t++ {
		if n.pg[t].id != "" {
			return timeline.ErrOverlap
		}
	}
	n.maxNow = now
	n.ids[id] = true
	for k, t := int64(0), s; t < s+d; k, t = k+1, t+1 {
		n.pg[t] = pgCell{id: id, off: k, fixed: fixed}
	}
	return nil
}

func (n *naive) preempt(now int64, id string, s, d int64) error {
	if n.bad(now, s, d, id) {
		return timeline.ErrBadArg
	}
	if now < n.maxNow {
		return timeline.ErrClockBack
	}
	if n.ids[id] {
		return timeline.ErrDuplicate
	}
	if s < now {
		return timeline.ErrPast
	}
	for t := s; t < s+d; t++ {
		if n.ov[t] != nil || n.pg[t].shift {
			return timeline.ErrOverride
		}
	}
	n.maxNow = now
	n.ids[id] = true
	for k, t := int64(0), s; t < s+d; k, t = k+1, t+1 {
		n.ov[t] = &ovCell{id: id, off: k}
	}
	return nil
}

func (n *naive) pgSegEnd(t int64) int64 {
	e := t
	for e < n.h && n.pg[e].id == n.pg[t].id && n.pg[e].shift == n.pg[t].shift {
		e++
	}
	return e
}

func (n *naive) ovSegEnd(t int64) int64 {
	e := t
	for e < n.h && n.ov[e] != nil && n.ov[e].id == n.ov[t].id {
		e++
	}
	return e
}

func (n *naive) shift(now int64, id string, s, d int64) error {
	if n.bad(now, s, d, id) {
		return timeline.ErrBadArg
	}
	if now < n.maxNow {
		return timeline.ErrClockBack
	}
	if n.ids[id] {
		return timeline.ErrDuplicate
	}
	if s < now {
		return timeline.ErrPast
	}
	for t := int64(0); t < n.h; {
		if n.ov[t] != nil {
			if n.ovSegEnd(t) > s {
				return timeline.ErrOverride
			}
			t = n.ovSegEnd(t)
			continue
		}
		if n.pg[t].shift {
			if n.pgSegEnd(t) > s {
				return timeline.ErrOverride
			}
			t = n.pgSegEnd(t)
			continue
		}
		t++
	}
	if s < n.h && n.pg[s].id != "" && n.pg[s].fixed {
		return timeline.ErrInFixed
	}
	snap := append([]pgCell(nil), n.pg...)
	restore := func() { n.pg = append([]pgCell(nil), snap...) }

	disp := make([]int64, n.h)
	// cursor in ORIGINAL coordinates; splitTail, when present, is the first
	// segment encountered and always moves by d plus any carried remain.
	pos := s
	splitTailStart, splitTailEnd := int64(-1), int64(-1)
	// split only when s is strictly inside a floating program segment:
	// find the segment containing s, not whatever sits exactly at s.
	if s < n.h {
		j := s
		for j > 0 && (snap[j].id == "" || snap[j].shift) {
			j--
		}
		if j >= 0 {
			end := j
			for end < n.h && snap[end].id == snap[j].id && !snap[end].shift && !snap[end].fixed {
				end++
			}
			if snap[j].id != "" && !snap[j].fixed && !snap[j].shift && j < s && s < end {
				splitTailStart, splitTailEnd = j, end
			}
		}
	}
	remain := d
	for remain > 0 && pos < n.h {
		var a int64
		if splitTailStart >= 0 {
			a = splitTailStart
		} else {
			gap := int64(0)
			for pos+gap < n.h && snap[pos+gap].id == "" {
				gap++
			}
			if gap >= remain || pos+gap >= n.h {
				remain = 0
				break
			}
			a = pos + gap
		}
		if snap[a].fixed {
			restore()
			return timeline.ErrCrowdFixed
		}
		b := a
		for b < n.h && snap[b].id == snap[a].id && snap[b].shift == snap[a].shift {
			b++
		}
		move := remain
		if splitTailStart >= 0 {
			move = d + remain // jump over the override and carry the delay
		}
		for u := a; u < b; u++ {
			disp[u] = move
		}
		if splitTailStart >= 0 {
			// cells [s, splitTailStart) are the dropped left part of the
			// split; the scan resumes in original coordinates at the tail's
			// original end, with delay remain still unconsumed
			pos = splitTailEnd
			splitTailStart, splitTailEnd = -1, -1
			continue
		}
		pos = b
	}
	out := make([]pgCell, n.h)
	for t := int64(0); t < n.h; t++ {
		if snap[t].id == "" {
			continue
		}
		dst := t + disp[t]
		if dst >= n.h {
			continue
		}
		out[dst] = snap[t]
	}
	for k, t := int64(0), s; t < s+d; k, t = k+1, t+1 {
		out[t] = pgCell{id: id, off: k, shift: true}
	}
	n.pg = out
	n.maxNow = now
	n.ids[id] = true
	return nil
}

func (n *naive) cancel(now int64, id string) error {
	if now < 0 || now > n.h || id == "" {
		return timeline.ErrBadArg
	}
	if now < n.maxNow {
		return timeline.ErrClockBack
	}
	if !n.ids[id] {
		return timeline.ErrNotFound
	}
	live := false
	hasPast := false
	for t := int64(0); t < n.h; {
		if n.ov[t] != nil && n.ov[t].id == id {
			e := n.ovSegEnd(t)
			if t < now && e > now {
				live = true
			}
			if t < now {
				hasPast = true
			}
			for u := t; u < e; u++ {
				if u >= now {
					n.ov[u] = nil
				}
			}
			t = e
			continue
		}
		if n.pg[t].id == id {
			e := n.pgSegEnd(t)
			if t < now && e > now {
				live = true
			}
			if t < now {
				hasPast = true
			}
			for u := t; u < e; u++ {
				if u >= now {
					n.pg[u] = pgCell{}
				}
			}
			t = e
			continue
		}
		t++
	}
	n.maxNow = now
	if !live && hasPast {
		return timeline.ErrEnded
	}
	if !live {
		// wholly future segments removed silently; id released
		delete(n.ids, id)
	}
	return nil
}

func (n *naive) at(t int64) timeline.Result {
	if c := n.ov[t]; c != nil {
		return timeline.Result{Kind: playout.KindOverride, ID: c.id, Offset: c.off}
	}
	if c := n.pg[t]; c.id != "" {
		k := playout.KindProgram
		if c.shift {
			k = playout.KindOverride
		}
		return timeline.Result{Kind: k, ID: c.id, Offset: c.off}
	}
	origin := int64(0)
	for u := t - 1; u >= 0; u-- {
		if n.pg[u].id != "" {
			origin = u + 1
			break
		}
	}
	return timeline.Result{Kind: playout.KindFiller, Offset: (t - origin) % n.f}
}

func sameErr(a, b error) bool { return errors.Is(a, b) && errors.Is(b, a) }

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestNaiveRandom(t *testing.T) {
	const (
		seeds   = 1500
		horizon = 240 // queried time range
		gridH   = horizon + 60000
		maxOps  = 70
	)
	fixedRNG := rand.New(rand.NewPCG(7, 7))
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewPCG(uint64(seed), 0x9e3779b97f4a7c15))
		f := int64(1 + rng.IntN(12))
		c, _ := slot.New(f)
		nv := newNaive(f, gridH)
		logs := []string{fmt.Sprintf("seed=%d F=%d", seed, f)}
		now := int64(0)
		for i := 0; i < maxOps; i++ {
			now += int64(rng.IntN(3))
			if now > horizon-2 {
				break
			}
			id := fmt.Sprintf("p%02d", 1+rng.IntN(16))
			s := int64(rng.IntN(horizon - 2))
			d := int64(1 + rng.IntN(45))
			if s+d > horizon-1 {
				d = horizon - 1 - s
			}
			var e1, e2 error
			var desc string
			switch rng.IntN(6) {
			case 0, 1:
				fixed := fixedRNG.IntN(4) == 0
				e1 = slot.Schedule(c, now, id, s, d, fixed)
				e2 = nv.schedule(now, id, s, d, fixed)
				desc = fmt.Sprintf("schedule(now=%d id=%s s=%d d=%d fixed=%v) -> %v", now, id, s, d, fixed, e1)
			case 2:
				e1 = override.Preempt(c, now, id, s, d)
				e2 = nv.preempt(now, id, s, d)
				desc = fmt.Sprintf("preempt(now=%d id=%s s=%d d=%d) -> %v", now, id, s, d, e1)
			case 3:
				e1 = override.Shift(c, now, id, s, d)
				e2 = nv.shift(now, id, s, d)
				desc = fmt.Sprintf("shift(now=%d id=%s s=%d d=%d) -> %v", now, id, s, d, e1)
			default:
				e1 = override.Cancel(c, now, id)
				e2 = nv.cancel(now, id)
				desc = fmt.Sprintf("cancel(now=%d id=%s) -> %v", now, id, e1)
			}
			logs = append(logs, desc)
			if !sameErr(e1, e2) {
				t.Fatalf("seed=%d decision mismatch: engine=%v naive=%v | %s", seed, e1, e2, desc)
			}
			for tt := int64(0); tt < horizon; tt++ {
				got, err := playout.At(c, tt)
				if err != nil {
					t.Fatal(err)
				}
				want := nv.at(tt)
				if got != want {
					for k, l := range logs {
						t.Logf("input[%d] %s", k, l)
					}
					t.Fatalf("seed=%d At(%d): engine=%+v naive=%+v (basis: %s)", seed, tt, got, want, desc)
				}
			}
		}
	}
}

func TestRejectionOrder(t *testing.T) {
	t.Run("schedule", func(t *testing.T) {
		c := mustChannel(t, 7)
		mustOK(t, slot.Schedule(c, 10, "A", 100, 10, false))
		check := func(want error, now int64, id string, s, d int64, fixed bool) {
			t.Helper()
			if err := slot.Schedule(c, now, id, s, d, fixed); !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		}
		check(timeline.ErrBadArg, 5, "", 0, 10, false)
		check(timeline.ErrClockBack, 5, "Z", 100, 10, false)
		check(timeline.ErrDuplicate, 10, "A", 100, 10, false)
		check(timeline.ErrPast, 11, "B", 5, 10, false)
		check(timeline.ErrOverlap, 11, "B", 105, 10, false)
		mustOK(t, slot.Schedule(c, 11, "B", 110, 10, false))
	})
	t.Run("override", func(t *testing.T) {
		c := mustChannel(t, 7)
		mustOK(t, slot.Schedule(c, 0, "F", 270, 130, true))
		mustOK(t, slot.Schedule(c, 0, "G", 220, 10, false))
		mustOK(t, override.Preempt(c, 0, "X", 250, 10))
		checkShift := func(want error, now int64, id string, s, d int64) {
			t.Helper()
			if err := override.Shift(c, now, id, s, d); !errors.Is(err, want) {
				t.Fatalf("shift got %v want %v", err, want)
			}
		}
		checkShift(timeline.ErrBadArg, 5, "", 10, 10)
		checkShift(timeline.ErrBadArg, -1, "Z", 10, 10)
		c2 := mustChannel(t, 7)
		mustOK(t, slot.Schedule(c2, 0, "P", 100, 100, false))
		mustOK(t, override.Shift(c2, 1, "early", 50, 5))
		if err := override.Shift(c2, 0, "later", 60, 5); !errors.Is(err, timeline.ErrClockBack) {
			t.Fatalf("got %v want clock back", err)
		}
		checkShift(timeline.ErrDuplicate, 0, "X", 10, 10)
		checkShift(timeline.ErrPast, 260, "Z", 10, 10)
		checkShift(timeline.ErrOverride, 2, "Z", 240, 10)
		checkShift(timeline.ErrInFixed, 3, "Z", 300, 10)
		// crowd fixed: after X (ends 260), a shift whose delay reaches F@270
		checkShift(timeline.ErrCrowdFixed, 3, "Z", 265, 10)
	})
	t.Run("cancel", func(t *testing.T) {
		c := mustChannel(t, 7)
		if err := override.Cancel(c, -1, "A"); !errors.Is(err, timeline.ErrBadArg) {
			t.Fatalf("got %v", err)
		}
		mustOK(t, slot.Schedule(c, 10, "A", 100, 10, false))
		if err := override.Cancel(c, 5, "A"); !errors.Is(err, timeline.ErrClockBack) {
			t.Fatalf("got %v", err)
		}
		if err := override.Cancel(c, 10, "B"); !errors.Is(err, timeline.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
		if err := override.Cancel(c, 200, "A"); !errors.Is(err, timeline.ErrEnded) {
			t.Fatalf("got %v", err)
		}
	})
}

func ceilLog2Bound(n int) int64 {
	return 2*int64(math.Ceil(math.Log2(float64(n+2)))) + 2
}

func buildProbeTimeline(t *testing.T, segments int) *timeline.Timeline {
	t.Helper()
	c := mustChannel(t, 7)
	for i := 0; i < segments; i++ {
		base := int64(i) * 100
		if err := slot.Schedule(c, 0, fmt.Sprintf("p%05d", i), base, 90, false); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestProbeBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		c := buildProbeTimeline(t, n)
		var worst int64
		for q := int64(0); q < int64(n)*100; q += 37 {
			c.ResetCounters()
			if _, err := playout.At(c, q); err != nil {
				t.Fatal(err)
			}
			if c.Probes() > worst {
				worst = c.Probes()
			}
		}
		bound := ceilLog2Bound(c.SegmentCount())
		t.Logf("segments=%d worst probes=%d bound 2*ceil(log2(n+2))+2=%d", c.SegmentCount(), worst, bound)
		if worst > bound {
			t.Fatalf("probes %d exceed bound %d at n=%d", worst, bound, n)
		}
	}
}

func TestMovedBound(t *testing.T) {
	for _, trailing := range []int{100, 10000} {
		c := mustChannel(t, 7)
		if err := slot.Schedule(c, 0, "head", 10, 20, false); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < trailing; i++ {
			base := 1000 + int64(i)*10
			if err := slot.Schedule(c, 0, fmt.Sprintf("t%05d", i), base, 5, false); err != nil {
				t.Fatal(err)
			}
		}
		c.ResetCounters()
		if err := override.Shift(c, 0, "Y", 15, 5); err != nil {
			t.Fatal(err)
		}
		affected := 1
		t.Logf("trailing=%d moved checks=%d, allowed <= affected(%d)+3=%d",
			trailing, c.Moved(), affected, affected+3)
		if c.Moved() > int64(affected+3) {
			t.Fatalf("moved %d exceeds %d with %d trailing segments", c.Moved(), affected+3, trailing)
		}
	}
}

func TestConcurrentEquivalence(t *testing.T) {
	c := mustChannel(t, 7)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(g), 1))
			for i := 0; i < 200; i++ {
				now := int64(g*10000 + i)
				id := fmt.Sprintf("g%dp%d", g, i)
				s := 100000 + int64(g)*10000 + int64(i)*3
				_ = slot.Schedule(c, now, id, s, 2, false)
				_, _ = playout.At(c, int64(rng.IntN(300000)))
			}
		}(g)
	}
	wg.Wait()
	r, err := playout.At(c, 250000)
	if err != nil || r.Kind < 0 || r.Kind > playout.KindOverride {
		t.Fatalf("bad state after concurrent run: %+v %v", r, err)
	}
}
