package sla

import (
	"fmt"
	"sync"
	"testing"
)

// 并发冒烟：主 goroutine 充当唯一调度器，每轮先放行所有 worker 在同一时间戳
// Pause，再统一在更大时间戳 Resume；节假日由主 goroutine 在 pause 时刻发布
// （严格未来日号）。任何 worker 都不应收到 ErrClock/ErrState，
// 在 -race 下验证共享日历与管理器的加锁正确性。
func TestConcurrentInterleave(t *testing.T) {
	c := newConCal()
	m := newConMgr(c)
	const G = 8
	for g := 0; g < G; g++ {
		if err := startCon(m, g, 600); err != nil {
			t.Fatalf("start g%d: %v", g, err)
		}
	}

	const rounds = 20
	for k := 0; k < rounds; k++ {
		pauseAt := 600 + int64(k)*1440
		var arrived, proceed, done sync.WaitGroup
		arrived.Add(G)
		proceed.Add(G)
		done.Add(G)
		for g := 0; g < G; g++ {
			go func(g int) {
				arrived.Done()
				proceed.Wait()
				if err := pauseCon(m, g, pauseAt); err != nil {
					t.Errorf("g%d pause k%d: %v", g, k, err)
				}
				done.Done()
			}(g)
		}
		arrived.Wait()
		// 所有 worker 已就位，主 goroutine 先发布节假日（严格未来），再放行。
		// 选远期且各轮递增的日号：now=pauseAt，严格小于 day 起点，且 day 单调递增。
		day := int64(10000 + k)
		if err := addConHol(c, day, pauseAt); err != nil {
			t.Errorf("holiday day%d k%d: %v", day, k, err)
		}
		proceed.Add(-G)
		done.Wait() // 所有 pause 已返回后才进入 resume 阶段

		resumeAt := pauseAt + 100
		var arr2, go2, done2 sync.WaitGroup
		arr2.Add(G)
		go2.Add(G)
		done2.Add(G)
		for g := 0; g < G; g++ {
			go func(g int) {
				arr2.Done()
				go2.Wait()
				if err := resumeCon(m, g, resumeAt); err != nil {
					t.Errorf("g%d resume k%d: %v", g, k, err)
				}
				done2.Done()
			}(g)
		}
		arr2.Wait()
		go2.Add(-G)
		done2.Wait() // 所有 resume 返回后才允许进入下一轮
	}

	final := int64(600 + rounds*1440)
	for g := 0; g < G; g++ {
		el, err := elapsedCon(m, g, final)
		if err != nil || el < 0 || el > 100000 {
			t.Fatalf("g%d elapsed=%d err=%v", g, el, err)
		}
		t.Logf("timer %c elapsed=%d after concurrent interleaving", 'a'+g, el)
	}
	fmt.Println("concurrent interleave completed")
}
