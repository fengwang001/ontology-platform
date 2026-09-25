package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/delta"
	"ontology/store"
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
	// delta 包：追加（含负/零）、求和读取、合并保值
	var e delta.Entry
	e.Append(10)
	e.Append(-4)
	e.Append(0)
	before := e.Sum()
	e.Compact()
	check("delta: append/sum/compact", before == 6 && e.Sum() == 6 && e.Pending() == 0)

	// store 包：多 Key、全局 Compact 保值、DeltaCount
	st, err := store.New(8)
	ok := err == nil &&
		st.Apply("a", 10) == nil && st.Apply("b", 20) == nil && st.Apply("a", -4) == nil
	ga, gb := st.Get("a"), st.Get("b")
	st.Compact()
	ok = ok && ga == 6 && gb == 20 && st.Get("a") == 6 && st.Get("b") == 20 &&
		st.DeltaCount() == 0
	check("store: multi-key compact preserves values", ok)

	// api：第三节十个操作，逐步核验 Get 与 DeltaCount
	s := api.New(16)
	var gets []int64
	var counts []int
	step := func() { counts = append(counts, s.DeltaCount()) }
	_ = s.Apply("a", 10)
	step()
	_ = s.Apply("b", 20)
	step()
	_ = s.Apply("a", -4)
	step()
	gets = append(gets, s.Get("a"))
	step()
	preA, preB := s.Get("a"), s.Get("b")
	_ = s.Compact()
	c1 := s.Get("a") == preA && s.Get("b") == preB
	step()
	_ = s.Apply("a", 7)
	step()
	_ = s.Apply("b", -5)
	step()
	gets = append(gets, s.Get("b"))
	step()
	preA, preB = s.Get("a"), s.Get("b")
	_ = s.Compact()
	c2 := s.Get("a") == preA && s.Get("b") == preB
	step()
	gets = append(gets, s.Get("a"), s.Get("b"))
	step()
	wantGets := []int64{6, 15, 13, 15}
	wantCounts := []int{1, 2, 3, 3, 0, 1, 2, 2, 0, 0}
	check(fmt.Sprintf("api: ten-op gets=%v counts=%v", gets, counts),
		fmt.Sprint(gets) == fmt.Sprint(wantGets) && fmt.Sprint(counts) == fmt.Sprint(wantCounts))
	check("api: both compacts preserve Get field-wise", c1 && c2)

	// 负 delta 与零 delta
	z := api.New(4)
	_ = z.Apply("k", 5)
	_ = z.Apply("k", -5)
	_ = z.Apply("k", 0)
	check("api: negative and zero deltas", z.Get("k") == 0 && z.DeltaCount() == 3)

	// 三类可判定错误互不相同；被拒后状态不变且可继续用
	bad := api.New(0)
	e1 := bad.Apply("x", 1)
	r := api.New(2)
	_ = r.Apply("k", 3)
	e2 := r.Apply("", 1)
	_ = r.Apply("k", 1)
	e3 := r.Apply("k", 1)
	distinct := errors.Is(e1, api.ErrInvalidMaxDeltas) && errors.Is(e2, api.ErrEmptyKey) &&
		errors.Is(e3, api.ErrCapacityExceeded) && e1 != e2 && e2 != e3 && e1 != e3
	usable := r.Get("k") == 4 && r.DeltaCount() == 2 && r.Compact() == nil &&
		r.Apply("j", 1) == nil && r.Get("j") == 1 && r.Get("k") == 4
	check("api: 3 distinct errors, state intact after rejection", distinct && usable)

	// 大 m 下 Apply 为纯尾部追加（访问数不随 m 增长的断言见 store 包测试）
	big := api.New(10001)
	for i := 0; i < 10000; i++ {
		_ = big.Apply("k", 1)
	}
	_ = big.Apply("k", 1)
	check("api: 10001 tail appends, Get=10001 (O(1) asserted in store test)",
		big.Get("k") == 10001)

	// 并发只读：N 个 goroutine 对同一已填充实例，结果逐字段一致
	keys := []string{"a", "b", "c"}
	want := map[string]int64{"a": 13, "b": 15, "c": 0}
	start := make(chan struct{})
	var wg sync.WaitGroup
	consistent := true
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for _, k := range keys {
				if s.Get(k) != want[k] {
					mu.Lock()
					consistent = false
					mu.Unlock()
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("api: concurrent reads field-wise identical", consistent && s.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
