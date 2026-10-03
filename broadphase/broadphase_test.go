package broadphase_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/broadphase"
)

func bx(lx, hx, ly, hy int64) broadphase.Box {
	return broadphase.Box{Lx: lx, Hx: hx, Ly: ly, Hy: hy}
}

func pr(a, b int64) broadphase.Pair { return broadphase.Pair{A: a, B: b} }

// equalPairs compares two pair lists treating nil and empty as equal.
func equalPairs(got, want []broadphase.Pair) bool {
	if len(got) == 0 && len(want) == 0 {
		return true
	}
	return reflect.DeepEqual(got, want)
}

func checkEvents(t *testing.T, ctx string, got broadphase.Events,
	fatEnter, fatExit, conEnter, conExit []broadphase.Pair) {
	t.Helper()
	if !equalPairs(got.FatEnter, fatEnter) || !equalPairs(got.FatExit, fatExit) ||
		!equalPairs(got.ContactEnter, conEnter) || !equalPairs(got.ContactExit, conExit) {
		t.Fatalf("%s: events mismatch\n got: FatEnter=%v FatExit=%v ContactEnter=%v ContactExit=%v\nwant: FatEnter=%v FatExit=%v ContactEnter=%v ContactExit=%v",
			ctx, got.FatEnter, got.FatExit, got.ContactEnter, got.ContactExit,
			fatEnter, fatExit, conEnter, conExit)
	}
}

func mustNew(t *testing.T, margin int64, capacity int) *broadphase.Broadphase {
	t.Helper()
	b, err := broadphase.New(margin, capacity)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", margin, capacity, err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Naive reference implementation: recomputes everything by pairwise
// comparison after every operation.
// ---------------------------------------------------------------------------

type nObj struct {
	tight, fat  broadphase.Box
	layer, mask int
}

type naive struct {
	margin              int64
	cap                 int
	objs                map[int64]*nObj
	crossed, pairChecks int64
}

func nCollidable(a, b *nObj) bool {
	return a.layer&b.mask != 0 && b.layer&a.mask != 0
}

func nIntersect(a, b broadphase.Box) bool {
	return a.Lx < b.Hx && b.Lx < a.Hx && a.Ly < b.Hy && b.Ly < a.Hy
}

func nContains(outer, inner broadphase.Box) bool {
	return inner.Lx >= outer.Lx && inner.Hx <= outer.Hx &&
		inner.Ly >= outer.Ly && inner.Hy <= outer.Hy
}

func nValidBox(t broadphase.Box) bool {
	const c = int64(1_000_000_000)
	return t.Lx >= -c && t.Hx <= c && t.Lx < t.Hx &&
		t.Ly >= -c && t.Hy <= c && t.Ly < t.Hy
}

func nValidFilter(f int) bool { return f >= 0 && f <= 65535 }

func (n *naive) expand(t broadphase.Box) broadphase.Box {
	return broadphase.Box{
		Lx: t.Lx - n.margin, Hx: t.Hx + n.margin,
		Ly: t.Ly - n.margin, Hy: t.Hy + n.margin,
	}
}

func (n *naive) sortedIDs() []int64 {
	ids := make([]int64, 0, len(n.objs))
	for id := range n.objs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (n *naive) pairs() []broadphase.Pair {
	ids := n.sortedIDs()
	out := []broadphase.Pair{}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := n.objs[ids[i]], n.objs[ids[j]]
			if nCollidable(a, b) && nIntersect(a.fat, b.fat) {
				out = append(out, pr(ids[i], ids[j]))
			}
		}
	}
	return out
}

func (n *naive) contacts() []broadphase.Pair {
	ids := n.sortedIDs()
	out := []broadphase.Pair{}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := n.objs[ids[i]], n.objs[ids[j]]
			if nCollidable(a, b) && nIntersect(a.tight, b.tight) {
				out = append(out, pr(ids[i], ids[j]))
			}
		}
	}
	return out
}

// pairsOf returns the fat-pair and contact partners of id.
func (n *naive) pairsOf(id int64) (fat, con map[int64]bool) {
	fat, con = map[int64]bool{}, map[int64]bool{}
	o := n.objs[id]
	if o == nil {
		return fat, con
	}
	for pid, p := range n.objs {
		if pid == id || !nCollidable(o, p) {
			continue
		}
		if nIntersect(o.fat, p.fat) {
			fat[pid] = true
		}
		if nIntersect(o.tight, p.tight) {
			con[pid] = true
		}
	}
	return fat, con
}

func nEvents(id int64, fatB, fatA, conB, conA map[int64]bool) broadphase.Events {
	mk := func(set map[int64]bool, other map[int64]bool) []broadphase.Pair {
		out := []broadphase.Pair{}
		for pid := range set {
			if !other[pid] {
				out = append(out, pr(min(id, pid), max(id, pid)))
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].A != out[j].A {
				return out[i].A < out[j].A
			}
			return out[i].B < out[j].B
		})
		return out
	}
	return broadphase.Events{
		FatEnter:     mk(fatA, fatB),
		FatExit:      mk(fatB, fatA),
		ContactEnter: mk(conA, conB),
		ContactExit:  mk(conB, conA),
	}
}

