// Command demo prints OK/FAIL lines for every deliverable check of the
// Greenwald-Khanna quantile summary. Exit code 0 iff every line is OK.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/gk"
	"ontology/quant"
)

var failed bool

func report(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}
func trace() { // the six steps of NOTES.md section 3, eps=0.25
	s := gk.New(0.25)
	for _, v := range []int64{10, 20, 30, 40} {
		s.Insert(v)
	}
	report("trace s1-4", s.String() == "[(10,1,0),(20,1,0),(30,1,0),(40,1,0)]", s.String())
	s.Compress()
	s5 := s.String()
	s.Insert(25)
	ok := s5 == "[(20,2,0),(40,2,0)]" && s.String() == "[(20,2,0),(25,1,1),(40,2,0)]"
	report("trace s5-6", ok, s5+" -> "+s.String())
	q := &quant.Engine{}
	q5, q7 := q.Query(s, 0.5), q.Query(s, 0.7)
	report("QUERY(0.5)/QUERY(0.7)", q5 == 25 && q7 == 25, fmt.Sprintf("= %d / %d", q5, q7))
}
func band() { // band invariant after every trace step
	s := gk.New(0.25)
	ok := true
	for _, v := range []int64{10, 20, 30, 40} {
		s.Insert(v)
		ok = ok && s.BandOK()
	}
	s.Compress()
	ok = ok && s.BandOK()
	s.Insert(25)
	report("band g+Δ<=2εn", ok && s.BandOK(), "on all 6 trace states")
}
func naive() { // Query vs exact sorted reference within ε·n
	const n, eps = 2000, 0.1
	a, _ := api.New(eps)
	srt := make([]int64, n)
	for i := range srt {
		srt[i] = int64(i) * 997 % n // gcd(997,2000)=1: distinct
		_ = a.Insert(srt[i])
	}
	slices.Sort(srt)
	ok := true
	for k := 1; k < 100; k++ {
		got, _ := a.Query(float64(k) / 100)
		r, _ := slices.BinarySearch(srt, got)
		d := float64(r+1) - float64(k)/100*n
		ok = ok && d <= eps*n && d >= -eps*n
	}
	report("naive |rank-φn|<=εn", ok, "eps=0.1 n=2000")
}
func rejects() { // four distinguishable failures, no trace left
	a, _ := api.New(0.1)
	for _, v := range []int64{3, 1, 4} {
		_ = a.Insert(v)
	}
	n0 := a.Size()
	q0, _ := a.Query(0.5)
	_, e1 := api.New(0)
	e2 := a.Insert(3)
	_, e3 := a.Query(1.5)
	b, _ := api.New(0.1)
	_, e4 := b.Query(0.5)
	ok := errors.Is(e1, api.ErrEpsilon) && errors.Is(e2, api.ErrDuplicate) &&
		errors.Is(e3, api.ErrPhi) && errors.Is(e4, api.ErrEmpty) &&
		!errors.Is(e1, e2) && !errors.Is(e1, e3) && !errors.Is(e1, e4) &&
		!errors.Is(e2, e3) && !errors.Is(e2, e4) && !errors.Is(e3, e4)
	report("4 distinct sentinel errors", ok, "epsilon/duplicate/phi/empty")
	q1, _ := a.Query(0.5)
	ok = a.Size() == n0 && q0 == q1 && a.Insert(9) == nil
	report("rejected ops leave no trace", ok, fmt.Sprintf("n=%d still usable", n0))
}
func sizes() { // summary size (≥ tuples a query scans) stays small
	size := func(m int) int {
		s := gk.New(0.05) // mirrors api eps=0.1 (internal eps/2)
		for i := 0; i < m; i++ {
			s.Insert(int64(i))
			if s.N()%10 == 0 {
				s.Compress()
			}
		}
		return len(s.Tuples())
	}
	c1, c2 := size(100), size(10000)
	report("summary size independent of m", c1 <= 20 && c2 <= 20, fmt.Sprintf("m=100:%d m=10000:%d", c1, c2))
}
func concurrent() { // N goroutines over the same phis must agree
	a, _ := api.New(0.1)
	for i := 0; i < 1000; i++ {
		_ = a.Insert(int64(i))
	}
	phis := []float64{0.1, 0.25, 0.5, 0.75, 0.9}
	want := make([]int64, len(phis))
	for i, p := range phis {
		want[i], _ = a.Query(p)
	}
	start := make(chan struct{})
	var bad atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i, p := range phis {
				if v, _ := a.Query(p); v != want[i] {
					bad.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	report("concurrent queries consistent", bad.Load() == 0, "16 goroutines x 5 phis")
}
func main() {
	trace()
	band()
	naive()
	rejects()
	a, _ := api.New(0.1)
	report("SelfCheck", a.SelfCheck() == nil, "4 invariants on built-in sequences")
	sizes()
	concurrent()
	if failed {
		os.Exit(1)
	}
}
