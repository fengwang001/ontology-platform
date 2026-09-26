// Command demo prints OK/FAIL judgments for the weighted-median exercise.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"

	"ontology/api"
	"ontology/median"
	"ontology/wmid"
)

var failed bool

func report(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		failed = true
		fmt.Printf("FAIL %s\n", name)
	}
}

// readTree walks the exported read-only node view in value order.
func readTree(tr *wmid.Tree) [][2]int64 {
	var out [][2]int64
	var walk func(*wmid.Node)
	walk = func(n *wmid.Node) {
		if n == nil {
			return
		}
		walk(n.Left)
		out = append(out, [2]int64{n.Value, n.Weight})
		walk(n.Right)
	}
	walk(tr.Root())
	return out
}

func naive(elems [][2]int64) int64 {
	s := append([][2]int64(nil), elems...)
	sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] })
	var w, p int64
	for _, e := range s {
		w += e[1]
	}
	for _, e := range s {
		if p += e[1]; 2*p >= w {
			return e[0]
		}
	}
	return s[len(s)-1][0]
}

func main() {
	pairs := [][2]int64{{30, 3}, {10, 3}, {40, 3}, {20, 3}}
	tr := &wmid.Tree{}
	c := api.New()
	for _, e := range pairs {
		tr.Insert(e[0], e[1])
		c.Insert(e[0], e[1])
	}
	med, _ := c.Median()
	var p int64
	var prefix []int64
	for _, e := range readTree(tr) {
		p += e[1]
		prefix = append(prefix, p)
	}
	report(fmt.Sprintf("four-element P=%v W=%d median=%d", prefix, c.Total(), med),
		fmt.Sprint(prefix) == "[3 6 9 12]" && c.Total() == 12 && med == 20)
	var below, above int64
	for _, e := range readTree(tr) {
		switch {
		case e[0] < med:
			below += e[1]
		case e[0] > med:
			above += e[1]
		}
	}
	report("side weight bounds (both <= W/2)", 2*below <= c.Total() && 2*above <= c.Total())
	report("minimality (strictly smaller prefix < W/2)", 2*below < c.Total())
	agree := true
	r := rand.New(rand.NewSource(1))
	for t := 0; t < 50; t++ {
		d := api.New()
		var els [][2]int64
		for _, v := range r.Perm(1 + r.Intn(80)) {
			e := [2]int64{int64(v), int64(1 + r.Intn(9))}
			d.Insert(e[0], e[1])
			els = append(els, e)
		}
		if m, _ := d.Median(); m != naive(els) {
			agree = false
		}
	}
	report("naive agreement over 50 random sequences", agree)
	d := api.New()
	_, e0 := d.Median()
	e1 := d.Insert(7, 0)
	d.Insert(7, 1)
	e2 := d.Insert(7, 1)
	report("three distinct sentinel errors",
		errors.Is(e0, api.ErrEmpty) && errors.Is(e1, api.ErrNonPositiveWeight) &&
			errors.Is(e2, api.ErrDuplicateValue) && e0 != e1 && e1 != e2)
	before := d.Total()
	d.Insert(7, 5)
	d.Insert(8, -1)
	m1, _ := d.Median()
	report("rejected ops leave no trace", d.Total() == before && m1 == 7)
	report("traversal O(log m) at m=100..10000",
		median.StepBoundHolds([]int{100, 500, 1000, 5000, 10000}))
	big := api.New()
	for v := 0; v < 2000; v++ {
		big.Insert(int64(v), int64(1+(v%5)))
	}
	want, _ := big.Median()
	const N = 32
	var wg sync.WaitGroup
	res := make([]int64, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], _ = big.Median() // distinct slots: no shared mutation
		}(i)
	}
	wg.Wait()
	same := true
	for _, x := range res {
		if x != want {
			same = false
		}
	}
	report(fmt.Sprintf("%d concurrent medians identical (=%d)", N, want), same)
	if failed {
		os.Exit(1)
	}
}
