package sampler

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// four-exit identity: decided spans + buffered + late kept/dropped == accepted.
func TestNaiveEquivalenceRandom(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for seed := int64(0); seed < 200; seed++ {
		r.Seed(seed)
		c := Config{W: r.Int63n(6), Sc: 1 + r.Int63n(4), Nmax: 1 + r.Int63n(4),
			Td: r.Int63n(9), Cmax: r.Int63n(5), L: r.Int63n(21),
			P: r.Int63n(10001), Wb: 1 + r.Int63n(8), Q: r.Int63n(4), H: hfnv}
		s, nm := mustNew(c), newNaive(c)
		var got, want []Decision
		var accepted int64
		now := int64(0)
		for op := 0; op < 120; op++ {
			if r.Intn(5) == 0 {
				now += r.Int63n(4)
				g, _ := s.Tick(now)
				got, want = append(got, g...), append(want, nm.tick(now)...)
				continue
			}
			tid, sid := fmt.Sprintf("t%d", r.Intn(5)), fmt.Sprintf("s%d", r.Intn(6))
			dur, isErr := r.Int63n(25), r.Intn(6) == 0
			g, e := s.Ingest(now, tid, sid, dur, isErr)
			w := nm.ingest(now, tid, sid, dur, isErr)
			if e == nil {
				accepted++
			}
			got, want = append(got, g...), append(want, w...)
			if !eqDec(got, want) {
				t.Fatalf("seed=%d op=%d cfg=%+v\n got=%+v\nwant=%+v", seed, op, c, g, w)
			}
		}
		st := s.Stats()
		if st.LateKept != nm.lateK || st.LateDropped != nm.lateD || st.Decisions != nm.decN {
			t.Fatalf("seed=%d %+v vs naive dec=%d k=%d d=%d", seed, st, nm.decN, nm.lateK, nm.lateD)
		}
		var inD, buffered int64
		for _, d := range got {
			inD += d.Spans
		}
		for _, tr := range nm.buf {
			buffered += tr.cnt
		}
		if sum := inD + buffered + st.LateKept + st.LateDropped; sum != accepted {
			t.Fatalf("seed=%d four-exit sum=%d accepted=%d", seed, sum, accepted)
		}
	}
}

// TestTickInspectionBound proves Tick inspects at most decided+1 heap tops,
// at the required 100 and 10000 trace sizes.
func TestTickInspectionBound(t *testing.T) {
	for _, nmax := range []int64{100, 10000} {
		s := mustNew(Config{W: 10, Sc: 10000, Nmax: nmax, L: 1e9, Wb: 1e9, H: hfnv})
		for i := int64(0); i < nmax; i++ {
			if _, e := s.Ingest(0, fmt.Sprintf("t%05d", i), "s", 1, false); e != nil {
				t.Fatal(e)
			}
		}
		if ds, _ := s.Tick(9); len(ds) != 0 || s.Stats().TickInspected != 1 {
			t.Fatalf("non-silent tick inspected=%d decided=%d", s.Stats().TickInspected, len(ds))
		}
		ds, _ := s.Tick(10)
		st := s.Stats()
		if int64(len(ds)) != nmax || st.TickInspected != nmax || st.TickInspected > st.TickDecided+1 {
			t.Fatalf("nmax=%d decided=%d inspected=%d", nmax, len(ds), st.TickInspected)
		}
	}
}

// TestConcurrent exercises interleaved calls under -race; the mutex makes the
// resulting history equivalent to some serial interleaving.
func TestConcurrent(t *testing.T) {
	s := mustNew(Config{W: 3, Sc: 2, Nmax: 4, Td: 10, Cmax: 8, L: 10, P: 4000, Wb: 5, Q: 1, H: hfnv})
	var clk atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 30; k++ {
				t1, t2 := clk.Add(1), clk.Add(1)
				_, _ = s.Ingest(t1, fmt.Sprintf("t%d", (g+k)%7), fmt.Sprintf("s%d_%d", g, k), int64(k%20), k%9 == 0)
				_, _ = s.Tick(t2)
			}
		}(g)
	}
	wg.Wait()
}
