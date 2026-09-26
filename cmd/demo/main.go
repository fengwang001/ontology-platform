package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"slices"
	"strings"
	"sync"

	"ontology/api"
	"ontology/gk"
	"ontology/quant"
)

var failed bool

func ok(line string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, line)
}

func dump(ts []gk.Tuple) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "(%d,%d,%d)", t.V, t.G, t.D)
	}
	return b.String()
}

// naiveRef inserts m shuffled values into an api summary and checks every
// query answer's true rank against the sorted reference within εn.
func naiveRef() bool {
	const eps, m = 0.05, 1000
	a, err := api.New(eps)
	if err != nil {
		return false
	}
	vals := make([]int64, m)
	for i := range vals {
		vals[i] = int64(i) + 1
	}
	rand.New(rand.NewSource(7)).Shuffle(m, func(i, j int) { vals[i], vals[j] = vals[j], vals[i] })
	for _, v := range vals {
		if a.Insert(v) != nil {
			return false
		}
	}
	slices.Sort(vals)
	for _, phi := range []float64{0.1, 0.3, 0.5, 0.7, 0.9} {
		got, err := a.Query(phi)
		if err != nil {
			return false
		}
		rank, _ := slices.BinarySearch(vals, got)
		if math.Abs(float64(rank+1)-phi*m) > eps*m {
			return false
		}
	}
	return true
}

// rejects checks the four distinct sentinel errors and that rejected
// operations leave the summary untouched and still usable.
func rejects() bool {
	if _, err := api.New(0); !errors.Is(err, api.ErrEpsilon) {
		return false
	}
	if _, err := api.New(1.5); !errors.Is(err, api.ErrEpsilon) {
		return false
	}
	a, _ := api.New(0.25)
	for _, v := range []int64{5, 1, 9} {
		a.Insert(v)
	}
	before, _ := a.Query(0.5)
	dup := a.Insert(5)
	_, badPhi := a.Query(1.5)
	empty, _ := api.New(0.5)
	_, emptyQ := empty.Query(0.5)
	errs := []error{api.ErrEpsilon, gk.ErrDuplicate, quant.ErrBadPhi, quant.ErrEmpty}
	for i := range errs {
		if errs[i] == nil {
			return false
		}
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				return false
			}
		}
	}
	after, _ := a.Query(0.5)
	stateOK := errors.Is(dup, gk.ErrDuplicate) && errors.Is(badPhi, quant.ErrBadPhi) &&
		errors.Is(emptyQ, quant.ErrEmpty) && a.Size() == 3 && after == before
	return stateOK && a.Insert(7) == nil
}

// scaling inserts m ascending values and checks the summary stays small.
func scaling() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := gk.New(0.5)
		for v := 1; v <= m; v++ {
			s.Insert(int64(v))
			s.Compress()
		}
		if s.Len() > 20 {
			return false
		}
	}
	return true
}

// concurrent runs N goroutines querying the same φs; all must agree.
func concurrent() bool {
	a, _ := api.New(0.1)
	for v := 1; v <= 1000; v++ {
		a.Insert(int64(v))
	}
	phis := []float64{0.1, 0.25, 0.5, 0.75, 0.9}
	want := make([]int64, len(phis))
	for i, p := range phis {
		want[i], _ = a.Query(p)
	}
	var wg sync.WaitGroup
	bad := make(chan struct{}, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, p := range phis {
				if got, err := a.Query(p); err != nil || got != want[i] {
					bad <- struct{}{}
				}
			}
			_ = a.Size()
			_ = a.SelfCheck()
		}()
	}
	wg.Wait()
	close(bad)
	return len(bad) == 0
}

func main() {
	// The six-step derivation from NOTES.md, replayed on gk directly.
	s := gk.New(0.25)
	steps := []struct {
		name string
		op   func()
		want string
	}{
		{"INSERT(10)", func() { s.Insert(10) }, "(10,1,0)"},
		{"INSERT(20)", func() { s.Insert(20) }, "(10,1,0) (20,1,0)"},
		{"INSERT(30)", func() { s.Insert(30) }, "(10,1,0) (20,1,0) (30,1,0)"},
		{"INSERT(40)", func() { s.Insert(40) }, "(10,1,0) (20,1,0) (30,1,0) (40,1,0)"},
		{"COMPRESS()", s.Compress, "(10,2,0) (30,2,0)"},
		{"INSERT(25)", func() { s.Insert(25) }, "(10,2,0) (25,1,1) (30,2,0)"},
	}
	for i, st := range steps {
		st.op()
		got := dump(s.Tuples())
		ok(fmt.Sprintf("step%d %-10s %s", i+1, st.name, got), got == st.want)
	}
	q := &quant.Engine{}
	q5, e5 := q.Query(s, 0.5)
	q7, e7 := q.Query(s, 0.7)
	ok(fmt.Sprintf("QUERY(0.5)=%d QUERY(0.7)=%d", q5, q7),
		e5 == nil && e7 == nil && q5 == 25 && q7 == 25)
	ok("band invariant holds; naive-reference rank diff <= eps*n", s.BandOK() && naiveRef())
	ok("4 distinct rejectable errors; state intact after rejects", rejects())
	ok("query cost independent of m; concurrent queries agree", scaling() && concurrent())
	if failed {
		os.Exit(1)
	}
}