// nEndpoint is one fat-box x endpoint in the naive sweep model; hi
// endpoints sort before lo endpoints at equal coordinates.
type nEndpoint struct {
	coord int64
	hi    bool
	id    int64
}

func nEndpointLess(a, b nEndpoint) bool {
	if a.coord != b.coord {
		return a.coord < b.coord
	}
	if a.hi != b.hi {
		return a.hi
	}
	return a.id < b.id
}

func (n *naive) insert(id int64, tight broadphase.Box, layer, mask int) (broadphase.Events, string) {
	if id <= 0 || !nValidBox(tight) || !nValidFilter(layer) || !nValidFilter(mask) {
		return broadphase.Events{}, "invalid"
	}
	if _, ok := n.objs[id]; ok {
		return broadphase.Events{}, "exists"
	}
	if len(n.objs) >= n.cap {
		return broadphase.Events{}, "full"
	}
	fatB, conB := n.pairsOf(id)
	n.objs[id] = &nObj{tight: tight, fat: n.expand(tight), layer: layer, mask: mask}
	fatA, conA := n.pairsOf(id)
	return nEvents(id, fatB, fatA, conB, conA), ""
}

func (n *naive) move(id int64, tight broadphase.Box) (broadphase.Events, bool, string) {
	if id <= 0 || !nValidBox(tight) {
		return broadphase.Events{}, false, "invalid"
	}
	o := n.objs[id]
	if o == nil {
		return broadphase.Events{}, false, "notexists"
	}
	fatB, conB := n.pairsOf(id)
	refit := !nContains(o.fat, tight)
	if refit {
		newFat := n.expand(tight)
		moving := []struct {
			old, new int64
			hi       bool
		}{
			{o.fat.Lx, newFat.Lx, false},
			{o.fat.Hx, newFat.Hx, true},
		}
		for pid, p := range n.objs {
			if pid == id {
				continue
			}
			others := []nEndpoint{{p.fat.Lx, false, pid}, {p.fat.Hx, true, pid}}
			for _, mv := range moving {
				for _, e := range others {
					k1 := nEndpoint{mv.old, mv.hi, id}
					k2 := nEndpoint{mv.new, mv.hi, id}
					if nEndpointLess(k1, e) != nEndpointLess(k2, e) {
						n.crossed++
						if mv.hi != e.hi && nCollidable(o, p) {
							n.pairChecks++
						}
					}
				}
			}
		}
		o.tight = tight
		o.fat = newFat
	} else {
		o.tight = tight
	}
	fatA, conA := n.pairsOf(id)
	return nEvents(id, fatB, fatA, conB, conA), refit, ""
}

func (n *naive) remove(id int64) (broadphase.Events, string) {
	if id <= 0 {
		return broadphase.Events{}, "invalid"
	}
	if n.objs[id] == nil {
		return broadphase.Events{}, "notexists"
	}
	fatB, conB := n.pairsOf(id)
	delete(n.objs, id)
	return nEvents(id, fatB, nil, conB, nil), ""
}

func (n *naive) setFilter(id int64, layer, mask int) (broadphase.Events, string) {
	if id <= 0 || !nValidFilter(layer) || !nValidFilter(mask) {
		return broadphase.Events{}, "invalid"
	}
	o := n.objs[id]
	if o == nil {
		return broadphase.Events{}, "notexists"
	}
	fatB, conB := n.pairsOf(id)
	o.layer, o.mask = layer, mask
	fatA, conA := n.pairsOf(id)
	return nEvents(id, fatB, fatA, conB, conA), ""
}

func kindOf(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, broadphase.ErrInvalidParam):
		return "invalid"
	case errors.Is(err, broadphase.ErrAlreadyExists):
		return "exists"
	case errors.Is(err, broadphase.ErrNotExists):
		return "notexists"
	case errors.Is(err, broadphase.ErrFull):
		return "full"
	}
	return "unknown"
}

