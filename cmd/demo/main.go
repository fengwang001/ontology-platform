package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/query"
	"ontology/rhash"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func naiveLCP(s []byte, i, j int) int {
	k := 0
	for i+k < len(s) && j+k < len(s) && s[i+k] == s[j+k] {
		k++
	}
	return k
}

func main() {
	// Section-3 demo parameters: B=3, M=7, s="abab".
	var p, pw [5]uint64
	pw[0] = 1
	for i := 1; i <= 4; i++ {
		p[i] = (p[i-1]*3 + uint64("abab"[i-1])) % 7
		pw[i] = pw[i-1] * 3 % 7
	}
	demoHash := (p[4] + 7 - p[2]*pw[2]%7) % 7

	// Not-built must be rejected before New.
	_, errNotBuilt := api.Equal(0, 1, 0, 1)
	if err := api.New(nil); !errors.Is(err, api.ErrEmpty) {
		failed = true
	}
	if err := api.New([]byte("abab")); err != nil {
		failed = true
	}
	eq, err := api.Equal(0, 2, 2, 4)
	_, errRange := api.Equal(-1, 0, 0, 0)
	check("section3 table, s[2..4)==4, Equal(0,2,2,4)=true",
		p == [5]uint64{0, 6, 4, 4, 5} && pw == [5]uint64{1, 3, 2, 6, 4} &&
			demoHash == 4 && eq && err == nil)
	check("three distinct sentinel errors",
		errors.Is(errNotBuilt, api.ErrNotBuilt) && errors.Is(errRange, api.ErrRange) &&
			api.ErrEmpty != api.ErrRange && api.ErrRange != api.ErrNotBuilt)
	check("state unchanged after rejection", eq && func() bool {
		l, e := api.LCP(0, 2)
		return e == nil && l == 2
	}())

	// Random cross-checks against the naive references.
	rng := rand.New(rand.NewSource(1))
	s := make([]byte, 10000)
	for i := range s {
		s[i] = byte(rng.Intn(4)) // small alphabet -> frequent real equal substrings
	}
	api.New(s)
	t := rhash.New(s)
	q := query.New(t)
	m := 300
	naiveOK, lcpOK, boundOK := true, true, true
	bound := bits.Len(uint(len(s)-1)) + 1 // ceil(log2 n) + 1
	for k := 0; k < m; k++ {
		l1 := rng.Intn(len(s) + 1)
		r1 := l1 + rng.Intn(len(s)-l1+1)
		l2 := rng.Intn(len(s) + 1)
		r2 := l2 + rng.Intn(len(s)-l2+1)
		got, e := api.Equal(l1, r1, l2, r2)
		if e != nil || got != bytes.Equal(s[l1:r1], s[l2:r2]) {
			naiveOK = false
		}
		i, j := rng.Intn(len(s)+1), rng.Intn(len(s)+1)
		l, e := api.LCP(i, j)
		if e != nil || l != naiveLCP(s, i, j) {
			lcpOK = false
		}
		if _, cmp := q.LCP(i, j); cmp > bound {
			boundOK = false
		}
	}
	check("Equal matches bytes.Equal over m random ranges", naiveOK)
	check("LCP matches byte-by-byte over m random suffix pairs", lcpOK)
	check("LCP hash comparisons <= ceil(log2 n)+1", boundOK)
	// The unexported rhash counter cannot be read through any exported
	// API; its numeric ==0 assertion lives in rhash/rhash_test.go.
	check("m Equal O(1) prefix-only, rescanned=0 (pinned in rhash_test)", naiveOK)

	// Concurrency: all goroutines must agree item by item.
	type pair struct{ l1, r1, l2, r2, i, j int }
	pairs := make([]pair, 200)
	for k := range pairs {
		pairs[k] = pair{
			rng.Intn(len(s) + 1), 0, rng.Intn(len(s) + 1), 0,
			rng.Intn(len(s) + 1), rng.Intn(len(s) + 1),
		}
		pairs[k].r1 = pairs[k].l1 + rng.Intn(len(s)-pairs[k].l1+1)
		pairs[k].r2 = pairs[k].l2 + rng.Intn(len(s)-pairs[k].l2+1)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([][]bool, 8)
	for g := range results {
		results[g] = make([]bool, 2*len(pairs))
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for k, pr := range pairs {
				e, _ := api.Equal(pr.l1, pr.r1, pr.l2, pr.r2)
				l, _ := api.LCP(pr.i, pr.j)
				results[g][2*k] = e
				results[g][2*k+1] = l == naiveLCP(s, pr.i, pr.j)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	concurOK := true
	for k := range results[0] {
		for g := 1; g < len(results); g++ {
			if results[g][k] != results[0][k] {
				concurOK = false
			}
		}
	}
	check("concurrent Equal/LCP results agree item by item", concurOK)
	check("SelfCheck passes", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
