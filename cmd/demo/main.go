// Command demo prints one OK/FAIL line per deliverable check.
package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"ontology/api"
	"ontology/heap"
	"ontology/ss"
)

var failed bool

func check(name string, ok bool) {
	st := "OK"
	if !ok {
		st, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", name, st)
}

// state renders all counters sorted by key as "k:count:err ...".
func state(s *ss.Summary) string {
	es := s.TopK()
	sort.Slice(es, func(i, j int) bool { return es[i].Key < es[j].Key })
	var b []string
	for _, e := range es {
		b = append(b, fmt.Sprintf("%d:%d:%d", e.Key, e.Count, e.Err))
	}
	return strings.Join(b, " ")
}

// wrongTieQuery1 replays the canonical stream with the broken "on tie evict the largest key" rule, returning the final Query(1).
func wrongTieQuery1() int {
	cs := map[int]int{}
	for _, x := range []int{3, 1, 3, 2, 4, 1, 3, 5} {
		if _, ok := cs[x]; ok {
			cs[x]++
			continue
		}
		if len(cs) < 3 {
			cs[x] = 1
			continue
		}
		m, mn := -1, 1<<30
		for k, n := range cs { // broken rule: on tie evict largest key
			if n < mn || (n == mn && k > m) {
				m, mn = k, n
			}
		}
		delete(cs, m)
		cs[x] = mn + 1
	}
	return cs[1] // 0 if evicted
}

func main() {
	stream := []int{3, 1, 3, 2, 4, 1, 3, 5}
	want := []string{"3:1:0", "1:1:0 3:1:0", "1:1:0 3:2:0", "1:1:0 2:1:0 3:2:0",
		"2:1:0 3:2:0 4:2:1", "1:2:1 3:2:0 4:2:1", "1:2:1 3:3:0 4:2:1", "3:3:0 4:2:1 5:3:2"}
	s := ss.New(3)
	stepsOK := true
	for i, x := range stream {
		s.Add(x)
		stepsOK = stepsOK && state(s) == want[i]
	}
	check("01 eight-step states", stepsOK)
	c, _ := api.New(3)
	_ = c.Feed(stream)
	top := c.TopK() // count-desc: (3,3,0) (5,3,2) (4,2,1)
	check("02 Query(5)=3 err=2 true=1 Query(1)=0", c.Query(5) == 3 && top[1].Key == 5 && top[1].Err == 2 && c.Query(1) == 0)
	extra := -1 // key passing count>tau but failing (count-err)>tau, tau=8/3
	for _, e := range top {
		if 3*e.Count > 8 && 3*(e.Count-e.Err) <= 8 {
			extra = e.Key
		}
	}
	// (甲) err misrecorded as 0 -> count-err = Query(5) = 3; (乙) wrong tie -> 2; (丙) extra key 5.
	check("03 wrong-values a=3 b=2 c=5", c.Query(5) == 3 && wrongTieQuery1() == 2 && extra == 5)
	c2, _ := api.New(10)
	tru := map[int]int{}
	seed := uint64(7)
	for i := 0; i < 2000; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		x := int(seed>>33) % 25
		tru[x]++
		_ = c2.Feed([]int{x})
	}
	es := c2.TopK()
	min, mon := es[len(es)-1].Count, map[int]api.Entry{}
	for _, e := range es {
		mon[e.Key] = e
	}
	under, bound := true, true
	for x, tc := range tru {
		if e, ok := mon[x]; ok {
			under = under && c2.Query(x) >= tc
			bound = bound && e.Count-e.Err <= tc && tc <= e.Count
		} else {
			under = under && tc <= min // unmonitored: true <= min count
		}
	}
	check("04 no-underestimate vs naive", under)
	check("05 error bound count-err<=true<=count", bound)
	_, errK := api.New(0)
	c3, _ := api.New(3)
	errNil, errNeg := c3.Feed(nil), c3.Feed([]int{-1})
	distinct := !errors.Is(errK, errNil) && !errors.Is(errK, errNeg) && !errors.Is(errNil, errNeg)
	check("06 three distinct decidable errors", errors.Is(errK, api.ErrBadK) &&
		errors.Is(errNil, api.ErrNilFeed) && errors.Is(errNeg, api.ErrNegativeKey) && distinct)
	_ = c3.Feed([]int{7, 7, 9})
	before := fmt.Sprint(c3.TopK())
	_ = c3.Feed(nil)
	_ = c3.Feed([]int{5, -2})
	check("07 rejected feed leaves state", fmt.Sprint(c3.TopK()) == before)
	check("08 min-find comparisons O(1)", heap.SelfCheck())
	c4, _ := api.New(8)
	_ = c4.Feed([]int{1, 2, 3, 1, 2, 1, 4, 5, 6, 7, 8, 1})
	wantTop, start, res := fmt.Sprint(c4.TopK()), make(chan struct{}), make(chan bool, 16)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok := true
			for i := 0; i < 50; i++ {
				ok = ok && fmt.Sprint(c4.TopK()) == wantTop && c4.Query(1) == 4
			}
			res <- ok
		}()
	}
	close(start)
	wg.Wait()
	all := true
	for i := 0; i < 16; i++ {
		all = all && <-res
	}
	check("09 concurrent reads identical", all)
	sc, _ := api.New(4)
	check("10 SelfCheck", sc.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
