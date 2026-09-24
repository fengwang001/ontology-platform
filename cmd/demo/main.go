package main

import (
	"fmt"
	"math/rand"
	"os"
	"slices"
	"sync"

	"ontology/mono"
	"ontology/nge"
)

const limit = 1 << 20

func check(name string, ok bool) {
	if !ok {
		fmt.Println("FAIL:", name)
		os.Exit(1)
	}
	fmt.Println("OK:", name)
}
func main() {
	sc, _ := mono.NewScanner(3)
	victim := []int{3, 1, 2, 0}
	before := slices.Clone(victim)
	_, e1 := sc.Scan(nil)
	_, e2 := sc.Scan(victim)
	_, e3 := mono.NewScanner(0)
	reuse, rerr := sc.Scan([]int{2, 1, 3})
	check("three distinct sentinel errors", e1 == mono.ErrNilInput && e2 == mono.ErrTooLong && e3 == mono.ErrBadLimit && e1 != e2 && e2 != e3)
	check("rejected input untouched; scanner reusable", slices.Equal(victim, before) && rerr == nil && slices.Equal(reuse, []int{2, 2, -1}))
	rr := rand.New(rand.NewSource(7))
	rnd := make([]int, 2000)
	for i := range rnd {
		rnd[i] = rr.Intn(100)
	}
	got, _ := nge.NextGreater(rnd, limit)
	check("random matches naive + SelfCheck", slices.Equal(got, naive(rnd)) && nge.SelfCheck(rnd, got))
	a223, _ := nge.NextGreater([]int{2, 2, 3}, limit)
	check("[2,2,3] strict -> [2 2 -1]", slices.Equal(a223, []int{2, 2, -1}))
	eq, _ := nge.NextGreater(build("eq", 500), limit)
	inc, _ := nge.NextGreater(build("inc", 500), limit)
	dec, _ := nge.NextGreater(build("dec", 500), limit)
	check("all-equal -> all None", slices.Max(eq) == nge.None)
	check("strict inc/dec match naive", slices.Equal(inc, naive(build("inc", 500))) && slices.Equal(dec, naive(build("dec", 500))))
	check("None=-1 never equals legal index 0", inc[len(inc)-1] == nge.None && eq[0] == nge.None)
	opsOK := true
	for _, n := range []int{1000, 100000} { // pushes == n; pops == answered slots
		for _, sh := range []string{"inc", "dec", "eq"} {
			res, err := nge.NextGreater(build(sh, n), limit)
			answered := 0
			for _, j := range res {
				if j != nge.None {
					answered++
				}
			}
			opsOK = opsOK && err == nil && n+answered <= 2*n
		}
	}
	check("push+pop <= 2n (n=1000,100000)", opsOK)
	gots := make([][]int, 8)
	wg, start := sync.WaitGroup{}, make(chan struct{})
	for k := range gots {
		wg.Add(1)
		go func(k int) { defer wg.Done(); <-start; gots[k], _ = nge.NextGreater(rnd, limit) }(k)
	}
	close(start)
	wg.Wait()
	same := true
	for _, g := range gots {
		same = same && slices.Equal(g, got)
	}
	check("concurrent NextGreater results identical", same)
}
func naive(a []int) []int {
	ans := make([]int, len(a))
	for i := range a {
		ans[i] = nge.None
		for j := i + 1; j < len(a) && ans[i] == nge.None; j++ {
			if a[j] > a[i] {
				ans[i] = j
			}
		}
	}
	return ans
}
func build(shape string, n int) []int {
	a := make([]int, n)
	for i := range a {
		a[i] = 7
		if shape == "inc" {
			a[i] = i
		}
		if shape == "dec" {
			a[i] = n - i
		}
	}
	return a
}
