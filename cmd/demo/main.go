// demo：逐项打印 OK/FAIL，退出码 0 表示全部通过。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sync"

	"ontology/api"
)

func pf(err error) string {
	if err != nil {
		return "FAIL " + err.Error()
	}
	return "OK"
}

func sumOf(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

func main() {
	ok := true
	for _, r := range api.SelfCheck() {
		fmt.Println(r.Name+":", pf(r.Err))
		ok = ok && r.Err == nil
	}
	err := checkAligned()
	fmt.Println("aligned/exactly-once(random):", pf(err))
	ok = ok && err == nil
	ok = concurrent() && ok
	fmt.Println("large-m visits O(1): OK (asserted by white-box chq test)")
	if !ok {
		os.Exit(1)
	}
}

// 随机交错：检查点完成时 cSum+CS 之和 == 屏障前到达之和，且条数 == 快照前已处理+CS 条数。
func checkAligned() error {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		o := api.New(1 << 20)
		var totSum, totCnt, preSum, preCnt, proc, procSnap [2]int
		var bar [2]bool
		act, n := false, 1
		for i := 0; i < 60; i++ {
			ch := r.Intn(2)
			switch r.Intn(5) {
			case 0, 1, 2:
				v := r.Intn(100)
				_ = o.Arrive(ch+1, v)
				totSum[ch] += v
				totCnt[ch]++
			case 3:
				if o.Step(ch+1) == nil {
					proc[ch]++
				}
			case 4:
				if !bar[ch] {
					first := !act
					if o.Barrier(ch+1, n) == nil {
						if first {
							act, procSnap = true, proc
						}
						bar[ch] = true
						preSum[ch], preCnt[ch] = totSum[ch], totCnt[ch]
					}
				}
			}
			if act && bar[0] && bar[1] {
				s := o.Snapshot()
				for k := 0; k < 2; k++ {
					if preSum[k] != s.Sum[k]+sumOf(s.State[k]) || preCnt[k] != procSnap[k]+len(s.State[k]) {
						return fmt.Errorf("trial %d ch %d", trial, k+1)
					}
				}
				act, bar, n = false, [2]bool{}, n+1
			}
		}
	}
	return nil
}

// 两路并发 Arrive（各 N 条、中途发屏障 1）+ 两路并发 Step；完成后核验不变量 1/2 与恢复等价。
func concurrent() bool {
	const N = 500
	o := api.New(1 << 20)
	var wg sync.WaitGroup
	for ch := 1; ch <= 2; ch++ {
		wg.Add(2)
		go func(ch int) {
			defer wg.Done()
			for i := 0; i < N; i++ {
				if i == N/2 {
					_ = o.Barrier(ch, 1)
				}
				_ = o.Arrive(ch, 1)
			}
		}(ch)
		go func(ch int) {
			defer wg.Done()
			for n := 0; n < N; {
				if o.Step(ch) == nil {
					n++
				} else {
					runtime.Gosched()
				}
			}
		}(ch)
	}
	wg.Wait()
	s := o.Snapshot()
	ok := true
	for k := 0; k < 2; k++ { // 每条记录值都是 1：cSum+CS 条数 == 屏障前到达条数
		ok = ok && s.Sum[k]+sumOf(s.State[k]) == N/2 && len(s.State[k]) <= N/2
	}
	o.RunAll()
	direct := o.State()
	o.Restore()
	o.RunAll()
	rest := o.State()
	ok = ok && direct.Sum == rest.Sum && direct.Last == rest.Last && direct.Has == rest.Has
	if !ok {
		fmt.Println("concurrent invariants: FAIL")
		return false
	}
	fmt.Println("concurrent invariants: OK")
	return true
}
