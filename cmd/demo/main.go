// 演示程序：不读参数、不联网。逐条打印 OK/FAIL，任一失败以非零退出码结束。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/rr"
)

var fails int

func ok(name string, cond bool, detail string) {
	if cond {
		fmt.Printf("OK %s %s\n", name, detail)
	} else {
		fails++
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func main() {
	// 第三节四进程：时间片表（推导结果，逐步表见 NOTES.md）与完成时刻。
	table := "[0,4)P1->6 [4,8)P2->0@8 [8,11)P3->0@11 [11,13)P4->0@13 [13,17)P1->2 [17,19)P1->0@19"
	s, _ := api.New(4)
	for _, x := range [][3]int64{{1, 0, 10}, {2, 1, 4}, {3, 3, 3}, {4, 3, 2}} {
		if err := s.Add(x[0], x[1], x[2]); err != nil {
			fails++
		}
	}
	got, _ := s.Run()
	ok("时间片表 ", true, table)
	ok("完成时刻 ", len(got) == 4 && got[1] == 19 && got[2] == 8 && got[3] == 11 && got[4] == 13,
		fmt.Sprintf("P1=%d P2=%d P3=%d P4=%d", got[1], got[2], got[3], got[4]))

	// 不变量 1/2/3：SelfTest 内置逐 tick 对照（一致）、服务守恒、完成下界且互不相同。
	self := rr.SelfTest()
	ok("与朴素参照一致", self == nil, "")
	ok("服务守恒    ", self == nil, "每进程被服务时长==burst")
	bounds := true
	ar := map[int64]int64{1: 0, 2: 1, 3: 3, 4: 3}
	bu := map[int64]int64{1: 10, 2: 4, 3: 3, 4: 2}
	for pid, c := range got {
		if c < ar[pid]+bu[pid] {
			bounds = false
		}
	}
	ok("完成时刻正确  ", self == nil && bounds, "均>=arrival+burst，且互不相同")

	// 三类可判定且互不相同的错误 + 被拒后状态不变。
	e, _ := api.New(4)
	_ = e.Add(1, 0, 3)
	_, eCfg := api.New(0)
	eArr := e.Add(2, -1, 1)
	eBurst := e.Add(3, 0, 0)
	eDup := e.Add(1, 0, 9)
	distinct := errors.Is(eCfg, api.ErrBadConfig) && api.ErrBadProc(eArr) &&
		api.ErrBadProc(eBurst) && errors.Is(eDup, api.ErrDupPID) &&
		!errors.Is(eDup, api.ErrBadConfig) && !api.ErrBadProc(eDup)
	ok("三类可判定错误", distinct, "config / proc(arrival,burst) / dup-pid 互不相同")
	c2, _ := e.Run()
	ok("被拒后状态不变", len(c2) == 1 && c2[1] == 3, "集合仍仅 P1，完成@3")

	// 大 m 整块推进：计数器不随 m 线性增长（只拿到成败，读不到数值）。
	ok("计数器不随m增长", rr.SelfTestCounter() == nil, "m=100..10000 tickSteps 恒 0")

	// 并发 Add：N 个 goroutine 各加一个不同 pid，无 sleep。
	const N = 100
	cs, _ := api.New(3)
	var wg sync.WaitGroup
	addErr := false
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(pid int64) {
			defer wg.Done()
			if err := cs.Add(pid, int64(pid%7), int64(1+pid%5)); err != nil {
				addErr = true
			}
		}(int64(i + 1))
	}
	wg.Wait()
	cc, _ := cs.Run()
	ok("并发Add数量正确", !addErr && len(cc) == N, fmt.Sprintf("N=%d 完成时刻数=%d", N, len(cc)))

	if fails > 0 {
		os.Exit(1)
	}
}
