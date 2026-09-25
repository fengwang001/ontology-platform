package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/casc"
	"ontology/ent"
)

var scenario = [][2]int{
	{1, 0}, {2, 1}, {3, 1}, {4, 2}, {5, 2},
	{6, 3}, {7, 4}, {8, 0}, {9, 8}, {10, 99},
}

func seed() *casc.Set { // Insert allows the never-existed parent 99 (orphan 10)
	s := casc.New()
	for _, e := range scenario {
		if err := s.Insert(e[0], e[1]); err != nil {
			panic(err)
		}
	}
	return s
}

func main() {
	fail := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
		} else {
			fmt.Println("FAIL " + name)
			fail++
		}
	}

	s := seed()
	d1, err := s.Delete(1)
	check("Delete(1) seven-step post-order", err == nil &&
		slices.Equal(d1, []int{7, 4, 5, 2, 6, 3, 1}))
	s2 := seed()
	d2, err := s2.Delete(2)
	check("Delete(2) order", err == nil && slices.Equal(d2, []int{7, 4, 5, 2}))
	check("Orphans()=[10]", slices.Equal(seed().Orphans(), []int{10}))

	c := casc.New() // orphan chain: cleanup must cascade 20->21->22
	for _, e := range [][2]int{{20, 77}, {21, 20}, {22, 21}} {
		_ = c.Insert(e[0], e[1])
	}
	check("CleanupOrphans cascade", slices.Equal(c.CleanupOrphans(), []int{22, 21, 20}))

	a := api.New()
	_ = a.Add(1, 0)
	snap := func() string {
		b := ""
		for id := 0; id <= 12; id++ {
			b += strconv.FormatBool(a.Exists(id))
		}
		return b
	}
	before := snap()
	errs := []error{a.Add(0, 0), a.Add(1, 0), a.Add(2, 98)}
	_, e4 := a.Delete(999)
	errs = append(errs, e4)
	fourDistinct := errors.Is(errs[0], ent.ErrInvalidID) &&
		errors.Is(errs[1], ent.ErrDuplicateID) &&
		errors.Is(errs[2], ent.ErrInvalidParent) &&
		errors.Is(errs[3], casc.ErrNotFound)
	check("four distinct decidable errors", fourDistinct)
	check("rejected ops leave no trace", snap() == before && a.Add(2, 1) == nil)

	sizes := map[int]int{}
	bounded := true
	for _, m := range []int{100, 1000, 10000} {
		b := seed()
		for i := range m {
			if err := b.Add(1000+i, 0); err != nil {
				bounded = false
			}
		}
		got, err := b.Delete(1)
		if err != nil {
			bounded = false
		}
		sizes[m] = len(got)
		if len(got) != 7 {
			bounded = false
		}
	}
	check(fmt.Sprintf("traversal bounded by subtree (sizes=%v)", sizes), bounded)

	r := seed()
	want := r.Orphans()
	const n = 16
	var wg sync.WaitGroup
	var agree atomic.Bool
	agree.Store(true)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			for range 100 {
				if !slices.Equal(r.Orphans(), want) || !r.Exists(1) {
					agree.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent readers agree", agree.Load())
	check("SelfCheck passes all four invariants", api.New().SelfCheck() == nil)

	if fail > 0 {
		os.Exit(1)
	}
}
