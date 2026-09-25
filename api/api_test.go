package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/csync"
	"ontology/marz"
)

type clk struct {
	id          string
	offset, err int64
}
type scenario struct {
	f      int
	clocks []clk
}

func build(t *testing.T, f int, clocks []clk) *api.System {
	sys, _ := api.New(f)
	for _, c := range clocks {
		if err := sys.Add(c.id, c.offset, c.err); err != nil {
			t.Fatalf("add %s: %v", c.id, err)
		}
	}
	return sys
}
func scenarios() (out []scenario) {
	rng := rand.New(rand.NewSource(7))
	out = []scenario{{1, []clk{{"A", 10, 2}, {"B", 11, 1}, {"C", 20, 1}}}}
	for _, m := range []int{2, 5, 30, 100} {
		for f := 0; f <= 2; f++ {
			cs := make([]clk, m)
			for i := range cs {
				cs[i] = clk{string(rune(i)), rng.Int63n(200) - 100, rng.Int63n(30)}
			}
			out = append(out, scenario{f, cs})
		}
	}
	return out
}

// naive is the from-scratch sweep recomputation used as reference.
func naive(cs []clk, need int) (int64, int64, bool) {
	eps := []marz.Endpoint{}
	for _, c := range cs {
		l, r := marz.Endpoints(c.offset, c.err)
		eps = append(eps, l, r)
	}
	marz.SortEndpoints(eps)
	return marz.Sweep(eps, need)
}
func TestConsensusMatchesNaive(t *testing.T) { // Invariant 1
	for _, sc := range scenarios() {
		lo, hi, err := build(t, sc.f, sc.clocks).Consensus()
		nlo, nhi, ok := naive(sc.clocks, len(sc.clocks)-sc.f)
		if ok != (err == nil) || (ok && (lo != nlo || hi != nhi)) {
			t.Errorf("f=%d K=%d: got [%d,%d,%v], naive [%d,%d,%v]", sc.f, len(sc.clocks), lo, hi, err, nlo, nhi, ok)
		}
	}
}
func TestBoundaryClosure(t *testing.T) { // Invariant 2
	for _, sc := range scenarios() {
		sys := build(t, sc.f, sc.clocks)
		lo, hi, err := sys.Consensus()
		if err != nil {
			continue
		}
		for _, p := range []int64{lo, hi} {
			boundary := false
			for _, c := range sc.clocks {
				boundary = boundary || p == c.offset-c.err || p == c.offset+c.err
			}
			if !boundary || sys.CountAt(p) < len(sc.clocks)-sc.f {
				t.Errorf("f=%d K=%d: endpoint %d boundary=%v count=%d", sc.f, len(sc.clocks), p, boundary, sys.CountAt(p))
			}
		}
	}
}
func TestRejectedOpsLeaveStateUnchanged(t *testing.T) { // Invariant 4
	sys := build(t, 1, []clk{{"A", 10, 2}, {"B", 11, 1}, {"C", 20, 1}})
	empty, _ := api.New(0)
	_, _, eFew := empty.Consensus()
	full := build(t, 3, []clk{{"X", 0, 1}, {"Y", 1, 1}, {"Z", 2, 1}})
	_, _, eBigF := full.Consensus() // f == K
	_, eF := api.New(-1)
	errs := []error{eF, sys.Add("NEG", 0, -1), sys.Add("A", 0, 0), eFew}
	cats := []error{api.ErrInvalidF, csync.ErrNegativeError, csync.ErrDuplicateID, api.ErrTooFewClocks}
	for i, e := range errs { // each matches its own category and no other
		for j, c := range cats {
			if (i == j) != errors.Is(e, c) {
				t.Fatalf("op %d: error %v mismatches category %d", i, e, j)
			}
		}
	}
	if !errors.Is(eBigF, api.ErrInvalidF) {
		t.Fatal("f == K not reported as invalid f")
	}
	lo1, hi1, err := sys.Consensus()
	if err != nil || lo1 != 10 || hi1 != 12 || sys.CountAt(11) != 2 || sys.CountAt(0) != 0 {
		t.Fatal("rejected operations mutated state")
	}
	if err := sys.Add("D", 30, 1); err != nil { // still usable afterwards
		t.Fatal("system unusable after rejections")
	}
}
func TestConcurrentConsistent(t *testing.T) {
	clocks := scenarios()[12].clocks
	sys := build(t, 1, clocks)
	lo0, hi0, err0 := sys.Consensus()
	c0 := sys.CountAt(lo0)
	var bad atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		wg.Go(func() {
			<-start
			for i := 0; i < 20; i++ {
				lo, hi, err := sys.Consensus()
				if err != err0 || lo != lo0 || hi != hi0 || sys.CountAt(lo0) != c0 || sys.SelfCheck() != nil {
					bad.Add(1)
				}
			}
		})
	}
	close(start)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatal("concurrent reads diverged")
	}
	par, _ := api.New(1)
	start = make(chan struct{})
	for _, c := range clocks {
		wg.Go(func() {
			<-start
			par.Add(c.id, c.offset, c.err)
		})
	}
	close(start)
	wg.Wait()
	pl, ph, _ := par.Consensus()
	if sl, sh, _ := naive(clocks, len(clocks)-1); pl != sl || ph != sh {
		t.Fatalf("concurrent adds [%d,%d] != serial [%d,%d]", pl, ph, sl, sh)
	}
}
