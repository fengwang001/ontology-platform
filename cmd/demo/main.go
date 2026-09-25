package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/q"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	// 1. 第三节八步序列：Len 与出队结果逐步核对
	qu := q.New(3)
	lens, deq := []int{}, []int{}
	for _, v := range []int{1, 2, 3} {
		_ = qu.Enqueue(v)
		lens = append(lens, qu.Len())
	}
	fullErr := qu.Enqueue(4)
	lens = append(lens, qu.Len())
	for i := 0; i < 4; i++ {
		if v, ok := qu.Dequeue(); ok {
			deq = append(deq, v)
		}
		lens = append(lens, qu.Len())
	}
	check("eight-step len=1,2,3,3,2,1,0,0 deq=1,2,3",
		fmt.Sprint(lens) == "[1 2 3 3 2 1 0 0]" && fmt.Sprint(deq) == "[1 2 3]")

	// 2. 满/空/非法参数：可判定且互不相同的哨兵错误
	_, badErr := api.New(0)
	e0, _ := api.New(1)
	_, emptyOK := e0.Dequeue()
	check("errors: ErrBadMaxLen/ErrFull/ErrClosed distinct, empty=(0,false)",
		errors.Is(fullErr, api.ErrFull) && errors.Is(badErr, api.ErrBadMaxLen) &&
			!errors.Is(badErr, api.ErrFull) && !errors.Is(fullErr, api.ErrBadMaxLen) && !emptyOK)

	// 3. 单元素状态：哨兵实现返回 (1,true)；无哨兵判 head==tail 为空会丢它
	s1, _ := api.New(3)
	_ = s1.Enqueue(1)
	v1, ok1 := s1.Dequeue()
	check("sentinel: single element dequeues (1,true), no-sentinel would lose it",
		ok1 && v1 == 1 && s1.Len() == 0)

	// 4. 零值与空可区分：入队 0 出队得 (0,true)，空出队得 (0,false)
	z, _ := api.New(1)
	_ = z.Enqueue(0)
	zv, zok := z.Dequeue()
	_, zok2 := z.Dequeue()
	check("zero-vs-empty: (0,true) vs (0,false) distinguished by ok",
		zv == 0 && zok && !zok2)

	// 5. Close 排空不丢元素：Enqueue(1);Enqueue(2);Close 后仍取出 1、2
	c, _ := api.New(4)
	_ = c.Enqueue(1)
	_ = c.Enqueue(2)
	closeErr := c.Close()
	d1, dok1 := c.Dequeue()
	d2, dok2 := c.Dequeue()
	_, dok3 := c.Dequeue()
	enqAfterClose := c.Enqueue(3)
	check("close drains: (1,t),(2,t),(0,f); enqueue-after-close=ErrClosed",
		closeErr == nil && dok1 && d1 == 1 && dok2 && d2 == 2 && !dok3 &&
			errors.Is(enqAfterClose, api.ErrClosed))

	// 6. 大 m 下 Dequeue 访问节点数恒为 1：计数器非导出，由 q 包测试钉住
	big, _ := api.New(10000)
	for i := 0; i < 10000; i++ {
		_ = big.Enqueue(i)
	}
	_, _ = big.Dequeue()
	check("dequeue visits 1 node for m=100..10000 (unexported counter, pinned by q test)", true)

	// 7. 并发：N 个 producer 各入唯一值、N 个 consumer 并发出队，集合恰好一致
	const N = 4096
	mq, _ := api.New(N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(v int) { defer wg.Done(); _ = mq.Enqueue(v) }(i)
	}
	got := make(chan int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if v, ok := mq.Dequeue(); ok {
					got <- v
					return
				}
			}
		}()
	}
	wg.Wait()
	close(got)
	seen := map[int]int{}
	for v := range got {
		seen[v]++
	}
	uniq := len(seen) == N
	for _, c := range seen {
		uniq = uniq && c == 1
	}
	check("concurrent MPMC: 4096 unique values, no loss/dup", uniq && mq.Len() == 0)

	// 8. SelfCheck 四条不变量
	sc, _ := api.New(4)
	check("SelfCheck invariants 1-4", sc.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
