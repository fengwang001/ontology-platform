package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/api"
)

// batch recomputes (mean, M2) from the full multiset for cross-checking.
func batch(xs []float64) (float64, float64) {
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var m2 float64
	for _, x := range xs {
		m2 += (x - mean) * (x - mean)
	}
	return mean, m2
}

func closef(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func mark(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

func main() {
	a := api.New()
	a.Apply(api.Op{Kind: api.OpAdd, Key: "G2", Value: 10})
	a.Apply(api.Op{Kind: api.OpAdd, Key: "G2", Value: 20})

	steps := []struct {
		op   api.Op
		vals []float64 // multiset in G after this step
	}{
		{api.Op{Kind: api.OpAdd, Key: "G", Value: 1}, []float64{1}},
		{api.Op{Kind: api.OpAdd, Key: "G", Value: 2}, []float64{1, 2}},
		{api.Op{Kind: api.OpAdd, Key: "G", Value: 3}, []float64{1, 2, 3}},
		{api.Op{Kind: api.OpAdd, Key: "G", Value: 5}, []float64{1, 2, 3, 5}},
		{api.Op{Kind: api.OpRemove, Key: "G", Value: 1}, []float64{2, 3, 5}},
		{api.Op{Kind: api.OpAdd, Key: "G", Value: 4}, []float64{2, 3, 4, 5}},
		{api.Op{Kind: api.OpRemove, Key: "G", Value: 3}, []float64{2, 4, 5}},
		{api.Op{Kind: api.OpMerge, Key: "G", Other: "G2"}, []float64{2, 4, 5, 10, 20}},
	}
	for i, st := range steps {
		a.Apply(st.op)
		v := a.View("G")
		bm, bm2 := batch(st.vals)
		ok := int64(len(st.vals)) == v.N && closef(v.Mean, bm) && closef(v.M2, bm2) && closef(v.Variance, bm2/float64(len(st.vals)))
		fmt.Printf("step%d: n=%d mean=%.4g M2=%.4g var=%.4g batch=%s\n", i+1, v.N, v.Mean, v.M2, v.Variance, mark(ok))
	}

	// Three distinct decidable errors; each rejected op leaves no trace.
	before := a.View("G")
	bad := []api.Op{{Kind: api.OpAdd, Key: ""}, {Kind: api.OpRemove, Key: "G", Value: 999}, {Kind: api.OpMerge, Key: "G", Other: "VOID"}}
	want := []error{api.ErrEmptyKey, api.ErrValueAbsent, api.ErrMergeEmpty}
	guards := true
	for i, op := range bad {
		err := a.Apply(op)
		guards = guards && errors.Is(err, want[i])
	}
	guards = guards && a.View("G") == before && a.SelfCheck() == nil // SelfCheck also pins O(1) lookups
	fmt.Printf("errors/no-trace/O(1)-lookup: %s\n", mark(guards))

	// Concurrent readers of the already-fed group must agree field by field.
	ref := a.View("G")
	var wg sync.WaitGroup
	diverged := false
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if a.View("G") != ref {
					mu.Lock()
					diverged = true
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	fmt.Printf("concurrent reads identical: %s\n", mark(!diverged))

	if !guards || diverged {
		panic("demo checks failed")
	}
}
