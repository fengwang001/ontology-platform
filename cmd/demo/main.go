// Command demo exercises the two-level perfect hash; exit 0 only if all pass.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/first"
)

var failed bool

func check(name, detail string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s %s\n", name, status, detail)
}

// probesOf peeks the unexported probe counter without any exported API.
func probesOf(tbl *api.Table) int64 {
	v := reflect.ValueOf(tbl).Elem().FieldByName("t")
	v = v.Elem().FieldByName("probes")
	return *(*int64)(unsafe.Pointer(v.UnsafeAddr()))
}

func randKeys(rng *rand.Rand, n, bound int) []int {
	set := make(map[int]struct{}, n)
	for len(set) < n {
		set[rng.Intn(bound)] = struct{}{}
	}
	out := make([]int, 0, n)
	for k := range set {
		out = append(out, k)
	}
	return out
}

func main() {
	keys := []int{5, 11, 13, 17, 19, 24}
	tbl, err := api.Build(keys, 6, 29)
	if err != nil {
		fmt.Println("build: FAIL", err)
		os.Exit(1)
	}
	naive := map[int]bool{}
	for _, k := range keys {
		naive[k] = true
	}
	// 1. section-3 derivation: bucket split and second-level slots
	st := tbl.Second()
	ok := first.Bucket(5, 6) == 5 && first.Bucket(24, 6) == 0
	d1, a1, b1, s1 := st.Describe(1)
	d5, a5, b5, s5 := st.Describe(5)
	d0, _, _, _ := st.Describe(0)
	ok = ok && !d1 && a1 == 1 && b1 == 0 && s1 == 4 && st.Slot(1, 13) == 1 && st.Slot(1, 19) == 3
	ok = ok && !d5 && a5 == 1 && b5 == 0 && s5 == 9 &&
		st.Slot(5, 5) == 5 && st.Slot(5, 11) == 2 && st.Slot(5, 17) == 8 && d0
	check("derive", "B1{13>1,19>3} B5{5>5,11>2,17>8} B0=direct", ok)
	// 2. no-collision lookup: every built key found
	ok = true
	for _, k := range keys {
		if found, _ := tbl.Lookup(k); !found {
			ok = false
		}
	}
	check("no-collision", "6/6 built keys found", ok)
	// 3. naive reference agreement over a sweep
	ok = true
	for x := 0; x <= 30; x++ {
		if found, _ := tbl.Lookup(x); found != naive[x] {
			ok = false
		}
	}
	check("naive-ref", "x in [0,30] matches map", ok)
	// 4. space: sum of n_j^2
	got, want := st.Space()
	check("space", fmt.Sprintf("sum=%d want=%d", got, want), got == want && want == 13)
	// 5. four distinguishable rejection errors
	_, eDup := api.Build([]int{2, 2}, 4, 5)
	_, eEmp := api.Build(nil, 4, 5)
	_, eInv := api.Build([]int{1}, 0, 5)
	_, eNF := tbl.Lookup(7)
	ok = errors.Is(eDup, api.ErrDuplicateKey) && errors.Is(eEmp, api.ErrEmptyKeys) &&
		errors.Is(eInv, api.ErrInvalidParam) && errors.Is(eNF, api.ErrNotFound) &&
		!errors.Is(eDup, api.ErrNotFound) && !errors.Is(eEmp, api.ErrInvalidParam) &&
		!errors.Is(eNF, api.ErrDuplicateKey)
	check("errors", "dup/empty/invalid/notfound distinct", ok)
	// 6. rejected operations left the built table untouched
	ok = tbl.Size() == 6
	for _, k := range keys {
		if found, _ := tbl.Lookup(k); !found {
			ok = false
		}
	}
	check("state-intact", "size=6 after rejections", ok)
	// 7. constant probe count for large prime m
	ok = true
	rng := rand.New(rand.NewSource(1))
	for _, m := range []int{101, 1009, 9973} {
		bk := randKeys(rng, m, 10*m)
		big, err := api.Build(bk, m, 100003)
		if err != nil {
			ok = false
			break
		}
		if found, _ := big.Lookup(bk[0]); !found || probesOf(big) > 2 {
			ok = false
		}
	}
	check("probes", "<=2 for m=101..9973", ok)
	// 8. concurrent read-only lookups agree with the naive map
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			good := true
			for i := 0; i < 500; i++ {
				x := r.Intn(31)
				if found, _ := tbl.Lookup(x); found != naive[x] {
					good = false
				}
			}
			results <- good
		}(int64(g))
	}
	wg.Wait()
	close(results)
	ok = true
	for good := range results {
		ok = ok && good
	}
	check("concurrent", "8 goroutines agree with naive", ok)
	// 9. self check
	check("selfcheck", "four invariants", tbl.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
