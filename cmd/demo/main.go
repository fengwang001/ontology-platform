// Command demo exercises the FKS structure and prints one OK/FAIL per check.
// It takes no arguments and performs no network access. Exit code is 0 only
// when every check passes. Output is at most 10 lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/first"
	"ontology/second"
)

var failed bool

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func nextPrime(after int) int {
	p := after + 1
	for {
		prime := p >= 2
		for d := 2; d*d <= p; d++ {
			if p%d == 0 {
				prime = false
				break
			}
		}
		if prime {
			return p
		}
		p++
	}
}

func main() {
	keys := []int{5, 11, 13, 17, 19, 24}
	tab := second.Build(first.Partition(keys, 6), 29)
	bs := tab.Buckets()
	line1 := bs[0].Direct && bs[0].Single == 24 &&
		!bs[1].Direct && bs[1].A == 1 && bs[1].B == 0 && bs[1].Slots[1] == 13 && bs[1].Slots[3] == 19 &&
		!bs[5].Direct && bs[5].A == 1 && bs[5].B == 0 && bs[5].Slots[5] == 5 && bs[5].Slots[2] == 11 && bs[5].Slots[8] == 17
	check("six-key buckets/slots:", line1, "b0=direct:24; b1(1,0):13->1,19->3; b5(1,0):5->5,11->2,17->8")

	s := api.New()
	buildErr := s.Build(keys, 6, 29)
	allFound := buildErr == nil
	for _, k := range keys {
		ok, _ := s.Lookup(k)
		allFound = allFound && ok
	}
	sevenOK, sevenErr := s.Lookup(7)
	check("collision-free lookup:", allFound && !sevenOK && errors.Is(sevenErr, api.ErrNotFound), "all six hit; Lookup(7)=ErrNotFound")

	ref := map[int]bool{}
	for _, k := range keys {
		ref[k] = true
	}
	matchRef := true
	for x := -5; x <= 35; x++ {
		ok, _ := s.Lookup(x)
		if ok != ref[x] {
			matchRef = false
		}
	}
	check("matches naive reference:", matchRef, "Lookup over -5..35 equals map[int]bool")

	check("space sum Σn_j²:", tab.SecondSize() == 13, fmt.Sprintf("got %d, want 4+9=13", tab.SecondSize()))

	errs := []error{
		s.Build([]int{1, 2, 2}, 4, 5),
		s.Build(nil, 4, 5),
		s.Build([]int{1, 2}, 0, 5),
	}
	_, notFound := s.Lookup(100000)
	fourKinds := errors.Is(errs[0], api.ErrDuplicateKey) && errors.Is(errs[1], api.ErrEmptyKeys) &&
		errors.Is(errs[2], api.ErrInvalidParam) && errors.Is(notFound, api.ErrNotFound)
	check("four decidable errors:", fourKinds, "duplicate / empty / invalid param / not found, all distinct")

	stateKept := s.Size() == 6
	for _, k := range keys {
		ok, _ := s.Lookup(k)
		stateKept = stateKept && ok
	}
	check("state intact after reject:", stateKept, "Size=6 and all keys still found after rejected Builds")

	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		half := m / 2
		ks := make([]int, 0, 2*half)
		for i := 0; i < half; i++ {
			ks = append(ks, i, m+i)
		}
		big := second.Build(first.Partition(ks, m), nextPrime(2*m))
		for _, k := range ks {
			if _, ok := big.Lookup(k); !ok {
				bigOK = false
			}
		}
	}
	check("large-m O(1) addressing:", bigOK, "m up to 10000; <=2-probe bound pinned by second.TestProbeCountConstant")

	const N, rounds = 32, 200
	var wg sync.WaitGroup
	var mu sync.Mutex
	concurrentOK := true
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				x := (seed*7 + i*13) % 40
				ok, _ := s.Lookup(x)
				if ok != ref[x] {
					mu.Lock()
					concurrentOK = false
					mu.Unlock()
				}
			}
		}(g + 1)
	}
	wg.Wait()
	check("concurrent reads agree:", concurrentOK, fmt.Sprintf("%d goroutines x %d read-only Lookups match reference", N, rounds))

	if failed {
		fmt.Println("FAIL demo")
		os.Exit(1)
	}
}
