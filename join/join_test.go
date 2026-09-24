package join

import (
	"errors"
	"maps"
	"math/rand"
	"ontology/rel"
	"sync"
	"testing"
)

func chk(t *testing.T, ok bool, f string, a ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(f, a...)
	}
}
func insN(t *testing.T, w *DB, tab string, x rel.Tuple, n int) {
	t.Helper()
	for range n {
		chk(t, w.Insert(tab, x) == nil, "insert %s %v", tab, x)
	}
}
func TestEightSteps(t *testing.T) {
	w := New(0)
	for i, op := range eightSteps {
		fn := w.Insert
		if op.del {
			fn = w.Delete
		}
		chk(t, fn(op.tab, op.t) == nil, "step %d", i+1)
		r := w.Result()
		chk(t, totalCopies(r) == op.wantLen, "step %d: %d copies, want %d", i+1, totalCopies(r), op.wantLen)
		switch i {
		case 3:
			chk(t, r[Quad{5, 1, 10, 100}] == 2, "step 4 multiplicity %d", r[Quad{5, 1, 10, 100}])
		case 5:
			chk(t, r[Quad{5, 1, 10, 100}] == 0 && r[Quad{6, 1, 10, 100}] == 2, "step 6 cascade: %v", r)
		case 6:
			chk(t, r[Quad{6, 1, 10, 100}] == 1 && w.s.Count(tp(1, 10)) == 1, "step 7 copy: %v", r)
		}
	}
}
func TestMultiplicity(t *testing.T) {
	w := New(0)
	insN(t, w, "S", tp(1, 10), 3)
	insN(t, w, "T", tp(10, 100), 2)
	insN(t, w, "R", tp(5, 1), 4)
	chk(t, w.Result()[Quad{5, 1, 10, 100}] == 24, "3*2*4 = %d", w.Result()[Quad{5, 1, 10, 100}])
	chk(t, w.Insert("R", tp(6, 1)) == nil, "insert second R")
	chk(t, w.Result()[Quad{6, 1, 10, 100}] == 6, "second R = %d", w.Result()[Quad{6, 1, 10, 100}])
	chk(t, w.Delete("S", tp(1, 10)) == nil, "delete one S")
	chk(t, w.s.Count(tp(1, 10)) == 2 && w.Result()[Quad{5, 1, 10, 100}] == 16, "single delete: %v", w.Result())
}
func TestCascadeDelete(t *testing.T) {
	w := New(0)
	for _, s := range []st{{false, "S", tp(1, 10), 0}, {false, "T", tp(10, 100), 0}, {false, "T", tp(10, 200), 0}, {false, "R", tp(5, 1), 0}, {false, "R", tp(7, 1), 0}} {
		chk(t, w.Insert(s.tab, s.t) == nil, "setup")
	}
	chk(t, w.Delete("R", tp(5, 1)) == nil, "delete R(5,1)")
	r := w.Result()
	chk(t, totalCopies(r) == 2 && r[Quad{5, 1, 10, 100}] == 0 && r[Quad{7, 1, 10, 200}] == 1, "cascade: %v", r)
}
func TestRandomVsBruteForce(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		w := New(0)
		g := map[string]map[rel.Tuple]int{"R": {}, "S": {}, "T": {}}
		rng := rand.New(rand.NewSource(seed))
		for n := 0; n < 3000; n++ {
			tab := []string{"R", "S", "T"}[rng.Intn(3)]
			x := rel.Tuple{X: rng.Intn(6) - 2, Y: rng.Intn(6) - 2}
			switch {
			case rng.Intn(3) == 0 && g[tab][x] > 0:
				chk(t, w.Delete(tab, x) == nil, "delete")
				g[tab][x]--
			case g[tab][x] == 0 && rng.Intn(2) == 0:
				chk(t, errors.Is(w.Delete(tab, x), ErrNotFound), "seed %d op %d", seed, n)
			default:
				chk(t, w.Insert(tab, x) == nil, "insert")
				g[tab][x]++
			}
			chk(t, maps.Equal(w.Result(), bruteForce(g["R"], g["S"], g["T"])), "seed %d op %d drift", seed, n)
		}
	}
}
func TestSentinelErrors(t *testing.T) {
	w := New(0)
	chk(t, errors.Is(w.Insert("Q", tp(0, 0)), ErrBadTable), "bad table")
	chk(t, errors.Is(w.Delete("R", tp(1, 1)), ErrNotFound), "missing tuple")
	l := New(1)
	for _, s := range []st{{false, "S", tp(1, 10), 0}, {false, "T", tp(10, 100), 0}, {false, "R", tp(1, 1), 0}} {
		chk(t, l.Insert(s.tab, s.t) == nil, "limit setup")
	}
	chk(t, errors.Is(l.Insert("R", tp(2, 1)), ErrLimit), "limit")
	for _, p := range [][2]error{{ErrBadTable, ErrNotFound}, {ErrBadTable, ErrLimit}, {ErrNotFound, ErrLimit}} {
		chk(t, !errors.Is(p[0], p[1]), "sentinel errors must be distinct")
	}
}
func TestRejectionAtomic(t *testing.T) {
	w := New(3)
	chk(t, w.Insert("S", tp(1, 10)) == nil, "setup S")
	chk(t, w.Insert("T", tp(10, 100)) == nil, "setup T")
	insN(t, w, "R", tp(5, 1), 3)
	snap := w.Result()
	for _, f := range []func() error{
		func() error { return w.Insert("R", tp(6, 1)) },
		func() error { return w.Delete("R", tp(9, 9)) },
		func() error { return w.Insert("X", tp(0, 0)) },
	} {
		chk(t, f() != nil && maps.Equal(w.Result(), snap) && w.r.Count(tp(6, 1)) == 0, "rejected op left a trace")
	}
	chk(t, w.Insert("R", tp(8, 9)) == nil, "usable after rejection")
}
func TestProbeComplexity(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		w := New(0)
		for i := range m {
			chk(t, w.Insert("S", tp(i, i)) == nil, "S")
			chk(t, w.Insert("T", tp(m+i, i)) == nil, "T")
		}
		chk(t, w.Insert("R", tp(0, m)) == nil, "R")
		chk(t, w.lastProbes == 0, "m=%d probed %d pairs", m, w.lastProbes)
	}
	w := New(0)
	for i := range 5 {
		chk(t, w.Insert("S", tp(1, 2+i)) == nil, "S")
		chk(t, w.Insert("T", tp(2+i, 9)) == nil, "T")
	}
	chk(t, w.Insert("R", tp(7, 1)) == nil, "R")
	chk(t, totalCopies(w.Result()) == 5 && w.lastProbes <= 6, "k-match: %v probes %d", w.Result(), w.lastProbes)
}
func TestConcurrent(t *testing.T) {
	const n = 200
	w, seq := New(0), New(0)
	for _, db := range []*DB{w, seq} {
		chk(t, db.Insert("S", tp(1, 1)) == nil, "S")
		chk(t, db.Insert("T", tp(1, 1)) == nil, "T")
	}
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = w.Insert("R", rel.Tuple{X: i, Y: 1}) }(i)
	}
	wg.Wait()
	for i := range n {
		chk(t, seq.Insert("R", tp(i, 1)) == nil, "seq R")
	}
	chk(t, maps.Equal(w.Result(), seq.Result()), "concurrent result differs")
}
func TestSelfCheck(t *testing.T) { chk(t, New(0).SelfCheck() == nil, "selfcheck failed") }
