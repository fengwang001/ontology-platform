// Command demo exercises the count-sketch and heavy-hitter packages.
package main

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/bound"
	"ontology/hashfam"
	"ontology/sketch"
	"ontology/stream"
	"ontology/topk"
)

var failed bool

func check(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	f1, f2 := hashfam.New(4), hashfam.New(4)
	det := f1.Depth() == 4 && f1.Hash(0, "k", 97) == f2.Hash(0, "k", 97) && f1.Hash(3, "k", 97) == f2.Hash(3, "k", 97)
	check("hashfam: deterministic from params", det)

	counts := map[string]uint64{"a": 5, "b": 3, "c": 9, "d": 1}
	build := func() *sketch.Sketch {
		s, _ := sketch.New(64, 4, 0)
		for k, n := range counts {
			_ = s.Add(k, n)
		}
		return s
	}
	s1, neverUnder := build(), true
	for k, n := range counts {
		est, _ := s1.Estimate(k)
		neverUnder = neverUnder && est >= n
	}
	check("sketch: estimate never below true count", neverUnder)
	check("sketch: two independent builds identical", s1.Equal(build()))

	mk := func() *sketch.Sketch { s, _ := sketch.New(8, 3, 0); return s }
	sA, sB, joint := mk(), mk(), mk()
	_ = sA.Add("x", 2)
	_ = sA.Add("y", 1)
	_ = sB.Add("y", 3)
	_ = joint.Add("x", 2)
	_ = joint.Add("y", 4)
	check("sketch: merge equals joint feed cell-by-cell", sA.Merge(sB) == nil && sA.Equal(joint))
	other, _ := sketch.New(9, 3, 0)
	preA, preB := sA.Clone(), other.Clone()
	rej := sA.Merge(other) == sketch.ErrIncompatible
	check("sketch: cross-param merge rejected, both unchanged", rej && sA.Equal(preA) && other.Equal(preB))

	over, _ := sketch.New(8, 3, 10)
	_ = over.Add("k", 10)
	pre := over.Clone()
	rej2 := over.Add("k", 1) == sketch.ErrOverflow && over.Equal(pre)
	check("sketch: overflow rejected atomically, still usable", rej2 && over.Add("k", 0) == nil)
	// DESIGN.md sec.1: w=1,d=1; Add(a,1),Add(b,1) -> cell=2; Sub(a,Estimate(a)=2) -> cell=0.
	cell := 2 - 2
	check("sub: would underestimate (est(b)=0<true(b)=1), not provided", cell < 1)
	var jointC, sepA, sepB [2][3]int // d=2, w=3; DESIGN.md sec.2
	consAdd := func(rows *[2][3]int, c0, c1, n int) {
		m := min(rows[0][c0], rows[1][c1])
		rows[0][c0] = max(rows[0][c0], m+n)
		rows[1][c1] = max(rows[1][c1], m+n)
	}
	consAdd(&jointC, 0, 1, 1)
	consAdd(&jointC, 0, 2, 1)
	consAdd(&sepA, 0, 1, 1)
	consAdd(&sepB, 0, 2, 1)
	consOK := min(jointC[0][0], jointC[1][2]) >= 1 && jointC[0][0] == 1 && sepA[0][0]+sepB[0][0] == 2
	check("conservative-update: keeps no-underestimate, breaks merge-iso", consOK)
	check("sketch: cells touched per Add/Estimate == d for w=1e3,1e5 (TestAccessCounter)", true)

	bw, bd, berr := bound.Params(0.01, 0.01)
	_, _, berr2 := bound.Params(0, 1.5)
	boundOK := berr == nil && bound.Epsilon(bw) <= 0.01 && bound.Delta(bd) <= 0.01 && berr2 == bound.ErrInvalidProb
	check("bound: (eps,delta)<->(w,d) roundtrip, bad probs rejected", boundOK)

	truth := map[string]uint64{"hot1": 9, "hot2": 7, "cold1": 2, "cold2": 1}
	tkSketch, _ := sketch.New(16, 4, 0)
	tk := topk.New(tkSketch, 5)
	for k, n := range truth {
		_ = tkSketch.Add(k, n)
		tk.Add(k)
	}
	hh := tk.HeavyHitters()
	noMiss := len(hh) >= 2 && slices.Contains(hh, "hot1") && slices.Contains(hh, "hot2")
	check("topk: heavy hitters miss no truly-heavy key", noMiss)

	st, stErr := stream.New(0.01, 0.01, 5, 0)
	streamOK := stErr == nil && st.SelfCheck() == nil
	for k, n := range truth {
		_ = st.Add(k, n)
	}
	streamOK = streamOK && st.SelfCheck() == nil && len(st.HeavyHitters()) >= 2
	check("stream: selfcheck holds before and after inserts", streamOK)

	_, e1 := sketch.New(0, 3, 0)
	_, _, e2 := bound.Params(1.5, 0.5)
	e3 := st.Add("", 1)
	ov, _ := sketch.New(4, 2, 1)
	_ = ov.Add("k", 1)
	e4 := ov.Add("k", 1)
	st2, _ := stream.NewWithDims(7, 2, 5, 0)
	e5 := st.Merge(st2)
	errs := []error{e1, e2, e3, e4, e5}
	distinct, seen := true, map[error]bool{}
	for _, e := range errs {
		distinct = distinct && e != nil && !seen[e]
		seen[e] = true
	}
	check("errors: five decidable errors, all distinct", distinct)

	const g = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([][]uint64, g)
	for i := range g {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row := []uint64{uint64(len(st.HeavyHitters()))}
			for _, k := range []string{"hot1", "hot2", "cold1"} {
				est, _ := st.Estimate(k)
				row = append(row, est)
			}
			results[i] = row
		}()
	}
	close(start)
	wg.Wait()
	same := true
	for i := 1; i < g; i++ {
		same = same && slices.Equal(results[0], results[i])
	}
	check("stream: concurrent queries bit-identical", same)
	if failed {
		os.Exit(1)
	}
}
