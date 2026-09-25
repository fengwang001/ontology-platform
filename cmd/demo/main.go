// Command demo exercises the RMQ end to end; no args, no network.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/rmq"
	"ontology/seg"
)

var failed bool

func check(name string, ok bool, detail string) {
	mark := "OK"
	if !ok {
		mark, failed = "FAIL", true
	}
	fmt.Printf("%s %s %s\n", mark, name, detail)
}

func main() {
	q, err := api.New([]int64{5, 2, 8, 1, 9, 3, 7, 4})
	if err != nil {
		panic(err)
	}
	// Eight-step table from NOTES.md section 3.
	v1, _ := q.Query(0, 8)
	q.Update(0, 10)
	v3, _ := q.Query(0, 4) // value a leaf-only (non-propagating) update would freeze
	q.Update(3, 11)
	v5, _ := q.Query(0, 4)
	v6, _ := q.Query(4, 8)
	q.Update(2, 0)
	v8, _ := q.Query(0, 8)
	check("8steps", v1 == 1 && v3 == 1 && v5 == 2 && v6 == 3 && v8 == 0,
		fmt.Sprintf("%d %d %d %d %d", v1, v3, v5, v6, v8))

	half, _ := q.Query(0, 2)   // correct half-open [0,2)
	closed, _ := q.Query(0, 3) // closed-interval bug would also count index 2
	check("half-open[0,2)=2 vs closedbug=0", half == 2 && closed == 0,
		fmt.Sprintf("half=%d closedbug=%d", half, closed))
	check("stale-no-propagate=1 vs correct=2", v3 == 1 && v5 == 2,
		fmt.Sprintf("stale=%d correct=%d", v3, v5))
	empty, _ := q.Query(3, 3)
	check("empty[3,3)=+Inf not A[3]=11", empty == seg.Inf, fmt.Sprintf("empty=%d", empty))

	r5, _ := api.New([]int64{5, 2, 8, 1, 9}) // n=5 padded to 8
	p5, _ := r5.Query(0, 5)
	check("non-pow2 padding stays +Inf", p5 == 1, fmt.Sprintf("min=%d (0 would mean polluted)", p5))
	check("naive scan agreement", scanEqual([]int64{3, 1, 4, 1, 5, 9, 2, 6}), "")
	r8, _ := api.New([]int64{5, 2, 8, 1, 9, 3, 7, 4})
	r8.Update(3, 11)
	r8.Update(2, 0)
	check("correct after updates", scanEqualCur(r8, []int64{5, 2, 0, 11, 9, 3, 7, 4}), "")

	_, e1 := q.Query(2, 1)
	_, e2 := q.Query(-1, 0)
	e3 := q.Update(8, 0)
	_, e4 := api.New(nil)
	distinct := errors.Is(e1, api.ErrInvalidRange) && errors.Is(e2, api.ErrRangeOutOfBounds) &&
		errors.Is(e3, api.ErrIndexOutOfBounds) && errors.Is(e4, api.ErrEmptyInput)
	before, _ := q.Query(0, 8)
	check("4 distinct sentinels; rejected ops leave state", distinct && before == v8,
		fmt.Sprintf("root still %d", before))
	check("query visits O(log n) nodes", rmq.VerifyLogAccess() == nil, "")
	check("concurrent readers agree", concurrentAgree(), "")

	if failed {
		os.Exit(1)
	}
}

func scanEqual(arr []int64) bool {
	r, err := api.New(arr)
	return err == nil && scanEqualCur(r, arr)
}

func scanEqualCur(r *api.RMQ, arr []int64) bool {
	for l := 0; l <= len(arr); l++ {
		for rr := l; rr <= len(arr); rr++ {
			got, err := r.Query(l, rr)
			if err != nil {
				return false
			}
			want := int64(math.MaxInt64)
			for k := l; k < rr; k++ {
				if arr[k] < want {
					want = arr[k]
				}
			}
			if got != want {
				return false
			}
		}
	}
	return true
}

// concurrentAgree: 32 goroutines read the same RMQ; sync by WaitGroup only.
func concurrentAgree() bool {
	r, _ := api.New([]int64{7, 2, 5, 1, 8, 3, 9, 4, 0, 6, 2})
	queries := [][2]int{{0, 11}, {2, 7}, {0, 1}, {5, 5}, {3, 10}}
	ref := answers(r, queries)
	var wg sync.WaitGroup
	ok := make(chan bool, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := answers(r, queries)
			same := len(got) == len(ref)
			for i := range ref {
				same = same && got[i] == ref[i]
			}
			ok <- same
		}()
	}
	wg.Wait()
	close(ok)
	for v := range ok {
		if !v {
			return false
		}
	}
	return true
}

func answers(r *api.RMQ, qs [][2]int) []int64 {
	out := make([]int64, len(qs))
	for i, qd := range qs {
		v, err := r.Query(qd[0], qd[1])
		if err != nil {
			return nil
		}
		out[i] = v
	}
	return out
}
