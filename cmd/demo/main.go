// Command demo runs sanity checks for the interval merger.
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"

	"ontology/check"
	"ontology/iv"
	"ontology/merge"
)

var failed bool

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		failed = true
		fmt.Println("FAIL", name)
	}
}

func main() {
	_, err := iv.New(2, 2)
	report("iv: empty interval rejected", errors.Is(err, iv.ErrEmptyInterval))

	var m merge.Merger
	_ = m.AddAll([]iv.Interval{iv.Must(1, 2), iv.Must(2, 3)})
	r := m.Ranges()
	report("merge: [1,2)+[2,3)=[1,3)", len(r) == 1 && r[0] == iv.Must(1, 3))

	var m2 merge.Merger
	_ = m2.AddAll([]iv.Interval{iv.Must(5, 6), iv.Must(0, 2), iv.Must(3, 4), iv.Must(1, 3)})
	r = m2.Ranges()
	disjoint := true
	for i := 1; i < len(r); i++ {
		disjoint = disjoint && r[i-1].End <= r[i].Start
	}
	report("merge: sorted and disjoint", disjoint && len(r) == 2)

	var m3 merge.Merger
	_ = m3.AddAll([]iv.Interval{iv.Must(1, 3), iv.Must(3, 4), iv.Must(0, 2), iv.Must(5, 6)})
	report("merge: order independent", fmt.Sprint(m3.Ranges()) == fmt.Sprint(m2.Ranges()))

	var m4 merge.Merger
	var ref check.Ref
	rnd := rand.New(rand.NewPCG(1, 2))
	for range 200 {
		a := rnd.IntN(50)
		v := iv.Must(a, a+1+rnd.IntN(10))
		_ = m4.Add(v)
		_ = ref.Add(v)
	}
	report("check: coverage matches reference", fmt.Sprint(m4.Ranges()) == fmt.Sprint(ref.Ranges()))
	report("merge: len(Ranges) <= total", len(m4.Ranges()) <= m4.Total())

	var mc, ms merge.Merger
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 100 {
				_ = mc.Add(iv.Must((i*16+g)%40, (i*16+g)%40+5))
			}
		}()
	}
	wg.Wait()
	for g := range 16 {
		for i := range 100 {
			_ = ms.Add(iv.Must((i*16+g)%40, (i*16+g)%40+5))
		}
	}
	report("merge: concurrent converges", fmt.Sprint(mc.Ranges()) == fmt.Sprint(ms.Ranges()))

	_, errE := iv.New(1, 1)
	_, errI := iv.New(3, 1)
	errM := new(merge.Merger).Add(iv.Interval{Start: 4, End: 4})
	report("errors.Is distinguishes sentinels",
		errors.Is(errE, iv.ErrEmptyInterval) && !errors.Is(errE, iv.ErrInvertedInterval) &&
			errors.Is(errI, iv.ErrInvertedInterval) && errors.Is(errI, iv.ErrEmptyInterval) &&
			errors.Is(errM, merge.ErrInvalidInterval) && errors.Is(errM, iv.ErrEmptyInterval))
	if failed {
		fmt.Println("FAIL summary: some checks failed")
		os.Exit(1)
	}
	fmt.Println("OK summary: all checks passed")
}
