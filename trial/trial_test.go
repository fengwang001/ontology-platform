package trial

import (
	"errors"
	"reflect"
	"testing"

	"ontology/refc"
)

// assertInv checks invariants 1 (rc conservation) and 3 (no dangling); reach also checks 2 (live == naive reachable).
func assertInv(t *testing.T, g *Graph, reach bool) {
	sn := g.h.Snapshot()
	alive, indeg := map[refc.Obj]bool{}, map[refc.Obj]int{}
	for _, s := range sn {
		alive[s.O], indeg[s.Child] = true, indeg[s.Child]+1
	}
	for _, s := range sn {
		if s.Child != 0 && !alive[s.Child] {
			t.Fatalf("dangling %d->%d", s.O, s.Child)
		}
		if s.Roots < 0 || s.Fields != indeg[s.O] {
			t.Fatalf("rc at %d: r=%d f=%d in=%d", s.O, s.Roots, s.Fields, indeg[s.O])
		}
	}
	if !reach {
		return
	}
	seen := map[refc.Obj]bool{}
	var st []refc.Obj
	for _, s := range sn {
		if s.Roots > 0 {
			seen[s.O], st = true, append(st, s.O)
		}
	}
	for len(st) > 0 {
		x := st[len(st)-1]
		st = st[:len(st)-1]
		if c := g.h.Child(x); c != 0 && alive[c] && !seen[c] {
			seen[c], st = true, append(st, c)
		}
	}
	if len(seen) != len(sn) {
		t.Fatalf("Collect live=%d naive reachable=%d", len(sn), len(seen))
	}
}

func randomGraph(t *testing.T, seed uint64, ops int) {
	g := New(1 << 20)
	var pool []refc.Obj
	for i := 0; i < ops; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		switch r := seed % 100; {
		case r < 45:
			if o, err := g.Root(); err == nil {
				pool = append(pool, o)
			}
		case r < 80 && len(pool) > 1:
			_ = g.Point(pool[seed%uint64(len(pool))], pool[(seed>>8)%uint64(len(pool))])
		case r < 90 && len(pool) > 0:
			_ = g.Unroot(pool[seed%uint64(len(pool))])
		default:
			g.Collect()
			assertInv(t, g, true)
		}
		assertInv(t, g, false)
	}
	g.Collect()
	assertInv(t, g, true)
}
func randomSeeds(t *testing.T, seeds []uint64, ops int) {
	for _, s := range seeds {
		randomGraph(t, s, ops)
	}
}
func TestEightStepTable(t *testing.T) {
	g := New(1 << 20)
	var A, B refc.Obj // dead or unborn objects read as rc 0
	want := [8][2]int{{1, 0}, {1, 1}, {1, 2}, {2, 2}, {1, 2}, {1, 1}, {0, 0}, {0, 0}}
	pair := func() [2]int { return [2]int{g.h.RC(A), g.h.RC(B)} }
	for i, op := range []func(){
		func() { A, _ = g.Root() },
		func() { B, _ = g.Root() },
		func() { _ = g.Point(A, B) },
		func() { _ = g.Point(B, A) },
		func() { _ = g.Unroot(A) },
		func() { _ = g.Unroot(B) },
	} {
		op()
		if p := pair(); p != want[i] {
			t.Fatalf("row %d: %v want %v", i+1, p, want[i])
		}
	}
	if f := g.Collect(); f != 2 { // step 7: the A<->B cycle is reclaimed
		t.Fatalf("step 7 freed %d, want 2", f)
	}
	if p1, p2 := pair(), pair(); p1 != want[6] || p2 != want[7] {
		t.Fatalf("post-collect rows: %v %v", p1, p2)
	}
}
func TestRefCountConservation(t *testing.T) { randomSeeds(t, []uint64{1, 2, 7, 42, 99}, 500) }
func TestNoDangling(t *testing.T)           { randomSeeds(t, []uint64{3, 17, 88}, 300) }
func TestReachabilityVsNaive(t *testing.T)  { randomSeeds(t, []uint64{5, 13, 1009}, 600) }

// TestRejectedOpsLeaveState pins invariant 4: rejected ops mutate nothing.
func TestRejectedOpsLeaveState(t *testing.T) {
	mk := func(g *Graph) refc.Obj { o, _ := g.Root(); return o }
	cases := []struct {
		setup func(*Graph)
		op    func(*Graph) error
		want  error
	}{
		{func(g *Graph) {}, func(g *Graph) error { return g.Point(9999, 0) }, ErrUnknownObject},
		{func(g *Graph) { mk(g) }, func(g *Graph) error { return g.Point(1, 4242) }, ErrUnknownObject},
		{func(g *Graph) { _ = g.Unroot(mk(g)) }, func(g *Graph) error { return g.Unroot(1) }, ErrUnknownObject},
		{func(g *Graph) { _ = g.Point(mk(g), mk(g)); _ = g.Unroot(2) }, func(g *Graph) error { return g.Unroot(2) }, ErrNoRoot},
	}
	for _, tc := range cases {
		g := New(1 << 20)
		tc.setup(g)
		before := g.h.Snapshot()
		if err := tc.op(g); !errors.Is(err, tc.want) {
			t.Fatalf("got %v want %v", err, tc.want)
		}
		if after := g.h.Snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatal("rejected op mutated state")
		}
	}
	g0 := New(1)
	_, _ = g0.Root()
	_, err := g0.Root()
	if !errors.Is(err, ErrLimit) || errors.Is(ErrNoRoot, ErrLimit) || errors.Is(ErrNoRoot, ErrUnknownObject) {
		t.Fatalf("limit or sentinel distinctness wrong: %v", err)
	}
}

func TestPointChecksConstant(t *testing.T) {
	base := -1
	for _, m := range []int{100, 1000, 10000} {
		g := New(1 << 30)
		for i := 0; i < m; i++ {
			_, _ = g.Root()
		}
		_ = g.Point(1, 2)
		if g.pointChecks > 3 || (base >= 0 && g.pointChecks != base) {
			t.Fatalf("m=%d checks=%d base=%d", m, g.pointChecks, base)
		}
		base = g.pointChecks
	}
}
