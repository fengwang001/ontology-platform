package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	e, _ := api.New(3)
	// 第三节八步轨迹：每步 winner 与 repaired
	trace := []struct {
		put   bool
		val   string
		ver   int64
		reps  []int
		wantV string
		wantR int
	}{
		{put: true, val: "a", ver: 5, reps: []int{0, 1}},
		{wantV: "a", wantR: 1}, // 空副本 R2 回填
		{put: true, val: "b", ver: 7, reps: []int{1, 2}},
		{put: true, val: "c", ver: 7, reps: []int{2}},
		{wantV: "c", wantR: 2}, // 并列冲突取字典序更大者
		{put: true, val: "d", ver: 4, reps: []int{0}},
		{wantV: "c", wantR: 1}, // 乱序低版本写被读修复纠正
		{wantV: "c", wantR: 0}, // 收敛
	}
	ok := true
	for i, s := range trace {
		if s.put {
			ok = ok && e.Put("k", s.val, s.ver, s.reps) == nil
			continue
		}
		v, found, r, err := e.Read("k")
		ok = ok && err == nil && found && v == s.wantV && r == s.wantR
		if !ok {
			fmt.Println("FAIL trace step", i+1, v, r)
		}
	}
	check("8-step trace (empty-backfill/tie/late-write/converge)", ok)

	before := fmt.Sprint(e.Snapshot("k"))
	errs := []error{
		e.Put("k", "x", 0, []int{0}),
		e.Put("", "x", 1, []int{0}),
		e.Put("k", "x", 1, nil),
		e.Put("k", "x", 1, []int{3}),
	}
	check("4 distinct sentinel errors", errors.Is(errs[0], api.ErrBadVersion) &&
		errors.Is(errs[1], api.ErrEmptyKey) && errors.Is(errs[2], api.ErrBadReplicas) &&
		errors.Is(errs[3], api.ErrBadReplicas))
	_, nerr := api.New(0)
	check("New(0) rejected", errors.Is(nerr, api.ErrBadN))
	check("state unchanged after rejects", before == fmt.Sprint(e.Snapshot("k")))

	big, _ := api.New(10000)
	all := make([]int, 10000)
	for i := range all {
		all[i] = i
	}
	_ = big.Put("g", "lo", 1, all)
	_ = big.Put("g", "hi", 9, []int{3, 4})
	v, _, r, _ := big.Read("g")
	check("big-m read correct (O(1) cmp proven in rrep test)", v == "hi" && r == 9998)

	c, _ := api.New(4)
	_ = c.Put("hot", "x", 2, []int{0, 1})
	_, _, _, _ = c.Read("hot") // 先收敛
	want, _, wantR, _ := c.Read("hot")
	start := make(chan struct{})
	var wg sync.WaitGroup
	identical := true
	var mu sync.Mutex
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, _, r, _ := c.Read("hot")
			mu.Lock()
			identical = identical && v == want && r == wantR
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent reads identical", identical)
	check("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
