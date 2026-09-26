package api_test

import (
	"errors"
	"ontology/api"
	"ontology/rsv"
	"sync"
	"testing"
)

type lcg struct{ v int64 }
type rec struct{ s, e, nd, id int64 }

func (g *lcg) n(k int64) int64 { g.v = (g.v*1103515245 + 12345) & 0x7fffffff; return g.v % k }

func chk(t *testing.T, c bool, m string) {
	t.Helper()
	if !c {
		t.Error(m)
	}
}

// npk is the literal point-by-point O(points*n) naive reference.
func npk(rs []rec, a, b int64) (pk int64) {
	for x := a; x < b; x++ {
		var l int64
		for _, r := range rs {
			if r.s <= x && x < r.e {
				l += r.nd
			}
		}
		pk = max(pk, l)
	}
	return
}

// TestReserveMatchesNaiveSweep pins invariant 1 on random sequences incl. releases.
func TestReserveMatchesNaiveSweep(t *testing.T) {
	for _, c := range [][4]int64{{1, 10, 20, 500}, {2, 1, 8, 500}, {3, 50, 40, 800}, {4, 7, 12, 600}} {
		g := lcg{c[0]}
		s, _ := api.New(c[1])
		var rs []rec
		for k := int64(0); k < c[3]; k++ {
			if len(rs) > 0 && g.n(4) == 0 {
				j := int(g.n(int64(len(rs))))
				chk(t, s.Release(rs[j].id) == nil, "release")
				rs = append(rs[:j], rs[j+1:]...)
				continue
			}
			a, b := g.n(c[2]), g.n(c[2])
			if a >= b {
				continue
			}
			n := 1 + g.n(c[1])
			id, ok, _ := s.Reserve(a, b, n)
			chk(t, ok == (npk(rs, a, b)+n <= c[1]), "decision differs from naive sweep")
			if ok {
				rs = append(rs, rec{a, b, n, id})
			}
		}
	}
}

// TestCapacityInvariant pins invariant 2 after every accepted operation.
func TestCapacityInvariant(t *testing.T) {
	for _, c := range []int64{1, 3, 10, 33} {
		s, _ := api.New(c)
		g := lcg{c*7 + 1}
		for k := 0; k < 700; k++ {
			a, b := g.n(30), g.n(30)
			if a < b {
				s.Reserve(a, b, 1+g.n(c))
				chk(t, s.Peak(0, 30) <= c, "capacity exceeded")
			}
		}
		chk(t, s.SelfCheck() == nil, "built-in SelfCheck failed")
	}
}

// TestReleaseRemovesReservation pins invariant 3.
func TestReleaseRemovesReservation(t *testing.T) {
	s, _ := api.New(5)
	id, ok, _ := s.Reserve(0, 4, 5)
	chk(t, ok, "first reserve should fit")
	_, ok, _ = s.Reserve(0, 4, 1)
	chk(t, !ok, "overlapping reserve should be rejected")
	chk(t, s.Release(id) == nil, "release")
	_, ok, _ = s.Reserve(0, 4, 5)
	chk(t, ok, "released interval must carry no load and fit again")
}

// TestRejectionLeavesNoTrace pins invariant 4 and the four distinct sentinels.
func TestRejectionLeavesNoTrace(t *testing.T) {
	s, _ := api.New(5)
	s.Reserve(0, 4, 3)
	before := s.Active()
	calls := []struct {
		want error
		f    func() error
	}{
		{api.ErrConfig, func() error { _, e := api.New(0); return e }},
		{rsv.ErrNeed, func() error { _, _, e := s.Reserve(0, 1, 0); return e }},
		{rsv.ErrNeed, func() error { _, _, e := s.Reserve(0, 1, 6); return e }},
		{rsv.ErrInterval, func() error { _, _, e := s.Reserve(3, 3, 1); return e }},
		{rsv.ErrRelease, func() error { return s.Release(1 << 40) }},
	}
	for _, c := range calls {
		chk(t, errors.Is(c.f(), c.want), "wrong sentinel")
		chk(t, s.Active() == before, "case changed the active set")
	}
	distinct := api.ErrConfig != rsv.ErrNeed && api.ErrConfig != rsv.ErrInterval &&
		api.ErrConfig != rsv.ErrRelease && rsv.ErrNeed != rsv.ErrInterval &&
		rsv.ErrNeed != rsv.ErrRelease && rsv.ErrInterval != rsv.ErrRelease
	chk(t, distinct, "the four sentinels must be pairwise distinct")
	_, ok, _ := s.Reserve(4, 8, 5)
	chk(t, ok, "scheduler must stay usable after rejections")
}

// TestConcurrentReserve: N goroutines reserve disjoint need-1 intervals while
// readers sample the peak; barrier synchronization only, never time-based.
func TestConcurrentReserve(t *testing.T) {
	const N = 200
	s, _ := api.New(4)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for s.Active() != N {
				if s.Peak(0, 2*N) > 4 {
					t.Error("peak exceeded mid-run")
				}
			}
		}()
	}
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, ok, _ := s.Reserve(int64(2*i), int64(2*i+1), 1)
			chk(t, ok, "disjoint reserve rejected")
		}(i)
	}
	close(start)
	wg.Wait()
	chk(t, s.Active() == N, "Active mismatch")
}
