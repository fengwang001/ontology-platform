package join_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/join"
	"ontology/rel"
)

func tp(x, y int) rel.Tuple { return rel.Tuple{X: x, Y: y} }
func rep(j *join.Join, tab string, k int, tu rel.Tuple) {
	for i := 0; i < k; i++ {
		_ = j.Insert(tab, tu)
	}
}
func nres(j *join.Join) (n int) {
	for _, c := range j.Result() {
		n += c
	}
	return
}
func mustIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
func mustTrue(t *testing.T, cond bool, msg string, args ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(msg, args...)
	}
}

func naive(r, s, t map[[2]int]int) map[join.Quad]int {
	o := map[join.Quad]int{}
	for rr, cr := range r {
		for ss, cs := range s {
			if rr[1] != ss[0] || cr == 0 || cs == 0 {
				continue
			}
			for tt, ct := range t {
				if ss[1] == tt[0] && ct > 0 {
					o[join.Quad{A: rr[0], B: rr[1], C: ss[1], D: tt[1]}] += cr * cs * ct
				}
			}
		}
	}
	return o
}

func TestEightSteps(t *testing.T) {
	names := []string{"R", "S", "T"}
	ops := [][4]int{{1, 1, 10, 0}, {2, 10, 100, 0}, {0, 5, 1, 0}, {1, 1, 10, 0},
		{0, 6, 1, 0}, {0, 5, 1, 1}, {1, 1, 10, 1}, {1, 1, 10, 1}}
	q5, q6 := join.Quad{A: 5, B: 1, C: 10, D: 100}, join.Quad{A: 6, B: 1, C: 10, D: 100}
	wants := []map[join.Quad]int{{}, {}, {q5: 1}, {q5: 2}, {q5: 2, q6: 2}, {q6: 2}, {q6: 1}, {}}
	j := join.New(0)
	for i, p := range ops {
		fn := j.Insert
		if p[3] == 1 {
			fn = j.Delete
		}
		if e := fn(names[p[0]], tp(p[1], p[2])); e != nil {
			t.Fatalf("step %d: %v", i+1, e)
		}
		mustTrue(t, nres(j) == []int{0, 0, 1, 2, 4, 2, 1, 0}[i] && reflect.DeepEqual(j.Result(), wants[i]),
			"step %d: got=%v want=%v", i+1, j.Result(), wants[i])
	}
}

func TestMultisetProduct(t *testing.T) {
	q := join.Quad{A: 5, B: 1, C: 10, D: 100}
	for _, c := range []struct{ rC, sC, tC, w int }{
		{1, 1, 1, 1}, {2, 3, 1, 6}, {2, 3, 2, 12},
	} {
		j := join.New(0)
		rep(j, "R", c.rC, tp(5, 1))
		rep(j, "S", c.sC, tp(1, 10))
		rep(j, "T", c.tC, tp(10, 100))
		mustTrue(t, j.Result()[q] == c.w, "got %d want %d", j.Result()[q], c.w)
		_ = j.Delete("R", tp(5, 1))
		mustTrue(t, j.Result()[q] == c.w-c.sC*c.tC, "after delete got %d", j.Result()[q])
	}
}

func TestEquivalentToBatchRecompute(t *testing.T) {
	j := join.New(0)
	ref := map[string]map[[2]int]int{"R": {}, "S": {}, "T": {}}
	tabs, x := []string{"R", "S", "T"}, 1
	rnd := func(n int) int { x = (x*1103515245 + 12345) & 0x7fffffff; return x % n }
	for i := 0; i < 600; i++ {
		tab, key := tabs[rnd(3)], [2]int{rnd(4), rnd(4)}
		e := j.Insert(tab, tp(key[0], key[1]))
		ref[tab][key]++
		if rnd(3) == 0 && ref[tab][key] > 1 {
			e, ref[tab][key] = j.Delete(tab, tp(key[0], key[1])), ref[tab][key]-1
		}
		mustTrue(t, e == nil && reflect.DeepEqual(j.Result(), naive(ref["R"], ref["S"], ref["T"])),
			"step %d: e=%v got=%v", i, e, j.Result())
	}
}

func TestRejectedOpsAtomic(t *testing.T) {
	j := join.New(0)
	mustIs(t, j.Insert("X", tp(1, 1)), join.ErrBadTable)
	mustIs(t, j.Delete("R", tp(1, 1)), rel.ErrNotFound)
	mustTrue(t, nres(j) == 0, "trace after reject")
	z := join.New(1)
	_ = z.Insert("S", tp(1, 2))
	_ = z.Insert("T", tp(2, 3))
	_ = z.Insert("R", tp(1, 1))
	snap := z.Result()
	mustIs(t, z.Insert("R", tp(2, 1)), join.ErrTooMany)
	mustTrue(t, reflect.DeepEqual(z.Result(), snap), "trace after over-limit")
	_ = z.Delete("R", tp(1, 1))
	_ = z.Insert("R", tp(2, 1))
	mustTrue(t, nres(z) == 1, "unusable after reject")
}

func TestConcurrentMatchesSerial(t *testing.T) {
	const nG, nPer = 16, 200
	serial, par := join.New(0), join.New(0)
	for _, j := range []*join.Join{serial, par} {
		_ = j.Insert("S", tp(1, 7))
		_ = j.Insert("T", tp(7, 9))
	}
	var wg sync.WaitGroup
	for g := range nG {
		wg.Go(func() {
			for i := range nPer {
				_ = par.Insert("R", tp(g*nPer+i, 1))
			}
		})
	}
	wg.Wait()
	for a := range nG * nPer {
		_ = serial.Insert("R", tp(a, 1))
	}
	mustTrue(t, nres(par) == nG*nPer && reflect.DeepEqual(par.Result(), serial.Result()),
		"n=%d par=%v serial=%v", nres(par), par.Result(), serial.Result())
}

func TestSelfCheck(t *testing.T) { mustIs(t, api.New(0).SelfCheck(), nil) }
