package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/wtm"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println(name + ": OK")
	} else {
		fmt.Println(name + ": FAIL")
		failed = true
	}
}

func main() {
	// wtm：迟到严格小于、空闲只看处理时间、推进只进不退
	check("wtm", wtm.Late(10, 3, 7) == false && wtm.Late(9, 3, 7) &&
		wtm.Idle(10, 1, 5, true) && !wtm.Idle(12, 11, 5, true) &&
		wtm.Idle(0, 0, 5, false) && wtm.Advance(17, 7) == 17 && wtm.Advance(17, 27) == 27)

	// 第三节八步：逐步水位与丢弃计数
	wm, err := api.New(3, 5)
	wantWM := []int64{7, 17, 17, 17, 17, 27, 27, 37}
	steps := []func() error{
		func() error { return wm.Feed("a", 10, 0) }, func() error { return wm.Feed("b", 20, 1) },
		func() error { return wm.Tick(10) }, func() error { return wm.Feed("c", 20, 11) },
		func() error { return wm.Tick(12) }, func() error { return wm.Tick(30) },
		func() error { return wm.Feed("d", 25, 31) }, func() error { return wm.Tick(40) },
	}
	ok := err == nil
	for i, step := range steps {
		if step() != nil || wm.Watermark() != wantWM[i] {
			ok = false
		}
	}
	check("8-step wm=37 dropped=1", ok && wm.Dropped() == 1)

	// 三类可判定错误，互不相同
	e1, e2, e3 := error(nil), error(nil), error(nil)
	_, e1 = api.New(0, 5)
	e2 = wm.Feed("", 1, 40)
	e3 = wm.Feed("k", -1, 40)
	check("sentinel errors", errors.Is(e1, api.ErrNonPositiveParam) &&
		errors.Is(e2, api.ErrEmptyKey) && errors.Is(e3, api.ErrNegativeTime) &&
		e1 != e2 && e2 != e3 && e1 != e3 && wm.Tick(-1) == api.ErrNegativeTime)

	// 被拒后状态不变
	bw, bd := wm.Watermark(), wm.Dropped()
	wm.Feed("", 1, 40)
	wm.Feed("k", -5, 40)
	wm.Tick(-2)
	check("rejected ops leave no trace", wm.Watermark() == bw && wm.Dropped() == bd)

	// 大 m 下迟到判定与历史长度无关：不同历史、相同水位，判定结果相同
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		a, _ := api.New(3, 5)
		for i := 0; i < m; i++ {
			a.Feed("k", int64(10+i), int64(i))
		}
		b, _ := api.New(3, 5)
		b.Feed("k", int64(10+m-1), 0)
		if a.Watermark() != b.Watermark() {
			ok = false
		}
		// 同一候选（必迟到）：两边判定一致，各丢 1 个
		cand := a.Watermark() + 3 - 1
		a.Feed("c", cand, int64(m))
		b.Feed("c", cand, 1)
		if a.Dropped() != 1 || b.Dropped() != 1 {
			ok = false
		}
	}
	check("late-check independent of m", ok)

	// 并发读一致 + 并发推进单调
	c, _ := api.New(3, 5)
	c.Feed("s", 100, 0)
	start := make(chan struct{})
	reads := make(chan int64, 64)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; reads <- c.Watermark() }()
	}
	close(start)
	wg.Wait()
	close(reads)
	same := true
	for r := range reads {
		if r != 97 {
			same = false
		}
	}
	done := make(chan struct{})
	vals := make(chan int64, 4096)
	go func() {
		for {
			select {
			case <-done:
				close(vals)
				return
			default:
				vals <- c.Watermark()
			}
		}
	}()
	for pt := int64(1); pt <= 2000; pt++ {
		c.Tick(pt)
	}
	close(done)
	mono, prev := true, int64(-1<<63)
	for v := range vals {
		if v < prev {
			mono = false
		}
		prev = v
	}
	check("concurrent reads consistent, monotonic", same && mono)

	// 内置自检：四条不变量
	check("SelfCheck", wm.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