// TestSpecWalkthrough replays the scenario from the specification step
// by step, checking events, refit flags and the crossed/pairChecks
// counters.
func TestSpecWalkthrough(t *testing.T) {
	b := mustNew(t, 2, 10)

	ev, err := b.Insert(1, bx(0, 10, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 1", ev, nil, nil, nil, nil)

	ev, err = b.Insert(2, bx(13, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 2", ev, []broadphase.Pair{pr(1, 2)}, nil, nil, nil)

	ev, err = b.Insert(3, bx(14, 16, 0, 4))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 3", ev, []broadphase.Pair{pr(2, 3)}, nil, []broadphase.Pair{pr(2, 3)}, nil)

	mv, err := b.Move(2, bx(12, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if mv.Refit {
		t.Errorf("move [12,20): expected no refit")
	}
	checkEvents(t, "move no refit", mv.Events, nil, nil, nil, nil)

	mv, err = b.Move(2, bx(9, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("move [9,20): expected refit")
	}
	checkEvents(t, "move refit", mv.Events, nil, nil, []broadphase.Pair{pr(1, 2)}, nil)
	if got := b.Crossed(); got != 0 {
		t.Errorf("crossed after small refit = %d, want 0", got)
	}
	if got := b.PairChecks(); got != 0 {
		t.Errorf("pairChecks after small refit = %d, want 0", got)
	}

	mv, err = b.Move(2, bx(30, 40, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("move [30,40): expected refit")
	}
	checkEvents(t, "move far", mv.Events, nil, []broadphase.Pair{pr(1, 2), pr(2, 3)},
		nil, []broadphase.Pair{pr(1, 2), pr(2, 3)})
	if got := b.Crossed(); got != 3 {
		t.Errorf("crossed = %d, want 3", got)
	}
	if got := b.PairChecks(); got != 2 {
		t.Errorf("pairChecks = %d, want 2", got)
	}

	mv, err = b.Move(2, bx(14, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("move [14,20): expected refit")
	}
	checkEvents(t, "move back", mv.Events, []broadphase.Pair{pr(2, 3)}, nil, []broadphase.Pair{pr(2, 3)}, nil)
	if got := b.Crossed(); got != 5 {
		t.Errorf("crossed = %d, want 5", got)
	}
	if got := b.PairChecks(); got != 3 {
		t.Errorf("pairChecks = %d, want 3", got)
	}

	ev, err = b.SetFilter(3, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "setfilter 3", ev, nil, []broadphase.Pair{pr(2, 3)}, nil, []broadphase.Pair{pr(2, 3)})

	ev, err = b.SetFilter(2, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "setfilter 2", ev, []broadphase.Pair{pr(2, 3)}, nil, []broadphase.Pair{pr(2, 3)}, nil)

	if got := b.Crossed(); got != 5 {
		t.Errorf("crossed after setfilter = %d, want 5", got)
	}
	if got := b.PairChecks(); got != 3 {
		t.Errorf("pairChecks after setfilter = %d, want 3", got)
	}
	if got := b.Pairs(); !equalPairs(got, []broadphase.Pair{pr(2, 3)}) {
		t.Errorf("final pairs = %v, want [(2 3)]", got)
	}
	if got := b.Contacts(); !equalPairs(got, []broadphase.Pair{pr(2, 3)}) {
		t.Errorf("final contacts = %v, want [(2 3)]", got)
	}
}

// TestEdgeTouching verifies that boxes touching only on an edge neither
// intersect as fat boxes nor count as contacts.
func TestEdgeTouching(t *testing.T) {
	b := mustNew(t, 0, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	// Shares only the edge x=10 with object 1.
	ev, err := b.Insert(2, bx(10, 20, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "edge insert", ev, nil, nil, nil, nil)
	if got := b.Pairs(); len(got) != 0 {
		t.Errorf("pairs = %v, want empty", got)
	}
	// Shares only the edge y=10 with object 1.
	ev, err = b.Insert(3, bx(0, 10, 10, 20))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "edge insert y", ev, nil, nil, nil, nil)
	// Overlaps object 1 by one unit: fat pair and contact appear.
	ev, err = b.Insert(4, bx(9, 15, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "overlap insert", ev, []broadphase.Pair{pr(1, 4), pr(2, 4)}, nil,
		[]broadphase.Pair{pr(1, 4), pr(2, 4)}, nil)
}

// TestRefitBoundary checks that a new tight box exactly touching the fat
// box boundary does not refit, while exceeding it by one does.
func TestRefitBoundary(t *testing.T) {
	b := mustNew(t, 2, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	// Fat box is [-2,12) x [-2,12).
	mv, err := b.Move(1, bx(2, 12, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if mv.Refit {
		t.Errorf("hx == fat hi: expected no refit")
	}
	mv, err = b.Move(1, bx(-2, 10, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if mv.Refit {
		t.Errorf("lx == fat lo: expected no refit")
	}
	mv, err = b.Move(1, bx(2, 13, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("hx == fat hi + 1: expected refit")
	}
	if fat, _ := b.FatBox(1); fat != bx(0, 15, -2, 12) {
		t.Errorf("fat box after refit = %+v, want [0,15)x[-2,12)", fat)
	}
	mv, err = b.Move(1, bx(2, 13, 0, 13))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("hy == fat hy + 1: expected refit")
	}
}

// TestNonRefitContactChanges moves the tight box inside the fat box and
// expects contact enters/exits without any fat-pair events.
func TestNonRefitContactChanges(t *testing.T) {
	b := mustNew(t, 100, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	ev, err := b.Insert(2, bx(100, 110, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	// Fat boxes [-100,110) and [0,210) overlap; tight boxes do not.
	checkEvents(t, "insert 2", ev, []broadphase.Pair{pr(1, 2)}, nil, nil, nil)

	mv, err := b.Move(2, bx(5, 15, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if mv.Refit {
		t.Errorf("expected no refit")
	}
	checkEvents(t, "contact enter", mv.Events, nil, nil, []broadphase.Pair{pr(1, 2)}, nil)

	mv, err = b.Move(2, bx(50, 60, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if mv.Refit {
		t.Errorf("expected no refit")
	}
	checkEvents(t, "contact exit", mv.Events, nil, nil, nil, []broadphase.Pair{pr(1, 2)})
	if got := b.Crossed(); got != 0 {
		t.Errorf("crossed = %d, want 0", got)
	}
	if got := b.PairChecks(); got != 0 {
		t.Errorf("pairChecks = %d, want 0", got)
	}
}

// TestRefitDropsOldRange verifies that a refit replaces the fat box
// instead of unioning it with the old one: pairs intersecting only the
// old range must exit.
func TestRefitDropsOldRange(t *testing.T) {
	b := mustNew(t, 1, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	ev, err := b.Insert(2, bx(5, 8, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 2", ev, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)}, nil)

	// Refit to [99,111): if the implementation unioned with the old fat
	// box [4,9), the pair with object 1 would wrongly survive.
	mv, err := b.Move(2, bx(100, 110, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("expected refit")
	}
	checkEvents(t, "refit away", mv.Events, nil, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)})
	if got := b.Pairs(); len(got) != 0 {
		t.Errorf("pairs = %v, want empty", got)
	}
	if fat, _ := b.FatBox(2); fat != bx(99, 111, -1, 11) {
		t.Errorf("fat box = %+v, want [99,111)x[-1,11)", fat)
	}

	mv, err = b.Move(2, bx(6, 8, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("expected refit")
	}
	checkEvents(t, "refit back", mv.Events, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)}, nil)
}

// TestMoveEnterAndExit covers one Move that produces both a fat-pair
// enter and a fat-pair exit.
func TestMoveEnterAndExit(t *testing.T) {
	b := mustNew(t, 5, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	ev, err := b.Insert(2, bx(18, 28, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 2", ev, []broadphase.Pair{pr(1, 2)}, nil, nil, nil)
	ev, err = b.Insert(3, bx(40, 50, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 3", ev, nil, nil, nil, nil)

	// New fat box [25,43): leaves object 1 (fat [-5,15)), meets object 3
	// (fat [35,55)).
	mv, err := b.Move(2, bx(30, 38, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("expected refit")
	}
	checkEvents(t, "enter+exit", mv.Events, []broadphase.Pair{pr(2, 3)}, []broadphase.Pair{pr(1, 2)}, nil, nil)
}

// TestEndpointOrderHiBeforeLo checks the sweep ordering rule: at equal
// coordinates hi endpoints sort before lo endpoints, so a lo endpoint
// arriving exactly at another object's hi coordinate does not flip with
// it.
func TestEndpointOrderHiBeforeLo(t *testing.T) {
	b := mustNew(t, 0, 10)
	if _, err := b.Insert(1, bx(0, 12, 0, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Insert(2, bx(20, 30, 0, 10)); err != nil {
		t.Fatal(err)
	}
	// lo of object 2 lands exactly on hi of object 1 (12): hi sorts
	// before lo, so no flip and no intersection (edge touch only).
	mv, err := b.Move(2, bx(12, 25, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("expected refit")
	}
	checkEvents(t, "lo lands on hi", mv.Events, nil, nil, nil, nil)
	if got := b.Crossed(); got != 0 {
		t.Errorf("crossed = %d, want 0", got)
	}
	if got := b.PairChecks(); got != 0 {
		t.Errorf("pairChecks = %d, want 0", got)
	}
	// Moving one unit further left flips lo2 past hi1: one crossing, one
	// y check, and the pair enters.
	mv, err = b.Move(2, bx(11, 25, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "lo crosses hi", mv.Events, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)}, nil)
	if got := b.Crossed(); got != 1 {
		t.Errorf("crossed = %d, want 1", got)
	}
	if got := b.PairChecks(); got != 1 {
		t.Errorf("pairChecks = %d, want 1", got)
	}
}

// TestZeroMarginShrink verifies that with M=0 a tight box shrinking
// inside the old fat box does not refit: the fat box never shrinks.
func TestZeroMarginShrink(t *testing.T) {
	b := mustNew(t, 0, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	mv, err := b.Move(1, bx(3, 7, 2, 8))
	if err != nil {
		t.Fatal(err)
	}
	if mv.Refit {
		t.Errorf("shrink inside fat box: expected no refit")
	}
	if fat, _ := b.FatBox(1); fat != bx(0, 10, 0, 10) {
		t.Errorf("fat box = %+v, want unchanged [0,10)x[0,10)", fat)
	}
	if tight, _ := b.Tight(1); tight != bx(3, 7, 2, 8) {
		t.Errorf("tight box = %+v, want [3,7)x[2,8)", tight)
	}
	if got := b.Crossed(); got != 0 {
		t.Errorf("crossed = %d, want 0", got)
	}
}

// TestRemoveExitsBoth checks that Remove exits both the fat pairs and
// the contact pairs of the removed object.
func TestRemoveExitsBoth(t *testing.T) {
	b := mustNew(t, 2, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	ev, err := b.Insert(2, bx(1, 5, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 2", ev, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)}, nil)
	ev, err = b.Remove(2)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "remove 2", ev, nil, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)})
	if got := b.Pairs(); len(got) != 0 {
		t.Errorf("pairs = %v, want empty", got)
	}
	if got := b.Contacts(); len(got) != 0 {
		t.Errorf("contacts = %v, want empty", got)
	}
	if _, ok := b.FatBox(2); ok {
		t.Errorf("FatBox(2) still present after remove")
	}
}

// TestBidirectionalFilter checks that collision requires both directions
// of the layer/mask test, and that layer=0 or mask=0 never collides.
func TestBidirectionalFilter(t *testing.T) {
	b := mustNew(t, 0, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10), 1, 1); err != nil {
		t.Fatal(err)
	}
	// One-directional pass only: 1.layer&2.mask = 1&1 ok, but
	// 2.layer&1.mask = 2&1 = 0, so not collidable.
	ev, err := b.Insert(2, bx(0, 10, 0, 10), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "one-way filter", ev, nil, nil, nil, nil)
	// layer = 0 never collides.
	ev, err = b.Insert(3, bx(0, 10, 0, 10), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "zero layer", ev, nil, nil, nil, nil)
	// mask = 0 never collides.
	ev, err = b.Insert(4, bx(0, 10, 0, 10), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "zero mask", ev, nil, nil, nil, nil)
	// Both directions pass with objects 1 and 2: 1&3, 3&1, 2&3, 3&1.
	ev, err = b.Insert(5, bx(0, 10, 0, 10), 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "both ways", ev, []broadphase.Pair{pr(1, 5), pr(2, 5)}, nil,
		[]broadphase.Pair{pr(1, 5), pr(2, 5)}, nil)
}

// TestSetFilterEvents verifies that SetFilter moves pairs in and out of
// both sets without changing boxes or counters.
func TestSetFilterEvents(t *testing.T) {
	b := mustNew(t, 0, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10), 1, 1); err != nil {
		t.Fatal(err)
	}
	ev, err := b.Insert(2, bx(5, 15, 0, 10), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "insert 2", ev, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)}, nil)

	ev, err = b.SetFilter(2, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "filter off", ev, nil, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)})
	if got := b.Pairs(); len(got) != 0 {
		t.Errorf("pairs = %v, want empty", got)
	}

	ev, err = b.SetFilter(2, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	checkEvents(t, "filter on", ev, []broadphase.Pair{pr(1, 2)}, nil, []broadphase.Pair{pr(1, 2)}, nil)

	if fat, _ := b.FatBox(2); fat != bx(5, 15, 0, 10) {
		t.Errorf("fat box changed by SetFilter: %+v", fat)
	}
	if got := b.Crossed(); got != 0 {
		t.Errorf("crossed = %d, want 0", got)
	}
	if got := b.PairChecks(); got != 0 {
		t.Errorf("pairChecks = %d, want 0", got)
	}
}

// TestNonCollidableFlipNoPairChecks flips endpoints against a
// non-collidable object: crossings are counted but no y comparison is.
func TestNonCollidableFlipNoPairChecks(t *testing.T) {
	b := mustNew(t, 0, 10)
	if _, err := b.Insert(1, bx(0, 10, 0, 10), 1, 1); err != nil {
		t.Fatal(err)
	}
	// 1.layer&2.mask = 1&2 = 0: never collidable.
	if _, err := b.Insert(2, bx(20, 30, 0, 10), 2, 2); err != nil {
		t.Fatal(err)
	}
	mv, err := b.Move(2, bx(5, 15, 0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if !mv.Refit {
		t.Errorf("expected refit")
	}
	checkEvents(t, "non-collidable move", mv.Events, nil, nil, nil, nil)
	if got := b.Crossed(); got != 1 {
		t.Errorf("crossed = %d, want 1", got)
	}
	if got := b.PairChecks(); got != 0 {
		t.Errorf("pairChecks = %d, want 0", got)
	}
	if got := b.Pairs(); len(got) != 0 {
		t.Errorf("pairs = %v, want empty", got)
	}
}

// TestRejectedOpsKeepState feeds invalid operations and checks the error
// kind and that no state (objects, boxes, pairs, counters) changes.
func TestRejectedOpsKeepState(t *testing.T) {
	b := mustNew(t, 1, 2)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Insert(2, bx(5, 15, 0, 10)); err != nil {
		t.Fatal(err)
	}

	snapshot := func() (int64, int64, []broadphase.Pair, []broadphase.Pair, broadphase.Box, broadphase.Box) {
		fat, _ := b.FatBox(1)
		tight, _ := b.Tight(1)
		return b.Crossed(), b.PairChecks(), b.Pairs(), b.Contacts(), fat, tight
	}
	c0, p0, pairs0, contacts0, fat0, tight0 := snapshot()

	checkUnchanged := func(ctx string) {
		t.Helper()
		c1, p1, pairs1, contacts1, fat1, tight1 := snapshot()
		if c1 != c0 || p1 != p0 || !equalPairs(pairs1, pairs0) || !equalPairs(contacts1, contacts0) ||
			fat1 != fat0 || tight1 != tight0 {
			t.Fatalf("%s: rejected op changed state", ctx)
		}
	}

	if _, err := b.Insert(0, bx(0, 1, 0, 1)); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("insert id=0: %v", err)
	}
	checkUnchanged("insert id=0")
	if _, err := b.Insert(3, bx(5, 5, 0, 1)); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("insert lo==hi: %v", err)
	}
	checkUnchanged("insert lo==hi")
	if _, err := b.Insert(3, bx(0, 2_000_000_000, 0, 1)); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("insert coord out of range: %v", err)
	}
	checkUnchanged("insert coord out of range")
	if _, err := b.Insert(3, bx(0, 1, 0, 1), 1, 70000); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("insert mask out of range: %v", err)
	}
	checkUnchanged("insert mask out of range")
	if _, err := b.Insert(1, bx(0, 1, 0, 1)); !errors.Is(err, broadphase.ErrAlreadyExists) {
		t.Errorf("insert duplicate: %v", err)
	}
	checkUnchanged("insert duplicate")
	if _, err := b.Insert(3, bx(0, 1, 0, 1)); !errors.Is(err, broadphase.ErrFull) {
		t.Errorf("insert when full: %v", err)
	}
	checkUnchanged("insert when full")
	if _, err := b.Move(9, bx(0, 1, 0, 1)); !errors.Is(err, broadphase.ErrNotExists) {
		t.Errorf("move missing: %v", err)
	}
	checkUnchanged("move missing")
	if _, err := b.Move(1, bx(0, 0, 0, 1)); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("move bad box: %v", err)
	}
	checkUnchanged("move bad box")
	if _, err := b.Remove(9); !errors.Is(err, broadphase.ErrNotExists) {
		t.Errorf("remove missing: %v", err)
	}
	checkUnchanged("remove missing")
	if _, err := b.SetFilter(9, 1, 1); !errors.Is(err, broadphase.ErrNotExists) {
		t.Errorf("setfilter missing: %v", err)
	}
	checkUnchanged("setfilter missing")
	if _, err := b.SetFilter(1, -1, 1); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("setfilter bad layer: %v", err)
	}
	checkUnchanged("setfilter bad layer")
}

// TestValidationOrder checks that the first applicable reason is
// reported: invalid parameter, then exists/not-exists, then full.
func TestValidationOrder(t *testing.T) {
	if _, err := broadphase.New(-1, 10); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("negative margin: %v", err)
	}
	if _, err := broadphase.New(1_000_001, 10); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("margin too large: %v", err)
	}
	if _, err := broadphase.New(0, 0); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("zero capacity: %v", err)
	}
	if _, err := broadphase.New(0, 100_001); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("capacity too large: %v", err)
	}

	b := mustNew(t, 0, 1)
	if _, err := b.Insert(1, bx(0, 10, 0, 10)); err != nil {
		t.Fatal(err)
	}
	// Invalid parameter wins over already-exists.
	if _, err := b.Insert(1, bx(5, 5, 0, 1)); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("invalid+exists: %v", err)
	}
	// Already-exists wins over full.
	if _, err := b.Insert(1, bx(0, 1, 0, 1)); !errors.Is(err, broadphase.ErrAlreadyExists) {
		t.Errorf("exists+full: %v", err)
	}
	// Invalid parameter wins over not-exists.
	if _, err := b.Move(9, bx(0, 0, 0, 1)); !errors.Is(err, broadphase.ErrInvalidParam) {
		t.Errorf("invalid+notexists: %v", err)
	}
}

// TestCounterBoundsLargeN inserts 100000 objects and then performs small
// refits far away from everything: crossed and pairChecks must stay 0 or
// constant.
func TestCounterBoundsLargeN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large-n test in -short mode")
	}
	const n = 99990
	b := mustNew(t, 0, 100000)
	for i := 1; i <= n; i++ {
		x := int64(i) * 10
		if _, err := b.Insert(int64(i), bx(x, x+1, 0, 1)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	// A few objects in a remote region, close to each other.
	for i := int64(1); i <= 3; i++ {
		x := 500_000_000 + i*7
		if _, err := b.Insert(int64(n)+i, bx(x, x+1, 0, 1)); err != nil {
			t.Fatalf("insert remote %d: %v", i, err)
		}
	}
	// Teleport object 1 far away (one big jump), then do many small
	// refits in the remote region.
	if _, err := b.Move(1, bx(500_000_098, 500_000_099, 0, 1)); err != nil {
		t.Fatal(err)
	}
	baseCrossed := b.Crossed()
	baseChecks := b.PairChecks()
	for k := int64(0); k < 1000; k++ {
		x := 500_000_100 + k*2
		mv, err := b.Move(1, bx(x, x+1, 0, 1))
		if err != nil {
			t.Fatal(err)
		}
		if !mv.Refit {
			t.Fatalf("move %d: expected refit with M=0", k)
		}
		dc := b.Crossed() - baseCrossed
		dp := b.PairChecks() - baseChecks
		if dc != 0 || dp != 0 {
			t.Fatalf("small far refit %d: crossed +%d, pairChecks +%d, want 0", k, dc, dp)
		}
		if len(mv.FatEnter) != 0 || len(mv.FatExit) != 0 {
			t.Fatalf("small far refit %d: unexpected events %+v", k, mv)
		}
	}
	// Small refits next to the remote cluster: bounded crossings only.
	if _, err := b.Move(1, bx(500_000_000, 500_000_001, 0, 1)); err != nil {
		t.Fatal(err)
	}
	prevC, prevP := b.Crossed(), b.PairChecks()
	for k := int64(0); k < 100; k++ {
		x := 500_000_000 + (k%3)*2
		if _, err := b.Move(1, bx(x, x+1, 0, 1)); err != nil {
			t.Fatal(err)
		}
		dc := b.Crossed() - prevC
		dp := b.PairChecks() - prevP
		if dc < 0 || dc > 8 {
			t.Fatalf("refit near cluster %d: crossed delta %d, want <= 8", k, dc)
		}
		if dp < 0 || dp > dc {
			t.Fatalf("refit near cluster %d: pairChecks %d exceeds crossed %d", k, dp, dc)
		}
		prevC, prevP = b.Crossed(), b.PairChecks()
	}
	if got := b.PairChecks(); got > b.Crossed() {
		t.Fatalf("pairChecks %d exceeds crossed %d", got, b.Crossed())
	}
}

// TestConcurrent hammers the maintainer from multiple goroutines; with
// -race this validates the locking, and the final invariants validate
// serializability of the resulting state.
func TestConcurrent(t *testing.T) {
	b := mustNew(t, 3, 200)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 1))
			for i := 0; i < 300; i++ {
				id := int64(1 + rng.Intn(150))
				lx := int64(rng.Intn(40) - 20)
				ly := int64(rng.Intn(40) - 20)
				box := bx(lx, lx+1+int64(rng.Intn(8)), ly, ly+1+int64(rng.Intn(8)))
				switch rng.Intn(4) {
				case 0:
					_, _ = b.Insert(id, box, rng.Intn(4), rng.Intn(4))
				case 1:
					_, _ = b.Move(id, box)
				case 2:
					_, _ = b.Remove(id)
				case 3:
					_, _ = b.SetFilter(id, rng.Intn(4), rng.Intn(4))
				}
			}
		}(g)
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_ = b.Pairs()
				_ = b.Contacts()
				_, _ = b.FatBox(int64(i%150 + 1))
				_, _ = b.Tight(int64(i%150 + 1))
				_ = b.Crossed()
				_ = b.PairChecks()
			}
		}()
	}
	wg.Wait()

	pairs := b.Pairs()
	inPair := make(map[broadphase.Pair]bool, len(pairs))
	for _, p := range pairs {
		inPair[p] = true
		fa, oka := b.FatBox(p.A)
		fb, okb := b.FatBox(p.B)
		if !oka || !okb || !nIntersect(fa, fb) {
			t.Errorf("pair %v present but fat boxes %+v %+v do not intersect", p, fa, fb)
		}
	}
	for _, c := range b.Contacts() {
		if !inPair[c] {
			t.Errorf("contact %v not a broad-phase pair", c)
		}
		ta, oka := b.Tight(c.A)
		tb, okb := b.Tight(c.B)
		if !oka || !okb || !nIntersect(ta, tb) {
			t.Errorf("contact %v present but tight boxes do not intersect", c)
		}
	}
	for id := int64(1); id <= 150; id++ {
		tight, ok := b.Tight(id)
		if !ok {
			continue
		}
		fat, _ := b.FatBox(id)
		if !nContains(fat, tight) {
			t.Errorf("object %d: tight %+v not inside fat %+v", id, tight, fat)
		}
	}
	if b.PairChecks() > b.Crossed() {
		t.Errorf("pairChecks %d exceeds crossed %d", b.PairChecks(), b.Crossed())
	}
}

// TestRandomAgainstNaive replays 2000 random operation sequences and
// after every operation compares events, pair sets, boxes and counters
// with a naive pairwise-comparison reference. Inputs, outputs and the
// comparison basis are logged (visible with -v).
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		margin := int64(rng.Intn(5))
		capacity := 4 + rng.Intn(14)
		b, err := broadphase.New(margin, capacity)
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		n := &naive{margin: margin, cap: capacity, objs: map[int64]*nObj{}}

		mkBox := func() broadphase.Box {
			if rng.Intn(100) < 5 {
				// Occasionally invalid: lo >= hi or coordinate overflow.
				if rng.Intn(2) == 0 {
					x := int64(rng.Intn(20))
					return bx(x+5, x, 0, 1)
				}
				return bx(0, 3_000_000_000, 0, 1)
			}
			lx := int64(rng.Intn(61) - 30)
			ly := int64(rng.Intn(61) - 30)
			return bx(lx, lx+1+int64(rng.Intn(15)), ly, ly+1+int64(rng.Intn(15)))
		}
		mkID := func() int64 {
			if rng.Intn(100) < 3 {
				return 0 // invalid id
			}
			return int64(1 + rng.Intn(14))
		}
		mkFilter := func() int {
			if rng.Intn(100) < 3 {
				return 70000 // invalid filter bits
			}
			return rng.Intn(4)
		}

		checkState := func(step int) {
			t.Helper()
			ctx := fmt.Sprintf("seed=%d step=%d", seed, step)
			if got, want := b.Pairs(), n.pairs(); !equalPairs(got, want) {
				t.Fatalf("%s: Pairs()=%v, naive=%v", ctx, got, want)
			}
			if got, want := b.Contacts(), n.contacts(); !equalPairs(got, want) {
				t.Fatalf("%s: Contacts()=%v, naive=%v", ctx, got, want)
			}
			if got, want := b.Crossed(), n.crossed; got != want {
				t.Fatalf("%s: Crossed()=%d, naive=%d", ctx, got, want)
			}
			if got, want := b.PairChecks(), n.pairChecks; got != want {
				t.Fatalf("%s: PairChecks()=%d, naive=%d", ctx, got, want)
			}
			if b.PairChecks() > b.Crossed() {
				t.Fatalf("%s: pairChecks %d exceeds crossed %d", ctx, b.PairChecks(), b.Crossed())
			}
			inPair := map[broadphase.Pair]bool{}
			for _, p := range b.Pairs() {
				inPair[p] = true
			}
			for _, c := range b.Contacts() {
				if !inPair[c] {
					t.Fatalf("%s: contact %v not a broad-phase pair", ctx, c)
				}
			}
			for id, o := range n.objs {
				fat, ok := b.FatBox(id)
				if !ok || fat != o.fat {
					t.Fatalf("%s: FatBox(%d)=%+v,%v naive=%+v", ctx, id, fat, ok, o.fat)
				}
				tight, ok := b.Tight(id)
				if !ok || tight != o.tight {
					t.Fatalf("%s: Tight(%d)=%+v,%v naive=%+v", ctx, id, tight, ok, o.tight)
				}
				if !nContains(fat, tight) {
					t.Fatalf("%s: object %d tight %+v escapes fat %+v", ctx, id, tight, fat)
				}
			}
		}

		ops := 30 + rng.Intn(60)
		for step := 0; step < ops; step++ {
			id := mkID()
			var desc string
			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14,
				15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29:
				box := mkBox()
				layer, mask := mkFilter(), mkFilter()
				got, gerr := b.Insert(id, box, layer, mask)
				want, wkind := n.insert(id, box, layer, mask)
				desc = fmt.Sprintf("Insert(id=%d box=%+v layer=%d mask=%d)", id, box, layer, mask)
				if gkind := kindOf(gerr); gkind != wkind {
					t.Fatalf("seed=%d step=%d %s: err kind=%q naive=%q", seed, step, desc, gkind, wkind)
				}
				if wkind == "" {
					checkEvents(t, fmt.Sprintf("seed=%d step=%d %s", seed, step, desc), got,
						want.FatEnter, want.FatExit, want.ContactEnter, want.ContactExit)
				}
				t.Logf("seed=%d step=%d in=%s out={kind=%q events=%+v} 判定依据=朴素两两比较", seed, step, desc, wkind, want)
			case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44,
				45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59:
				box := mkBox()
				got, gerr := b.Move(id, box)
				want, wrefit, wkind := n.move(id, box)
				desc = fmt.Sprintf("Move(id=%d box=%+v)", id, box)
				if gkind := kindOf(gerr); gkind != wkind {
					t.Fatalf("seed=%d step=%d %s: err kind=%q naive=%q", seed, step, desc, gkind, wkind)
				}
				if wkind == "" {
					if got.Refit != wrefit {
						t.Fatalf("seed=%d step=%d %s: refit=%v naive=%v", seed, step, desc, got.Refit, wrefit)
					}
					checkEvents(t, fmt.Sprintf("seed=%d step=%d %s", seed, step, desc), got.Events,
						want.FatEnter, want.FatExit, want.ContactEnter, want.ContactExit)
				}
				t.Logf("seed=%d step=%d in=%s out={kind=%q refit=%v events=%+v} 判定依据=朴素两两比较", seed, step, desc, wkind, wrefit, want)
			case 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74:
				got, gerr := b.Remove(id)
				want, wkind := n.remove(id)
				desc = fmt.Sprintf("Remove(id=%d)", id)
				if gkind := kindOf(gerr); gkind != wkind {
					t.Fatalf("seed=%d step=%d %s: err kind=%q naive=%q", seed, step, desc, gkind, wkind)
				}
				if wkind == "" {
					checkEvents(t, fmt.Sprintf("seed=%d step=%d %s", seed, step, desc), got,
						want.FatEnter, want.FatExit, want.ContactEnter, want.ContactExit)
				}
				t.Logf("seed=%d step=%d in=%s out={kind=%q events=%+v} 判定依据=朴素两两比较", seed, step, desc, wkind, want)
			case 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89:
				layer, mask := mkFilter(), mkFilter()
				got, gerr := b.SetFilter(id, layer, mask)
				want, wkind := n.setFilter(id, layer, mask)
				desc = fmt.Sprintf("SetFilter(id=%d layer=%d mask=%d)", id, layer, mask)
				if gkind := kindOf(gerr); gkind != wkind {
					t.Fatalf("seed=%d step=%d %s: err kind=%q naive=%q", seed, step, desc, gkind, wkind)
				}
				if wkind == "" {
					checkEvents(t, fmt.Sprintf("seed=%d step=%d %s", seed, step, desc), got,
						want.FatEnter, want.FatExit, want.ContactEnter, want.ContactExit)
				}
				t.Logf("seed=%d step=%d in=%s out={kind=%q events=%+v} 判定依据=朴素两两比较", seed, step, desc, wkind, want)
			default:
				desc = "Query"
				t.Logf("seed=%d step=%d in=Query out={pairs=%v contacts=%v} 判定依据=朴素两两比较",
					seed, step, b.Pairs(), b.Contacts())
			}
			checkState(step)
		}
		t.Logf("seed=%d done: margin=%d capacity=%d crossed=%d pairChecks=%d pairs=%v contacts=%v",
			seed, margin, capacity, b.Crossed(), b.PairChecks(), b.Pairs(), b.Contacts())
	}
}
