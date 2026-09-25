package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/bucket"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	vals := []int{5, 12, 20, 10, -3, -15}
	wantK := []int{0, 1, 2, 1, -1, -2}
	ok := true
	for i, v := range vals {
		ok = ok && bucket.Index(10, 0, v) == wantK[i]
	}
	check("负值与右边界桶号判定", ok)

	// 第三节六步每步之后的桶计数
	steps := []map[int]int{
		{0: 1},
		{0: 1, 1: 1},
		{0: 1, 1: 1, 2: 1},
		{0: 1, 1: 2, 2: 1},
		{-1: 1, 0: 1, 1: 2, 2: 1},
		{-2: 1, -1: 1, 0: 1, 1: 2, 2: 1},
	}
	h, err := api.New(10, 0)
	ok = err == nil
	for i, v := range vals {
		h.Insert(v)
		for k := -3; k <= 3; k++ {
			ok = ok && h.BucketCount(k) == steps[i][k]
		}
	}
	check("六步每步之后桶计数", ok)

	batch := map[int]int{}
	for _, v := range vals {
		batch[bucket.Index(10, 0, v)]++
	}
	total := 0
	for k := -3; k <= 3; k++ {
		total += batch[k]
		ok = ok && h.BucketCount(k) == batch[k]
	}
	check("Total 与批量重算一致", h.Total() == total && h.Total() == 6)

	min, max, rok := h.Range()
	check("Range 完整", rok && min == -2 && max == 2 &&
		h.BucketCount(min-1) == 0 && h.BucketCount(max+1) == 0)

	bad, err := api.New(0, 0)
	check("width<=0 可判定错误", bad == nil && errors.Is(err, api.ErrNonPositiveWidth))
	before := h.Total()
	_, _ = api.New(-5, 0)
	check("被拒后状态不变", h.Total() == before && h.BucketCount(1) == 2)

	for _, m := range []int{100, 1000, 10000} {
		g, _ := api.New(1, 0)
		for i := 0; i < m; i++ {
			g.Insert(i * 1000)
		}
		g.Insert(-1)
		ok = ok && g.Total() == m+1
	}
	check("大m直接定位(计数器见hist测试)", ok)

	const n = 16
	var wg sync.WaitGroup
	cons := true
	start := make(chan struct{})
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			mu.Lock()
			cons = cons && h.Total() == 6 && h.BucketCount(1) == 2 && h.SelfCheck() == nil
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	check("并发只读结果一致", cons && h.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
