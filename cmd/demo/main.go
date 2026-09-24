package main

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"ontology/api"
	"ontology/merge"
	"ontology/pwm"
)

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	// 1) pwm：空闲边界恰好相等时已空闲，比 idle 小 1 时仍非空闲。
	pp := pwm.New(0)
	pp.Report(5, 6)
	ok("pwm idle boundary ==idle", !pp.Idle(15, 10) && pp.Idle(16, 10))

	// 2) 第三节九步的合并水位，及第 2、5、8 步空闲集合判定。
	g, _ := api.New(3, 10, 0)
	ps := []int{0, 1, 0, 1, 2, 2, -1, -1, 1}
	ws := []int64{12, 15, 40, 18, 16, 21, 0, 0, 30}
	ns := []int64{6, 10, 12, 20, 22, 26, 30, 36, 37}
	want := []int64{math.MinInt64, 12, 15, 18, 18, 18, 21, 21, 30}
	snap := map[int][3]bool{1: {false, false, true}, 4: {true, false, false}, 7: {true, true, true}}
	nine := true
	for i := range want {
		var v int64
		if ps[i] < 0 {
			v, _ = g.Tick(ns[i])
		} else {
			v, _ = g.Report(ps[i], ws[i], ns[i])
		}
		w, snapOK := snap[i]
		if v != want[i] || (snapOK && w != [3]bool{g.Idle(0), g.Idle(1), g.Idle(2)}) {
			nine = false
		}
	}
	ok("nine steps merged + step2/5/8 idle sets", nine)

	// 3) 随机序列对拍朴素参照 + 边界 + 失败不留痕（内置 SelfCheck）。
	sc, _ := api.New(1, 1, 0)
	ok("selfcheck: random vs naive, boundary, no-trace", sc.SelfCheck() == nil)

	// 4) 四类可判定错误，身份互不相同。
	bad, e0 := api.New(-1, 1, 0)
	g2, _ := api.New(3, 10, 0)
	g2.Report(0, 5, 0)
	_, e1 := g2.Report(9, 1, 0)
	_, e2 := g2.Tick(-1)
	_, e3 := g2.Report(0, 4, 0)
	errs := []error{e0, e1, e2, e3}
	sents := []error{api.ErrInvalidArgument, api.ErrPartitionRange, api.ErrClockRewind, api.ErrWatermarkRewind}
	distinct := bad == nil
	for i := range errs {
		for j := range sents {
			if errors.Is(errs[i], sents[j]) != (i == j) {
				distinct = false
			}
		}
	}
	ok("four sentinel errors distinguishable", distinct)

	// 5) 被拒后状态不变（含最后活跃时间：last 仍为 0，则 9 活跃、10 空闲）。
	mBefore := g2.Merged()
	g2.Report(0, 4, 0) // 再拒一次
	_, eb := g2.Tick(9)
	at9 := g2.Idle(0)
	g2.Tick(10)
	noTrace := eb == nil && !at9 && g2.Idle(0) && g2.Merged() == mBefore
	ok("rejected op leaves no trace (incl. last)", noTrace)

	// 6) 大 m 下读取个数不随 m 增长（非导出计数，只取成败结论）。
	ok("large-m read count bounded", merge.CheckReadBound() == nil)

	// 7) 一个写者递增时钟，N 个读者并发读 Merged：各自序列必须单调不减。
	gw, _ := api.New(4, 1000, 0)
	done := make(chan struct{})
	var wg sync.WaitGroup
	var failMu sync.Mutex
	failed := false
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev := int64(math.MinInt64)
			for {
				select {
				case <-done:
					return
				default:
					if v := gw.Merged(); v < prev {
						failMu.Lock()
						failed = true
						failMu.Unlock()
					} else {
						prev = v
					}
				}
			}
		}()
	}
	now := int64(0)
	for i := range 400 {
		now += int64(i % 7)
		if i%2 == 0 {
			gw.Report(i%4, int64(i), now)
		} else {
			gw.Tick(now)
		}
	}
	close(done)
	wg.Wait()
	ok("concurrent readers see monotonic merged watermark", !failed)
}
