// Command demo 以 OK/FAIL 逐条验证引用计数物化状态回收的核心性质，退出码 0 表示全过。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/store"
)

var fails int

func ck(line string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag, fails = "FAIL", fails+1
	}
	fmt.Printf("%s %s %s\n", tag, line, detail)
}

func main() {
	s := store.New()
	// 第三节八步序列；want[i] = {v1refs, v2refs, alive}，v2 未出生记 -1。
	want := [8][3]int{{1, -1, 1}, {2, -1, 1}, {3, -1, 1}, {2, 1, 2}, {2, 2, 2}, {1, 2, 2}, {0, 2, 1}, {0, 2, 1}}
	var h1, h2, h3 *store.Handle
	var getErr error
	for step := 1; step <= 8; step++ { // 严格按序执行，不多一个操作
		switch step {
		case 1:
			_ = s.Publish(10)
		case 2:
			h1, _ = s.Acquire()
		case 3:
			h2, _ = s.Acquire()
		case 4:
			_ = s.Publish(20)
		case 5:
			h3, _ = s.Acquire()
		case 6:
			_ = h1.Release()
		case 7:
			_ = h2.Release()
		case 8:
			_, getErr = h1.Get()
		}
		r1 := s.CurrentRefs() // 第 1 步当前即 v1
		if step >= 2 {
			r1 = h1.Refs() // 释放后读到 0，与表一致
		}
		r2 := -1
		if step == 4 {
			r2 = s.CurrentRefs() // 此刻当前为 v2
		}
		if step >= 5 {
			r2 = h3.Refs()
		}
		alive := s.AliveCount()
		ok := r1 == want[step-1][0] && r2 == want[step-1][1] && alive == want[step-1][2]
		extra := ""
		switch step {
		case 6: // 共享不误回收：v1 refs=1 仍存活，h2.Get 仍得 10；h3 得 20
			v, e := h2.Get()
			v3, e3 := h3.Get()
			extra = fmt.Sprintf("shared: v1get=%d(err=%v) v2get=%d(err=%v)", v, e, v3, e3)
			ok = ok && e == nil && v == 10 && e3 == nil && v3 == 20
		case 7: // 零引用立即回收：无 GC 周期，alive 立即为 1
			extra = "immediate reclaim, no GC cycle"
		case 8: // use-after-free 必须可判定；幸存者 h3 不受影响
			v3, e3 := h3.Get()
			extra = fmt.Sprintf("h1.Get err=%v; survivor h3.Get=%d(err=%v)", getErr, v3, e3)
			ok = ok && errors.Is(getErr, store.ErrUseAfterFree) && e3 == nil && v3 == 20
		}
		ck(fmt.Sprintf("step%d", step), ok, fmt.Sprintf("v1refs=%d v2refs=%d alive=%d %s", r1, r2, alive, extra))
	}
	// 第 9 行：四类哨兵互不相同；全部被拒后状态不变、存储仍可正常使用。
	es := api.New()
	_, eEmpty := es.Acquire()
	eNeg := es.Publish(-1)
	eDbl := h1.Release() // h1 已在第 6 步释放 → 双重释放
	distinct := map[error]bool{store.ErrEmptyStore: true, store.ErrNegativeValue: true, store.ErrDoubleRelease: true, store.ErrUseAfterFree: true}
	before := s.AliveCount()
	_ = s.Publish(-9)
	used, uErr := h3.Get()
	noTrace := errors.Is(eEmpty, store.ErrEmptyStore) && errors.Is(eNeg, store.ErrNegativeValue) &&
		errors.Is(eDbl, store.ErrDoubleRelease) && len(distinct) == 4 &&
		s.AliveCount() == before && uErr == nil && used == 20
	ck("errors+notrace", noTrace, fmt.Sprintf("4 distinct sentinels; rejects leave alive=%d, h3 still=20", before))
	// 第 10 行：O(1) 检查（SelfCheck 内含 m=100/1000/10000）+ N goroutine 并发 Acquire/Release。
	const N = 200
	c := store.New()
	_ = c.Publish(1)
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := false
	for i := 0; i < N; i++ { // 不许用 sleep：WaitGroup 同步，-race 下计数恒为 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			h, err := c.Acquire()
			if err != nil || h.Release() != nil {
				mu.Lock()
				bad = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	conc := !bad && c.AliveCount() == 1 && c.CurrentRefs() == 1
	ck("o1+concurrent", api.New().SelfCheck() == nil && conc,
		fmt.Sprintf("selfcheck O(1) m=100..10000; %d goroutines -> refs=1 alive=1", N))
	if fails > 0 {
		os.Exit(1)
	}
}
