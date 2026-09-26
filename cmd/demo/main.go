// Command demo prints OK/FAIL lines for the single-decree Paxos acceptor FSM.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/quorum"
)

func ok(label string, good bool) {
	if good {
		fmt.Println("OK   " + label)
	} else {
		fmt.Println("FAIL " + label)
	}
}

func main() {
	// Section three: the ten steps, recording Chosen() after each step.
	p := api.New(3)
	ops := [10][3]int{{0, 2, 0}, {1, 2, 0}, {2, 2, 0}, {0, 2, 10}, {1, 2, 10},
		{0, 3, 0}, {1, 3, 0}, {2, 3, 0}, {0, 3, 10}, {1, 3, 10}}
	got := make([]int, 10)
	have := make([]bool, 10)
	for i, o := range ops { // bits 0,1,2,5,6,7 (mask 231) are Prepare steps
		if (231>>uint(i))&1 == 1 {
			_, _, _, _ = p.Prepare(o[0], o[1])
		} else {
			_, _ = p.Accept(o[0], o[1], o[2])
		}
		got[i], have[i] = p.Chosen()
	}
	wantV := []int{0, 0, 0, 0, 10, 10, 10, 10, 10, 10}
	wantH := []bool{false, false, false, false, true, true, true, true, true, true}
	tenOK := true
	for i := range wantV {
		if got[i] != wantV[i] || have[i] != wantH[i] {
			tenOK = false
		}
	}
	ok("ten steps Chosen = 无无无无 10x6", tenOK)

	// Three distinguishable sentinel errors, plus the normal stale-accept
	// refusal which is (false, nil), not an error.
	e := api.New(3)
	_, _, _, errIdx := e.Prepare(-1, 1)
	_, _, _, errNum := e.Prepare(0, 0)
	_, _, _, _ = e.Prepare(0, 2)
	_, _, _, errStale := e.Prepare(0, 2)
	_, _ = e.Accept(0, 2, 10)
	stale, errRefuse := e.Accept(0, 1, 99)
	distinct := errors.Is(errIdx, api.ErrAcceptorIndex) && errors.Is(errNum, api.ErrProposalNumber) &&
		errors.Is(errStale, api.ErrStalePrepare) && errIdx != errNum && errNum != errStale &&
		!stale && errRefuse == nil
	ok("three distinct sentinel errors; stale accept is (false,nil)", distinct)

	// Rejections leave no trace: acc0 still reports (promised 2, accepted 2,
	// value 10) to a fresh prepare after every illegal/stale call.
	_, an0, av0, _ := e.Prepare(0, 3)
	_, _, _, _ = e.Prepare(3, 5)
	_, _ = e.Accept(0, 0, 1)
	_, _ = e.Accept(0, 1, 99)
	_, an1, av1, _ := e.Prepare(0, 4)
	ok("rejected ops leave state untouched", an0 == 2 && av0 == 10 && an1 == 2 && av1 == 10)

	// Large m: Chosen reads stay bounded by an m-independent constant. The
	// counter value itself is never read; only the bounded verdict is used.
	bigOK := true
	for _, m := range []int{101, 1001, 10001} {
		t := quorum.New(m)
		for i := 0; i < m/2+1; i++ {
			t.Move(0, 7)
		}
		v, chosen := t.Chosen()
		bigOK = bigOK && chosen && v == 7 && t.ReadsBounded()
	}
	ok("large-m Chosen reads do not grow with m", bigOK)

	// Concurrent read-only goroutines must see field-identical results.
	ready := api.New(5)
	for _, a := range []int{0, 2, 4} {
		_, _, _, _ = ready.Prepare(a, 1)
		_, _ = ready.Accept(a, 1, 42)
	}
	const n = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	rv := make([]int, n)
	rh := make([]bool, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for k := 0; k < 1000; k++ {
				rv[g], rh[g] = ready.Chosen()
			}
		}(g)
	}
	close(start)
	wg.Wait()
	concOK := true
	for g := 0; g < n; g++ {
		if rv[g] != rv[0] || rh[g] != rh[0] {
			concOK = false
		}
	}
	ok("concurrent readers see field-identical Chosen", concOK && rh[0] && rv[0] == 42)

	ok("SelfCheck passes all four invariants", api.New(1).SelfCheck())
}
