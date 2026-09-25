package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/csync"
	"ontology/marz"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// Section 3 trace: A[8,12] B[10,12] C[19,21], f=1, need=2.
	clocks := [][2]int64{{10, 2}, {11, 1}, {20, 1}}
	eps := []marz.Endpoint{}
	for _, c := range clocks {
		l, r := marz.Endpoints(c[0], c[1])
		eps = append(eps, l, r)
	}
	marz.SortEndpoints(eps)
	count, open := 0, false
	trace := []int{}
	var enter, leave int64
	for _, e := range eps {
		count += e.Delta
		trace = append(trace, count)
		if count >= 2 && !open {
			open, enter = true, e.Pos
		}
		if count < 2 && open {
			open, leave = false, e.Pos
		}
	}
	check("sweep trace 1,2,1,0,1,0 candidate[10,12]",
		fmt.Sprint(trace) == "[1 2 1 0 1 0]" && enter == 10 && leave == 12)

	// Consensus through marz and through the public api.
	lo, hi, ok := marz.Sweep(eps, 2)
	sys, _ := api.New(1)
	sys.Add("A", 10, 2)
	sys.Add("B", 11, 1)
	sys.Add("C", 20, 1)
	alo, ahi, aerr := sys.Consensus()
	check("consensus [10,12]", ok && lo == 10 && hi == 12 && aerr == nil && alo == 10 && ahi == 12)

	// CountAt closed-interval semantics.
	check("CountAt(11)=2 CountAt(9)=1 CountAt(12)=2",
		sys.CountAt(11) == 2 && sys.CountAt(9) == 1 && sys.CountAt(12) == 2)

	// Invariant 1: api result equals a naive from-scratch sweep.
	nlo, nhi, nok := marz.Sweep(eps, 2)
	check("api == naive recompute", nok && alo == nlo && ahi == nhi)

	// Invariant 2: both ends are clock boundaries covered by >= K-f clocks.
	boundary := func(p int64) bool {
		for _, c := range clocks {
			if p == c[0]-c[1] || p == c[0]+c[1] {
				return true
			}
		}
		return false
	}
	check("boundary closure", boundary(alo) && boundary(ahi) && sys.CountAt(alo) >= 2 && sys.CountAt(ahi) >= 2)

	// Four distinct decidable errors.
	_, eF := api.New(-1)
	eNeg := sys.Add("NEG", 0, -1)
	eDup := sys.Add("A", 0, 0)
	empty, _ := api.New(0)
	_, _, eFew := empty.Consensus()
	distinct := !errors.Is(eF, eNeg) && !errors.Is(eF, eDup) && !errors.Is(eF, eFew) &&
		!errors.Is(eNeg, eDup) && !errors.Is(eNeg, eFew) && !errors.Is(eDup, eFew)
	check("four distinct decidable errors",
		errors.Is(eF, api.ErrInvalidF) && errors.Is(eNeg, csync.ErrNegativeError) &&
			errors.Is(eDup, csync.ErrDuplicateID) && errors.Is(eFew, api.ErrTooFewClocks) && distinct)

	// Rejected operations leave state unchanged.
	lo2, hi2, err2 := sys.Consensus()
	check("state unchanged after rejection",
		err2 == nil && lo2 == 10 && hi2 == 12 && sys.CountAt(11) == 2 && sys.CountAt(0) == 0)

	// CountAt probes stay O(log m) as m grows (unexported counter read via
	// reflection — it is not part of any exported API).
	probes := func(m int) int64 {
		s := csync.NewSet()
		for i := 0; i < m; i++ {
			s.Add(fmt.Sprint(i), int64(i*4), 1)
		}
		s.CountAt(int64(m * 2))
		return reflect.ValueOf(s).Elem().FieldByName("checked").Int()
	}
	p100, p10000 := probes(100), probes(10000)
	check("CountAt probes O(log m)", p100 > 0 && p100 <= 20 && p10000 <= 40 && p10000 < p100*10)

	// Concurrent read-only calls agree.
	var wg sync.WaitGroup
	var bad atomic.Int64
	start := make(chan struct{})
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				l, h, err := sys.Consensus()
				if err != nil || l != 10 || h != 12 || sys.CountAt(11) != 2 {
					bad.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent reads consistent", bad.Load() == 0)

	// Self-check of the four invariants on built-in sequences.
	check("SelfCheck", sys.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
