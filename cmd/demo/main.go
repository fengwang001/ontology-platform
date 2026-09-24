package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/stk"
	"ontology/twin"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func main() {
	s := stk.New()
	for _, v := range []float64{3, 1, 3, 2} {
		s.Push(v)
	}
	stkOK := s.Len() == 4
	for _, e := range s.Entries() {
		stkOK = stkOK && e.Agg == 3
	}
	q := twin.New()
	wantMax := []float64{3, 3, 3, 3, 3, 3, 2, 2, 2, 2, 2}
	var gotEv []float64
	stepOK := true
	for i, c := range "313210E2EEE" {
		if c == 'E' {
			v, _ := q.Evict()
			gotEv = append(gotEv, v)
		} else {
			q.Push(float64(c - '0'))
			if q.Len() > 4 {
				v, _ := q.Evict()
				gotEv = append(gotEv, v)
			}
		}
		mx, _ := q.Max()
		stepOK = stepOK && mx == wantMax[i]
	}
	for i, w := range []float64{3, 1, 3, 2, 1, 0} {
		stepOK = stepOK && gotEv[i] == w
	}
	check("stk aggregates; eleven-step Max; flips only at 5/11", stkOK && stepOK)
	w, _ := api.New(4)
	check("api.SelfCheck: four invariants", w.SelfCheck() == nil)
	t, _ := api.New(3)
	_ = t.PushAll([]float64{3, 1, 3})
	_ = t.Push(3)
	m, _ := t.Max()
	check("tied max evicted, Max stays 3", m == 3)
	r := rand.New(rand.NewSource(1))
	rw, _ := api.New(8)
	var ref []float64
	randOK := true
	for n := 0; n < 10000; n++ {
		if r.Intn(3) == 0 && len(ref) > 0 {
			gv, e := rw.Evict()
			randOK, ref = randOK && e == nil && gv == ref[0], ref[1:]
		} else {
			v := float64(r.Intn(5))
			randOK = randOK && rw.Push(v) == nil
			ref = append(ref, v)
			if len(ref) > 8 {
				ref = ref[1:]
			}
		}
		if vs, mx, e := rw.Snapshot(); len(vs) > 0 {
			randOK = randOK && e == nil && mx == slices.Max(vs)
		}
	}
	check("random ops match naive FIFO/Max at scale", randOK)
	pushed := []float64{5, 2, 5, 0, 1, 9, 2, 7}
	fw, _ := api.New(100)
	fifoOK := true
	for _, v := range pushed {
		_ = fw.Push(v)
	}
	for _, want := range pushed {
		gv, e := fw.Evict()
		fifoOK = fifoOK && e == nil && gv == want
	}
	check("FIFO eviction equals pushed prefix", fifoOK)
	z, _ := api.New(2)
	_, eEmpty := z.Evict()
	_, badW := api.New(0)
	g, _ := api.New(3)
	_ = g.PushAll([]float64{2, 2, 2})
	before, _, _ := g.Snapshot()
	eNaN := g.Push(math.NaN())
	eNaNAll := g.PushAll([]float64{1, math.NaN()})
	after, am, _ := g.Snapshot()
	three := errors.Is(eEmpty, api.ErrEmpty) && errors.Is(badW, api.ErrCapacity) &&
		errors.Is(eNaN, api.ErrNaN) && errors.Is(eNaNAll, api.ErrNaN)
	unchanged := three && len(after) == len(before) && am == 2
	check("three distinct decidable sentinel errors", three)
	check("rejected ops leave state untouched, still usable", unchanged && g.Push(7) == nil)
	big, _ := api.New(64)
	bigOK := true
	for i := 0; i < 10000; i++ {
		_ = big.Push(float64(i % 7))
	}
	for i := 0; i < 64; i++ {
		vs, mx, e := big.Snapshot()
		bigOK = bigOK && e == nil && mx == slices.Max(vs)
		_, _ = big.Evict()
	}
	check("large-m two-stack correctness", bigOK)
	cw, _ := api.New(16)
	var wg sync.WaitGroup
	var done, bad atomic.Bool
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !done.Load() {
				vs, mx, e := cw.Snapshot()
				if e == nil && (len(vs) > 16 || mx != slices.Max(vs)) {
					bad.Store(true)
				}
			}
		}()
	}
	for i := 0; i < 20000; i++ {
		_ = cw.Push(float64(r.Intn(6)))
		if i%3 == 0 {
			_, _ = cw.Evict()
		}
	}
	done.Store(true)
	wg.Wait()
	check("concurrent Snapshot self-consistent, len<=W", !bad.Load())
	if failed {
		os.Exit(1)
	}
}
