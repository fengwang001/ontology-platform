// Command demo prints OK/FAIL lines for every deliverable check.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"sync"

	"ontology/api"
	"ontology/idx"
	"ontology/inj"
)

var (
	S = []int{2, 3, 3, 3, 5, 7}
	R = []int{3, 5, 1, 3}
)

var failed bool

func ok(cond bool, format string, args ...any) {
	tag := "OK"
	if !cond {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf(format+" "+tag+"\n", args...)
}

func main() {
	x, err := idx.Build(S)
	if err != nil {
		fmt.Println("build index FAIL:", err)
		os.Exit(1)
	}
	sess := api.New()
	if err := sess.BuildIndex(S); err != nil {
		fmt.Println("BuildIndex FAIL:", err)
		os.Exit(1)
	}
	wantN := []int{3, 1, 0, 3} // four probe steps: lo, hi, match count
	total := 0
	for i, k := range R {
		lo, hi := x.Bounds(k, nil)
		total += hi - lo
		tail := ""
		if i == len(R)-1 {
			tail = fmt.Sprintf(" | total=%d", total)
		}
		ok(hi-lo == wantN[i], "step%d r=%d lo=%d hi=%d n=%d%s", i+1, k, lo, hi, hi-lo, tail)
	}
	// (甲)(乙)(丙): the wrong values of the three misimplementations.
	ok(bugStrictLo() == 0 && bugDedup() == 3 && bugCursor() == 4,
		"wrong-values strict-lo=%d dedup=%d cursor=%d (want 0/3/4)", bugStrictLo(), bugDedup(), bugCursor())
	got, err := sess.Probe(R)
	ok(err == nil && slices.Equal(got, naive()), "naive-consistent pairs=%d", len(got))
	ok(errors.Is(sess.BuildIndex([]int{-1}), api.ErrNegativeKey) &&
		errors.Is(sess.BuildIndex([]int{2, 1}), api.ErrNotSorted) &&
		errors.Is(sess.BuildIndex(nil), api.ErrNilInput) &&
		!errors.Is(api.ErrNegativeKey, api.ErrNotSorted) &&
		!errors.Is(api.ErrNotSorted, api.ErrNilInput), "errors distinguishable")
	again, err2 := sess.Probe(R)
	ok(err2 == nil && slices.Equal(got, again), "state unchanged after rejects")
	ok(inj.CheckCost() == nil, "log-bound probe cost for m=100..10000")
	ok(concurrentConsistent(sess), "concurrent read-only probes identical")
	if failed {
		os.Exit(1)
	}
}

// bugStrictLo: lower bound miswritten as "first > k" (甲).
func bugStrictLo() (n int) {
	for _, k := range R {
		lo := sort.Search(len(S), func(i int) bool { return S[i] > k })
		hi := sort.Search(len(S), func(i int) bool { return S[i] > k })
		n += hi - lo
	}
	return n
}

// bugDedup: index keeps only the first position per key (乙).
func bugDedup() (n int) {
	first := map[int]bool{}
	for _, k := range S {
		first[k] = true
	}
	for _, k := range R {
		if first[k] {
			n++
		}
	}
	return n
}

// bugCursor: monotone non-rewinding cursor instead of binary search (丙).
func bugCursor() (n int) {
	cur := 0
	for _, k := range R {
		for cur < len(S) && S[cur] < k {
			cur++
		}
		for cur < len(S) && S[cur] == k {
			cur++
			n++
		}
	}
	return n
}

func naive() (out []api.Pair) {
	for _, rk := range R {
		for _, sk := range S {
			if rk == sk {
				out = append(out, api.Pair{R: rk, S: sk})
			}
		}
	}
	return out
}

func concurrentConsistent(sess *api.Session) bool {
	want, err := sess.Probe(R)
	if err != nil {
		return false
	}
	var wg sync.WaitGroup
	bad := make(chan struct{}, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := sess.Probe(R)
			if err != nil || !slices.Equal(got, want) {
				bad <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(bad)
	return len(bad) == 0
}
