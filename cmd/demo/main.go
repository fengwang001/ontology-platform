package main

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/lb"
	"ontology/sched"
)

var failed bool

func report(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

// drainChecksOf 经反射读取 lb 的非导出计数器（不经过任何导出接口）。
func drainChecksOf(b *lb.Bucket) int64 {
	v := reflect.ValueOf(b).Elem().FieldByName("drainChecks")
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Int()
}

func main() {
	// 第三节八步序列：逐步放行/拒绝与出发时间；顺带核验平滑性与容量。
	l, _ := api.New(3, 5)
	want := [][3]int64{ // {t, dep, admitted}
		{0, 5, 1}, {5, 10, 1}, {6, 15, 1}, {10, 20, 1},
		{15, 25, 1}, {15, 30, 1}, {15, 0, 0}, {21, 35, 1},
	}
	eight, smooth, bounded, prev := true, true, true, int64(0)
	for _, w := range want {
		dep, ok, err := l.Submit(w[0])
		if err != nil || ok != (w[2] == 1) || (ok && dep != w[1]) {
			eight = false
		}
		if ok {
			smooth = smooth && (prev == 0 || dep-prev >= 5)
			prev = dep
		}
		bounded = bounded && l.InSystem() <= 3
	}
	report("八步序列逐步结果", eight)
	report("平滑性: 出发间隔>=interval", smooth)
	report("容量不越界", bounded)
	report("与朴素参照一致", matchNaive())

	// 三类可判定错误互不相同；被拒后状态不变。
	_, errCfg := api.New(0, 5)
	s := sched.New(lb.New(2, 5))
	s.Submit(10)
	before := s.InSystem()
	_, _, errNeg := s.Submit(-1)
	_, _, errBack := s.Submit(9)
	report("三类可判定错误", errCfg == api.ErrConfig && errNeg == sched.ErrNegativeT &&
		errBack == sched.ErrNonMonotonic && errCfg != errNeg && errNeg != errBack)
	report("被拒后状态不变", s.InSystem() == before)

	// 大 m 下 drainChecks 不随 m 增长。
	constant := true
	for _, m := range []int{100, 1000, 10000} {
		b := lb.New(int64(m)+1, 1<<40)
		for i := 0; i < m; i++ {
			b.Admit(0)
		}
		b.Drain(0)
		constant = constant && drainChecksOf(b) <= 2
	}
	report("drainChecks 不随 m 增长", constant)
	report("并发接纳计数正确", concurrent())
	report("SelfCheck", l.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

// matchNaive 用确定性伪随机序列对照朴素参照（显式出发时间列表）。
func matchNaive() bool {
	l, _ := api.New(4, 7)
	var deps []int64
	seed, t := uint64(42), int64(0)
	for i := 0; i < 300; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		t += int64(seed >> 33 % 20)
		j := 0
		for j < len(deps) && deps[j] <= t {
			j++
		}
		deps = deps[j:]
		nok := len(deps) < 4
		ndep := t + 7
		if len(deps) > 0 {
			ndep = deps[len(deps)-1] + 7
		}
		if nok {
			deps = append(deps, ndep)
		}
		dep, ok, err := l.Submit(t)
		if err != nil || ok != nok || (ok && dep != ndep) || l.InSystem() != len(deps) {
			return false
		}
	}
	return true
}

// concurrent N 个 goroutine 在同一时间戳提交：接纳数不超容量，出发时间互异且递增。
func concurrent() bool {
	const n, cap = 64, 10
	l, _ := api.New(cap, 5)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var deps []int64
	bad := false
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dep, ok, err := l.Submit(100)
			mu.Lock()
			defer mu.Unlock()
			bad = bad || err != nil
			if ok {
				deps = append(deps, dep)
			}
		}()
	}
	wg.Wait()
	if bad || len(deps) > cap {
		return false
	}
	slices.Sort(deps)
	for i := 1; i < len(deps); i++ {
		if deps[i]-deps[i-1] < 5 {
			return false
		}
	}
	return true
}
