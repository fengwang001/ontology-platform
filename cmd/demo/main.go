// Command demo 逐条打印心跳水位推进各判定的 OK/FAIL，退出码 0 表示全部通过。
// 不读参数、不联网、状态只在进程内存。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	hb "ontology/api"
	"ontology/wm"
)

var failed bool

func check(ok bool, format string, args ...any) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], fmt.Sprintf(format, args...))
}

type step struct {
	isHB bool
	key  string
	ts   int64
}

var eightSteps = []step{
	{false, "a", 5}, {true, "", 12}, {false, "b", 8}, {true, "", 9},
	{true, "", 20}, {false, "c", 14}, {true, "", 17}, {true, "", 25},
}

func feed(e *hb.Engine, s step) error {
	if s.isHB {
		return e.Heartbeat(s.ts)
	}
	return e.Feed(s.key, s.ts)
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	// 判定 1（wm 包）：初始负无穷；八步逐步水位与迟到计数。
	e, _ := hb.New(3)
	wantWM := []int64{2, 12, 12, 12, 20, 20, 20, 25}
	wantLate := []int64{0, 0, 0, 1, 1, 1, 2, 2}
	gotWM, gotLate := make([]int64, 8), make([]int64, 8)
	initNegInf := e.Watermark() == wm.NegInfinity
	for i, s := range eightSteps {
		if err := feed(e, s); err != nil {
			check(false, "step %d error: %v", i+1, err)
			os.Exit(1)
		}
		gotWM[i], gotLate[i] = e.Watermark(), e.LateHeartbeats()
	}
	check(initNegInf && eq(gotWM, wantWM) && eq(gotLate, wantLate), "eight steps wm=%v late=%v", gotWM, gotLate)

	// 判定 2（hbs 包）：最终水位与迟到心跳总数。
	check(e.Watermark() == 25 && e.LateHeartbeats() == 2,
		"final watermark=%d lateHeartbeats=%d", e.Watermark(), e.LateHeartbeats())

	// 判定 3（api 包）：三类哨兵错误可判定且互不相同。
	bad, errDelay := hb.New(0)
	errEmpty, errNeg, errHBNeg := e.Feed("", 5), e.Feed("k", -1), e.Heartbeat(-1)
	distinct := !errors.Is(hb.ErrEmptyKey, hb.ErrNegativeTS) && !errors.Is(hb.ErrInvalidDelay, hb.ErrEmptyKey)
	check(bad == nil && errors.Is(errDelay, hb.ErrInvalidDelay) && errors.Is(errEmpty, hb.ErrEmptyKey) &&
		errors.Is(errNeg, hb.ErrNegativeTS) && errors.Is(errHBNeg, hb.ErrNegativeTS) && distinct,
		"sentinel errors: invalid-delay / empty-key / negative-ts are distinct")

	// 判定 4：失败不留痕——水位、迟到计数不变，之后仍可正常推进。
	check(e.Watermark() == 25 && e.LateHeartbeats() == 2 && e.Heartbeat(30) == nil && e.Watermark() == 30,
		"rejected events left no trace; still advances to %d", e.Watermark())

	// 判定 5：大 m（100/1000/10000）下迟到判定 O(1)——经公开 SelfCheck 取布尔结论，不读内部计数器。
	fresh, _ := hb.New(3)
	check(fresh.SelfCheck() == nil, "SelfCheck incl. O(1) late check at m=100/1000/10000")

	// 判定 6：并发只读结果一致；并发推进水位单调不减（无 sleep）。
	check(concurrentOK(), "concurrent readers identical; concurrent advances monotonic")

	if failed {
		os.Exit(1)
	}
}

// concurrentOK 验证并发只读完全一致、并发推进单调不减（无 sleep）。
func concurrentOK() bool {
	e, _ := hb.New(1)
	for _, ts := range []int64{5, 12, 20} {
		_ = e.Feed("k", ts)
	}
	var wg sync.WaitGroup
	start, reads := make(chan struct{}), make([]int64, 32)
	for i := range reads {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; reads[i] = e.Watermark() }(i)
	}
	close(start)
	wg.Wait()
	for _, v := range reads {
		if v != reads[0] {
			return false
		}
	}
	var bad atomic.Bool
	goStart := make(chan struct{})
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-goStart
			prev := wm.NegInfinity
			for i := 0; i < 200; i++ {
				ts := int64(g*200 + i)
				if i%2 == 0 {
					_ = e.Feed("k", ts)
				} else {
					_ = e.Heartbeat(ts)
				}
				w := e.Watermark()
				if w < prev {
					bad.Store(true)
				}
				prev = w
			}
		}(g)
	}
	close(goStart)
	wg.Wait()
	return !bad.Load()
}
