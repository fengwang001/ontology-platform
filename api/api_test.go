package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

var vn = []string{"V1", "V2", "V3"}
var bn = []string{"B1", "B2", "B3"}
var names6 = []string{"B1", "B2", "B3", "V1", "V2", "V3"}

func ck(t *testing.T, cond bool, f string, a ...any) {
	if !cond {
		t.Fatalf(f, a...)
	}
}

func naiveOK(e *Engine) bool {
	b1, _ := e.Value("B1")
	b2, _ := e.Value("B2")
	b3, _ := e.Value("B3")
	want := map[string]int{"V1": b1 + b2, "V2": b2 + b3, "V3": b1 + 2*b2 + b3}
	for _, v := range vn {
		gv, _ := e.Value(v)
		if !e.IsStale(v) && gv != want[v] {
			return false
		}
	}
	return true
}

func snap(e *Engine) string {
	s := ""
	for _, n := range names6 {
		v, _ := e.Value(n)
		s += fmt.Sprintf("%s%d%v", n, v, e.IsStale(n))
	}
	return s
}

// TestNaiveConsistency pins invariant 1 over random sequences/orders (I1).
func TestNaiveConsistency(t *testing.T) {
	for _, seed := range []int{1, 2, 3, 42, 1000} {
		e, r := New(), rand.New(rand.NewSource(int64(seed)))
		for n := 0; n < 500; n++ {
			if r.Intn(2) == 0 {
				_ = e.UpdateBase(bn[r.Intn(3)], r.Intn(200)-50)
			} else {
				_ = e.Refresh(vn[r.Intn(3)])
			}
			ck(t, naiveOK(e), "seed=%d step=%d not naive", seed, n)
		}
	}
	ck(t, New().SelfCheck() == nil, "SelfCheck failed")
}

// TestStaleness pins invariant 2 on the prescribed sequence (I2).
func TestStaleness(t *testing.T) {
	ops := [7][3]int{{1, 1, 25}, {3, 0, 0}, {4, 0, 0}, {5, 0, 0}, {0, 1, 100}, {3, 0, 0}, {5, 0, 0}}
	want := [7][3]bool{{true, true, true}, {false, true, true}, {false, false, true}, {false, false, false}, {true, false, true}, {false, false, true}, {false, false, false}}
	e := New()
	for i, o := range ops {
		err := e.Refresh(names6[o[0]])
		if o[1] == 1 {
			err = e.UpdateBase(names6[o[0]], o[2])
		}
		ck(t, err == nil, "step %d: %v", i+1, err)
		for j, v := range vn {
			ck(t, e.IsStale(v) == want[i][j], "step %d %s stale=%v want %v", i+1, v, e.IsStale(v), want[i][j])
		}
	}
	for _, b := range bn {
		ck(t, !e.IsStale(b), "base %s stale", b)
	}
	ck(t, !e.IsStale("nope"), "unknown reported stale")
}

// TestDependencyOrder pins invariant 3: readiness gate + current values (I3).
func TestDependencyOrder(t *testing.T) {
	cases := [][3]int{{0, 100, 170}, {1, 25, 90}, {2, 7, 57}}
	for _, c := range cases {
		e := New()
		ck(t, e.UpdateBase(bn[c[0]], c[1]) == nil, "update")
		err := e.Refresh("V3")
		ck(t, errors.Is(err, ErrDepsNotReady), "premature V3 err=%v", err)
		v, _ := e.Value("V3")
		ck(t, v == 80, "rejected refresh changed V3 to %d", v)
		ck(t, e.Refresh("V1") == nil, "R V1")
		ck(t, e.Refresh("V2") == nil, "R V2")
		ck(t, e.Refresh("V3") == nil, "R V3 after ready")
		v, _ = e.Value("V3")
		ck(t, v == c[2], "%s: V3=%d want %d", bn[c[0]], v, c[2])
	}
}

// TestErrorsDistinctAndAtomic pins invariant 4: distinct errors, no trace (I4).
func TestErrorsDistinctAndAtomic(t *testing.T) {
	e := New()
	before := snap(e)
	got := []error{e.UpdateBase("X", 1), e.UpdateBase("V1", 1), e.Refresh("B1"), e.Refresh("X")}
	want := []error{ErrNotFound, ErrUpdateView, ErrRefreshBase, ErrNotFound}
	for i := range got {
		ck(t, errors.Is(got[i], want[i]), "case %d got %v want %v", i, got[i], want[i])
	}
	ck(t, snap(e) == before, "rejected ops changed state:\n%s\n%s", before, snap(e))
	ck(t, e.UpdateBase("B2", 25) == nil, "update B2")
	ck(t, errors.Is(e.Refresh("V3"), ErrDepsNotReady), "V3 should be not-ready")
	v, _ := e.Value("V3")
	ck(t, v == 80, "rejected Refresh changed V3 to %d", v)
	ck(t, e.Refresh("V1") == nil, "engine unusable after rejects")
}

// TestConcurrentReaders pins race-free, identical concurrent reads (no sleep).
func TestConcurrentReaders(t *testing.T) {
	e := New()
	ck(t, e.UpdateBase("B2", 40) == nil, "update")
	for _, v := range vn {
		ck(t, e.Refresh(v) == nil, "refresh %s", v)
	}
	const N = 64
	type res struct{ v1, v2, v3 int }
	ch := make(chan res, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := res{}
			r.v1, _ = e.Value("V1")
			r.v2, _ = e.Value("V2")
			r.v3, _ = e.Value("V3")
			for _, v := range vn {
				if e.IsStale(v) {
					t.Errorf("%s unexpectedly stale", v)
				}
			}
			ch <- r
		}()
	}
	wg.Wait()
	close(ch)
	first := <-ch
	ck(t, first == (res{50, 70, 120}), "first %+v", first)
	for r := range ch {
		ck(t, r == first, "diverged %+v", r)
	}
}
