package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/adapt"
	"ontology/api"
)

func chk(t *testing.T, ok bool, msg string, a ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(msg, a...)
	}
}

func TestNineSteps(t *testing.T) {
	tss := []int64{10, 11, 9, 5, 6, 15, 16, 17, 18}
	late := []bool{false, false, false, true, true, false, false, false, false}
	want := [][2]int64{{8, 2}, {9, 2}, {9, 2}, {9, 2}, {9, 2}, {13, 4}, {12, 4}, {13, 4}, {14, 2}}
	s, _ := api.New(2, 10, 2, 3, 2, 0)
	for i, ts := range tss {
		chk(t, s.Feed(ts) == late[i] && s.WM() == want[i][0] && s.Delay() == want[i][1], "step %d", i+1)
	}
	sb, _ := api.New(2, 10, 2, 100, 50, 0) // -inf wm never late; ts == wm on time
	for i, c := range [][3]int64{{-1000, 0, -1002}, {10, 0, 8}, {8, 0, 8}, {7, 1, 8}} {
		chk(t, sb.Feed(c[0]) == (c[1] == 1) && sb.WM() == c[2], "boundary %d", i)
	}
	chk(t, s.SelfCheck() == nil, "selfcheck")
}

// TestFixedDelayMatchesNaive pins invariant 1: frozen delay under random
// orders (min==max) and loop-built neutral windows with normal parameters.
func TestFixedDelayMatchesNaive(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		r := rand.New(rand.NewSource(seed))
		s, _ := api.New(2, 2, 2, 4, 4, 0)
		var maxSeen int64
		for i := 0; i < 200; i++ {
			ts := r.Int63n(1000) - 500
			chk(t, s.Feed(ts) == (i > 0 && ts < maxSeen-2), "seed %d event %d", seed, i)
			maxSeen = max(maxSeen, ts)
		}
	}
	s, _ := api.New(2, 10, 2, 4, 4, 0) // one late event per window of four
	var maxSeen int64
	first := true
	for blk := 0; blk < 10; blk++ {
		b := int64(blk * 20)
		for j, ts := range [4]int64{b + 100, b + 70, b + 101, b + 102} {
			naive := !first && ts < maxSeen-2
			chk(t, s.Feed(ts) == naive && s.Delay() == 2, "blk %d ev %d", blk, j)
			first, maxSeen = false, max(maxSeen, ts)
		}
	}
}

// TestDelayBounded pins invariant 2 across configs and random arrival orders.
func TestDelayBounded(t *testing.T) {
	for ci, c := range [][6]int64{{0, 1, 1, 1, 1, 0}, {2, 10, 2, 3, 2, 0},
		{5, 5, 5, 7, 6, 1}, {0, 100, 7, 13, 9, 3}} {
		s, _ := api.New(c[0], c[1], c[2], c[3], c[4], c[5])
		r := rand.New(rand.NewSource(int64(ci) + 99))
		for i := 0; i < 1000; i++ {
			s.Feed(r.Int63n(2000) - 1000)
			chk(t, s.Delay() >= c[0] && s.Delay() <= c[1], "case %d delay out of bounds", ci)
		}
	}
}

// TestConvergence pins invariant 3: monotone raise/hold then fall/hold.
func TestConvergence(t *testing.T) {
	for _, c := range [][3]int64{{2, 10, 2}, {0, 9, 3}, {1, 7, 1}} {
		s, _ := api.New(c[0], c[1], c[2], 3, 1, 0)
		s.Feed(1000)
		low := func() { s.Feed(1); s.Feed(1); s.Feed(1) }
		up := func(b int64) { s.Feed(b); s.Feed(b + 1); s.Feed(b + 2) }
		rounds := int((c[1]-c[0])/c[2]) + 2
		prev := c[0]
		for k := 0; k < rounds; k++ {
			low() // all-late window
			chk(t, s.Delay() >= prev, "raise not monotone %d->%d", prev, s.Delay())
			prev = s.Delay()
		}
		chk(t, s.Delay() == c[1], "no up-convergence: %d", s.Delay())
		for k, b := 0, int64(10000); k < rounds; k, b = k+1, b+10 {
			up(b) // on-time window
			chk(t, s.Delay() <= prev, "fall not monotone %d->%d", prev, s.Delay())
			prev = s.Delay()
		}
		chk(t, s.Delay() == c[0], "no down-convergence: %d", s.Delay())
	}
}

// TestRejectionsStateUntouched pins invariant 4: five distinct sentinels, nil
// result, existing store untouched and still usable.
func TestRejectionsStateUntouched(t *testing.T) {
	args := [][6]int64{{9, 8, 1, 3, 2, 0}, {-1, 8, 1, 3, 2, 0},
		{2, 8, 0, 3, 2, 0}, {2, 8, 1, 0, 2, 0}, {2, 8, 1, 3, 2, 2}}
	wants := []error{adapt.ErrMinAboveMax, adapt.ErrNegativeMin,
		adapt.ErrNonPositiveStep, adapt.ErrNonPositiveWindow, adapt.ErrThresholdRange}
	var errs []error
	for i, a := range args {
		st, err := api.New(a[0], a[1], a[2], a[3], a[4], a[5])
		chk(t, st == nil && errors.Is(err, wants[i]), "rejection %d: %v", i, err)
		errs = append(errs, err)
	}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			chk(t, !errors.Is(errs[i], errs[j]), "errors %d,%d not distinct", i, j)
		}
	}
	good, _ := api.New(2, 10, 2, 3, 2, 0)
	good.Feed(42)
	wm, d := good.WM(), good.Delay()
	for _, a := range args {
		st, _ := api.New(a[0], a[1], a[2], a[3], a[4], a[5])
		chk(t, st == nil, "rejected call created state")
	}
	chk(t, good.WM() == wm && good.Delay() == d, "rejections mutated existing store")
	good.Feed(100)
	chk(t, good.WM() == 98, "store unusable after rejections: wm=%d", good.WM())
}

// TestConcurrentReaders: N readers synchronize on a barrier (no sleeps) and
// must observe identical wm/delay; clean under -race.
func TestConcurrentReaders(t *testing.T) {
	s, _ := api.New(2, 10, 2, 3, 2, 0)
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		s.Feed(r.Int63n(1000))
	}
	const N = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	got := make([][2]int64, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; got[i] = [2]int64{s.WM(), s.Delay()} }(g)
	}
	close(start)
	wg.Wait()
	for i, x := range got {
		chk(t, x == got[0], "reader %d saw %v want %v", i, x, got[0])
	}
}
