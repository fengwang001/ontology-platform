package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"ontology/api"
	"ontology/row"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// 第三节八步序列：逐步执行并核验 View(R)
	r := row.New()
	steps := []struct {
		op   func() bool
		want map[string]string
	}{
		{func() bool { return r.Put("c1", 5, "a") }, map[string]string{"c1": "a"}},
		{func() bool { return r.Put("c2", 7, "x") }, map[string]string{"c1": "a", "c2": "x"}},
		{func() bool { return r.Put("c1", 5, "b") }, map[string]string{"c1": "b", "c2": "x"}},
		{func() bool { return r.Put("c1", 9, "c") }, map[string]string{"c1": "c", "c2": "x"}},
		{func() bool { return r.Del("c2", 8) }, map[string]string{"c1": "c"}},
		{func() bool { return r.Put("c1", 4, "old") }, map[string]string{"c1": "c"}},
		{func() bool { return r.Put("c3", 6, "y") }, map[string]string{"c1": "c", "c3": "y"}},
		{func() bool { return r.Del("c3", 2) }, map[string]string{"c1": "c", "c3": "y"}},
	}
	stepOK, tieConf, tieVal := true, false, ""
	for i, st := range steps {
		if st.op() && i == 2 {
			tieConf = true
		}
		if i == 2 {
			tieVal = r.View()["c1"]
		}
		if !reflect.DeepEqual(r.View(), st.want) {
			stepOK = false
		}
	}
	_, hasC2 := r.View()["c2"]
	check("8-step views", stepOK)
	check("step3 tie -> c1=b (conflict)", tieConf && tieVal == "b")
	check("step8 stale tombstone keeps c3=y", r.View()["c3"] == "y")
	check("step5 del c2 keeps sibling c1", !hasC2 && r.View()["c1"] == "c")

	// api：四类可判定错误互不相同，且被拒后状态不变
	s := api.New()
	_ = s.Put("R", "c1", 5, "a")
	before := s.View()
	errs := []error{s.Put("", "c", 1, "v"), s.Put("R", "", 1, "v"),
		s.Put("R", "c", -1, "v"), s.Put("R", "c", 1, "")}
	wantErrs := []error{api.ErrEmptyKey, api.ErrEmptyCol, api.ErrNegativeTS, api.ErrEmptyVal}
	distinct, errOK := map[error]bool{}, true
	for i, e := range errs {
		errOK = errOK && errors.Is(e, wantErrs[i])
		distinct[e] = true
	}
	check("4 distinguishable errors", errOK && len(distinct) == 4)
	check("rejected ops leave no trace", reflect.DeepEqual(s.View(), before))
	check("SelfCheck", api.SelfCheck() == nil)
	check("locate cost flat in m", locateConst()) // 精确计数见 row 包内测试
	check("concurrent writes consistent", concurrentOK())
	if fails > 0 {
		os.Exit(1)
	}
}

// locateConst 比较 m=100 与 m=10000 时单次 Put 最短耗时；线性扫描会差约 100 倍。
func locateConst() bool {
	timeit := func(m int) time.Duration {
		r := row.New()
		for i := 0; i < m; i++ {
			r.Put(fmt.Sprintf("c%d", i), 1, "v")
		}
		best := time.Hour
		for rep := 0; rep < 5; rep++ {
			t0 := time.Now()
			for i := 0; i < 2000; i++ {
				r.Put("c0", int64(2+i), "v")
			}
			best = min(best, time.Since(t0))
		}
		return best
	}
	return timeit(10000) <= 20*timeit(100)
}

// concurrentOK：N 个 goroutine 写同一 Key 的不同列，读循环校验任一时刻值一致；不用 sleep。
func concurrentOK() bool {
	s := api.New()
	const n = 64
	var wg sync.WaitGroup
	var bad atomic.Bool
	start, done := make(chan struct{}), make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				for col, v := range s.View()["K"] {
					if v != "v"+col[1:] {
						bad.Store(true)
					}
				}
			}
		}
	}()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_ = s.Put("K", fmt.Sprintf("c%d", i), 1, fmt.Sprintf("v%d", i))
		}(i)
	}
	close(start)
	wg.Wait()
	close(done)
	v := s.View()["K"]
	for i := 0; i < n; i++ {
		if v[fmt.Sprintf("c%d", i)] != fmt.Sprintf("v%d", i) {
			return false
		}
	}
	return len(v) == n && !bad.Load()
}
