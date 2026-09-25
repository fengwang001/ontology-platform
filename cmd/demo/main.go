// demo 逐项演示无锁 MPMC 队列的规格与不变量，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var fails int

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK   " + msg)
	} else {
		fmt.Println("FAIL " + msg)
		fails++
	}
}

func main() {
	// 1. 第三节八步：New(3)，每步之后核验 Len 与出队结果。
	qu, _ := api.New(3)
	enqs := []int{1, 2, 3, 4}
	good := true
	for i, v := range enqs {
		err := qu.Enqueue(v)
		wantLen := []int{1, 2, 3, 3}[i]
		if i < 3 {
			good = good && err == nil
		} else {
			good = good && errors.Is(err, api.ErrFull) // 第 4 步满
		}
		good = good && qu.Len() == wantLen
	}
	for i, want := range []int{1, 2, 3} {
		v, has := qu.Dequeue()
		good = good && has && v == want && qu.Len() == 2-i
	}
	v, has := qu.Dequeue()
	good = good && !has && v == 0 && qu.Len() == 0 // 第 8 步空
	ok(good, "eight-step trace matches NOTES.md table (Len + FIFO + full/empty)")

	// 2. 满与空是可判定错误/信号，且三者互不相同。
	qu2, _ := api.New(1)
	_, empty := qu2.Dequeue()
	_ = qu2.Enqueue(7)
	fullErr := qu2.Enqueue(8)
	ok(!empty && errors.Is(fullErr, api.ErrFull) && !errors.Is(fullErr, api.ErrClosed),
		"empty=(0,false), full=ErrFull, sentinels distinct")

	// 3. 单元素可正常取出；无哨兵且以 head==tail 判空会把它判丢。
	qu3, _ := api.New(1)
	_ = qu3.Enqueue(1)
	v, has = qu3.Dequeue()
	ok(has && v == 1, "single element dequeues (1,true); head==tail emptiness would lose it")

	// 4. ok 标志区分「入队 0 后出队」与「空队列出队」。
	qu4, _ := api.New(2)
	_ = qu4.Enqueue(0)
	v0, ok0 := qu4.Dequeue()
	vE, okE := qu4.Dequeue()
	ok(v0 == 0 && ok0 && vE == 0 && !okE, "ok flag distinguishes dequeued 0 from empty")

	// 5. Close 排空不丢元素，之后 Enqueue 恒报 ErrClosed。
	qu5, _ := api.New(2)
	_ = qu5.Enqueue(1)
	_ = qu5.Enqueue(2)
	_ = qu5.Close()
	a, ha := qu5.Dequeue()
	b, hb := qu5.Dequeue()
	_, hc := qu5.Dequeue()
	ok(ha && a == 1 && hb && b == 2 && !hc && errors.Is(qu5.Enqueue(9), api.ErrClosed),
		"Close drains (1,2) without loss, then stays empty; Enqueue=ErrClosed")

	// 6. 参数非法可判定；大 m 下 Dequeue 访问节点数恒为 1 由测试钉住
	//    （计数器是非导出字段，公开接口读不到，演示不得经导出路径读它）。
	_, badErr := api.New(0)
	ok(errors.Is(badErr, api.ErrBadMaxLen),
		"New(0)=ErrBadMaxLen; O(1) dequeue pinned by TestDequeueVisitsOneNode")

	// 7. 并发：N 生产者各入队唯一值，N 消费者并发出队，集合恰好相等。
	const n = 512
	quc, _ := api.New(n)
	ch := make(chan int, n)
	var got atomic.Int64
	var wg sync.WaitGroup
	for p := 0; p < n; p++ {
		wg.Add(1)
		go func(v int) { defer wg.Done(); _ = quc.Enqueue(v) }(p)
	}
	for c := 0; c < n; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for got.Load() < n {
				if val, has := quc.Dequeue(); has {
					got.Add(1)
					ch <- val
				}
			}
		}()
	}
	wg.Wait()
	close(ch)
	seen := make(map[int]int)
	for val := range ch {
		seen[val]++
	}
	uniq := len(seen) == n
	for val, cnt := range seen {
		uniq = uniq && cnt == 1 && val >= 0 && val < n
	}
	ok(uniq, "concurrent MPMC: 512 unique values, no loss/dup/fabrication")

	// 8. 内置自检：四条不变量。
	ok(api.SelfCheck() == nil, "api.SelfCheck passes all four invariants")

	if fails > 0 {
		os.Exit(1)
	}
}
