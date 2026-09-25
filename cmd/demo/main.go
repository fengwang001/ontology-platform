package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/merge"
)

func main() {
	fail := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK:", name)
		} else {
			fmt.Println("FAIL:", name)
			fail = 1
		}
	}

	// 1) merge package: tombstone erases a present value; later write restores it.
	d := merge.Apply(merge.Result{Val: "1", OK: true}, merge.Entry{Key: "c", Del: true})
	rw := merge.Apply(d, merge.Entry{Key: "c", Val: "7"})
	check("merge: delete then write", !d.OK && rw.OK && rw.Val == "7")

	// 2) Seven-step table (T=4): per-step delta lengths; step-4 compaction; base.
	s, _ := api.New(4)
	wantLen := []int{1, 2, 3, 0, 1, 2, 3}
	ops := []func(){
		func() { _ = s.Set("a", "1") }, func() { _ = s.Set("b", "2") },
		func() { _ = s.Set("c", "3") }, func() { _ = s.Del("c") },
		func() { _ = s.Set("b", "9") }, func() { _ = s.Set("c", "7") },
		func() { _ = s.Set("c", "8") },
	}
	stepOK := true
	for i, op := range ops {
		op()
		if s.DeltaLen() != wantLen[i] {
			stepOK = false
		}
	}
	check("seven-step delta/base + step4 compact", stepOK && s.DeltaLen() == 3 &&
		reflect.DeepEqual(s.BaseKeys(), []string{"a", "b"}))

	// 3) Three Read results and merge cost (= delta length scanned) = 3.
	av, aok := s.Read("a")
	bv, bok := s.Read("b")
	cv, cok := s.Read("c")
	check("reads a=1,b=9,c=8; merge cost=3", av == "1" && aok && bv == "9" && bok &&
		cv == "8" && cok && s.DeltaLen() == 3)

	// 4) Tombstone semantics, and latest value after delete-then-rewrite.
	_ = s.Set("z", "5")
	_ = s.Del("z")
	_, zok1 := s.Read("z")
	_ = s.Set("z", "6")
	zv, zok2 := s.Read("z")
	check("tombstone absent; rewrite latest", !zok1 && zok2 && zv == "6")

	// 5) Auto-compaction keeps delta bounded; cost does not grow with m.
	bounded := true
	for _, m := range []int{100, 1000, 10000} {
		b, _ := api.New(4)
		for i := 0; i < m; i++ {
			_ = b.Set(fmt.Sprintf("k%d", i), "x")
		}
		if b.DeltaLen() > 3 { // merge cost == delta length scanned
			bounded = false
		}
	}
	check("delta/cost bounded by T-1 for m in 100..10000", bounded)

	// 6) Two distinct decidable sentinel errors; rejected ops leave no trace.
	bk, dl := s.BaseKeys(), s.DeltaLen()
	errSet := s.Set("", "x")
	errDel := s.Del("")
	_, errNew := api.New(0)
	check("sentinel errors distinct; rejection leaves no trace",
		errors.Is(errSet, api.ErrEmptyKey) && errors.Is(errDel, api.ErrEmptyKey) &&
			errors.Is(errNew, api.ErrInvalidThreshold) &&
			!errors.Is(errSet, api.ErrInvalidThreshold) &&
			reflect.DeepEqual(s.BaseKeys(), bk) && s.DeltaLen() == dl)

	// 7) Concurrency: N identical readers; N distinct-key writers == serial.
	filled, _ := api.New(8)
	for i := 0; i < 20; i++ {
		_ = filled.Set(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	var wg sync.WaitGroup
	rdrs := make([][]string, 8)
	for g := range rdrs {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				v, ok := filled.Read(fmt.Sprintf("k%d", i))
				if !ok {
					v = ""
				}
				rdrs[g] = append(rdrs[g], v)
			}
		}(g)
	}
	wg.Wait()
	concOK := true
	for g := 1; g < len(rdrs); g++ {
		if !reflect.DeepEqual(rdrs[g], rdrs[0]) {
			concOK = false
		}
	}
	wc, _ := api.New(8)
	const N = 16
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = wc.Set(fmt.Sprintf("g%d-k%d", g, j), fmt.Sprintf("%d-%d", g, j))
			}
		}(g)
	}
	wg.Wait()
	for g := 0; g < N && concOK; g++ {
		for j := 0; j < 10; j++ {
			if v, ok := wc.Read(fmt.Sprintf("g%d-k%d", g, j)); !ok || v != fmt.Sprintf("%d-%d", g, j) {
				concOK = false
			}
		}
	}
	check("concurrent readers identical; distinct-key writers == serial", concOK)

	// 8) Built-in self-check over the four invariants.
	sc, _ := api.New(4)
	check("SelfCheck passes", sc.SelfCheck() == nil)

	os.Exit(fail)
}
