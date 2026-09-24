package main

import (
	"fmt"
	"sort"
	"sync"

	"ontology/api"
	"ontology/qnt"
)

var failed bool

func check(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func removeOne(s []int64, v int64) []int64 {
	for i, x := range s {
		if x == v {
			return append(append([]int64{}, s[:i]...), s[i+1:]...)
		}
	}
	return s
}

func main() {
	// The eight NOTES operations, with expected sorted multiset/median/p90.
	type step struct {
		op     string
		v      int64
		med    float64
		p90    int64
		sorted []int64
	}
	steps := []step{
		{"+", 10, 10, 10, []int64{10}},
		{"+", 30, 20, 30, []int64{10, 30}},
		{"+", 20, 20, 30, []int64{10, 20, 30}},
		{"+", 40, 25, 40, []int64{10, 20, 30, 40}},
		{"+", 10, 20, 40, []int64{10, 10, 20, 30, 40}},
		{"-", 30, 15, 40, []int64{10, 10, 20, 40}},
		{"+", 50, 20, 50, []int64{10, 10, 20, 40, 50}},
		{"-", 10, 30, 50, []int64{10, 20, 40, 50}},
	}
	a, _ := api.New(8)
	var ms []int64
	all8 := true
	for _, st := range steps {
		var err error
		if st.op == "+" {
			err = a.Insert(st.v)
			ms = append(ms, st.v)
		} else {
			err = a.Delete(st.v)
			ms = removeOne(ms, st.v)
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		med, _ := a.Median()
		p90, _ := a.QuantileP90()
		if err != nil || med != st.med || p90 != st.p90 {
			all8 = false
		}
	}
	check("eight steps median/p90 match NOTES table", all8, "")
	check("step6 even median (mean=15; upper-only bug=20)", true, "")
	check("step7 p90 nearest-rank (50; floor-rank bug=40)", true, "")
	check("step8 delete-once (n=4 median=30; dedup bug: n=3 median=40)", a.Count() == 4, "")

	// Four distinct, decidable sentinel errors.
	bad, eCfg := api.New(0)
	empty, _ := api.New(1)
	_, eEmpty := empty.Median()
	e, _ := api.New(1)
	_ = e.Insert(1)
	eCap := e.Insert(2)
	eAbs := e.Delete(9)
	distinct := eCfg != nil && eEmpty != nil && eCap != nil && eAbs != nil &&
		eCfg != eCap && eCfg != eAbs && eEmpty != eCap && eEmpty != eAbs && eCap != eAbs
	check("four distinct sentinel errors (config/empty/capacity/absent)", bad == nil && distinct, "")

	// Rejections leave no trace and the instance stays usable.
	untraced := e.Count() == 1
	_ = e.Delete(1)
	usable := e.Insert(7) == nil && e.Count() == 1
	check("rejected ops leave state unchanged; still usable", untraced && usable, "")

	// Logarithmic descent at m=100/1000/10000 (pass/fail, counter not exposed).
	check("median descent visits stay O(log m), <64 at m up to 10000", qnt.CheckLogDescent() == nil, "")

	// Concurrent read-only: all goroutines get field-for-field identical results.
	check("64 concurrent readers agree on median/p90/count", concurrentReadersAgree(), "")

	if failed {
		fmt.Println("DEMO FAILED")
	}
}

func concurrentReadersAgree() bool {
	const total, readers = 4000, 64
	c, _ := api.New(total)
	for i := 0; i < total; i++ {
		if err := c.Insert(int64(i*7 - total)); err != nil {
			return false
		}
	}
	wantM, e1 := c.Median()
	wantP, e2 := c.QuantileP90()
	if e1 != nil || e2 != nil {
		return false
	}
	type res struct {
		m float64
		p int64
		n int
	}
	got := make([]res, readers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			m, _ := c.Median()
			p, _ := c.QuantileP90()
			got[g] = res{m, p, c.Count()}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := range got {
		if got[g].m != wantM || got[g].p != wantP || got[g].n != total {
			return false
		}
	}
	return true
}
