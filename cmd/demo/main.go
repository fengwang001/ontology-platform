package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/bucket"
	"ontology/hist"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	h, err := api.New(10, 0)
	if err != nil {
		fmt.Println("FAIL api.New:", err)
		os.Exit(1)
	}
	vals := []int{5, 12, 20, 10, -3, -15}
	steps := [][]int{{1}, {1, 1}, {1, 1, 1}, {1, 2, 1}, {1, 1, 2, 1}, {1, 1, 1, 2, 1}}
	stepOK := true
	for i, v := range vals {
		h.Insert(v)
		lo, hi, _ := h.Range()
		got := []int{}
		for k := lo; k <= hi; k++ {
			got = append(got, h.BucketCount(k))
		}
		stepOK = stepOK && slices.Equal(got, steps[i])
	}
	check("six-step counts", stepOK)
	check("floor(-3/10)=-1 not 0", h.BucketCount(-1) == 1 && h.BucketCount(0) == 1)
	check("boundary 20 -> bucket 2", h.BucketCount(2) == 1 && h.BucketCount(1) == 2)
	check("total == batch recompute", batchEqual(vals))
	lo, hi, ok := h.Range()
	check("range [-2,2] complete", ok && lo == -2 && hi == 2 && h.BucketCount(-3) == 0 && h.BucketCount(3) == 0)
	_, err = api.New(0, 0)
	check("width<=0 decidable", errors.Is(err, api.ErrNonPositiveWidth))
	before := h.Total()
	bad, _ := api.New(-5, 0)
	check("rejected new leaves state", bad == nil && h.Total() == before && h.BucketCount(1) == 2)
	check("locate bound independent of m", hist.VerifyLocateBound([]int{100, 1000, 10000}, 2))
	check("concurrent reads consistent", concurrent(h))
	check("selfcheck", h.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

// batchEqual 新建实例重放 vals，核验 Total 与逐桶计数都和批量重算一致。
func batchEqual(vals []int) bool {
	h, err := api.New(10, 0)
	if err != nil {
		return false
	}
	batch := map[int]int{}
	for _, v := range vals {
		h.Insert(v)
		batch[bucket.Number(v, 0, 10)]++
	}
	if h.Total() != len(vals) {
		return false
	}
	for k, c := range batch {
		if h.BucketCount(k) != c {
			return false
		}
	}
	return true
}

// concurrent 让 8 个 goroutine 并发只读同一实例，结果必须逐字段相同。
func concurrent(h *api.Histogram) bool {
	const n = 8
	want := map[int]int{-2: 1, -1: 1, 0: 1, 1: 2, 2: 1}
	start := make(chan struct{})
	res := make(chan bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok := h.Total() == 6 && h.SelfCheck() == nil
			lo, hi, rOk := h.Range()
			ok = ok && rOk && lo == -2 && hi == 2
			for k := lo; k <= hi; k++ {
				ok = ok && h.BucketCount(k) == want[k]
			}
			res <- ok
		}()
	}
	close(start)
	wg.Wait()
	close(res)
	for r := range res {
		if !r {
			return false
		}
	}
	return true
}
