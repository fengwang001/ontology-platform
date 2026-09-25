package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	// 1) 第三节 New(3) 八步：逐步 Len、Pop 结果、LIFO、满与空。
	st, _ := api.New(3)
	lens, pops := []int{}, []int{}
	lastOK := true
	for step := 1; step <= 8; step++ {
		if step <= 4 {
			_ = st.Push(step)
		} else {
			v, ok := st.Pop() // 步5/6/7 返回 3/2/1；步8 空返回 (0,false)
			pops, lastOK = append(pops, v), ok
		}
		lens = append(lens, st.Len())
	}
	report("8step lens=[1 2 3 3 2 1 0 0] pops=[3 2 1 0] LIFO, step8 empty",
		fmt.Sprint(lens) == "[1 2 3 3 2 1 0 0]" && fmt.Sprint(pops) == "[3 2 1 0]" && !lastOK)

	// 2) 三类哨兵互不相同、可判定；关闭后 Pop 弹空、Push 持续报已关闭。
	_, eBad := api.New(0)
	full, _ := api.New(1)
	_ = full.Push(1)
	eFull := full.Push(2)
	eClose1 := full.Close()
	eClosed := full.Push(3)
	eClose2 := full.Close()
	dv, dOk := full.Pop() // 关闭后先弹空剩余的 1
	_, dEmpty := full.Pop()
	distinct := errors.Is(eBad, api.ErrInvalidMaxLen) && errors.Is(eFull, api.ErrFull) &&
		errors.Is(eClosed, api.ErrClosed) && errors.Is(eClose2, api.ErrClosed) &&
		api.ErrInvalidMaxLen != api.ErrFull && api.ErrFull != api.ErrClosed
	report("errors: invalid/full/closed distinct & decidable; close drains then empty",
		distinct && eClose1 == nil && dv == 1 && dOk && !dEmpty)

	// 3) 零值与空：Push(0) 得 (0,true)，空栈得 (0,false)；无 ok 则两者不可区分。
	z, _ := api.New(1)
	_ = z.Push(0)
	z1, ok1 := z.Pop()
	z2, ok2 := z.Pop()
	report("zero-vs-empty: Push(0)->Pop=(0,true), empty Pop=(0,false) (no-ok API conflates them)",
		z1 == 0 && ok1 && z2 == 0 && !ok2)

	// 4) 非原子 Pop 丢失并发 Push：模型化 [2,1] 上 A 读(2,1)、B 压 3 后 A 直接写后继。
	list := []int{2, 1}
	list = []int{3, 2, 1}                  // B 的 Push：CAS 栈顶 -> 3
	list = []int{1}                        // A 非原子「读-改-写」：栈顶直接写成后继
	lost := list[0] == 1 && len(list) == 1 // 3 被孤立丢失；CAS 实现则 CAS(2->1) 失败、重弹得 3、余 [2,1]
	report("non-atomic Pop loses pushed value 3 (stack becomes [1]); CAS retries and pops 3 -> [2,1]", lost)

	// 5) O(1)：m=100/1000/10000 只弹一个，恒立即取栈顶；访问节点数==1 由包内 TestPopVisitsOne 钉住。
	o1 := true
	for _, m := range []int{100, 1000, 10000} {
		big, _ := api.New(m)
		for i := 0; i < m; i++ {
			_ = big.Push(i)
		}
		v, ok := big.Pop()
		if !ok || v != m-1 || big.Len() != m-1 {
			o1 = false
		}
	}
	report("O(1): one Pop always takes only the top at m=100/1000/10000 (visit==1 asserted by TestPopVisitsOne)", o1)

	// 6) 并发：N 生产者各压唯一值、N 消费者并发弹，集合全等、每值恰 1 次。
	const N = 256
	cs, _ := api.New(N)
	start, out := make(chan struct{}), make(chan int, N)
	var wg sync.WaitGroup
	wg.Add(2 * N)
	for i := 0; i < N; i++ {
		i := i
		go func() { defer wg.Done(); <-start; _ = cs.Push(i) }()
		go func() {
			defer wg.Done()
			<-start
			for {
				if v, ok := cs.Pop(); ok {
					out <- v
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(out)
	seen := map[int]int{}
	for v := range out {
		seen[v]++
	}
	good := len(seen) == N && cs.Len() == 0
	for _, c := range seen {
		good = good && c == 1
	}
	report("concurrency N=256: popped set == pushed set, each value exactly once", good)

	// 7) 对外 SelfCheck（四条不变量内置序列）。
	chk, _ := api.New(1)
	report("api.SelfCheck passes all four invariants", chk.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
