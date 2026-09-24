package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/seq"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println(name + ": OK")
	} else {
		fmt.Println(name + ": FAIL")
	}
}

func kindLabel(k seq.Kind) string {
	switch k {
	case seq.Equal:
		return "相等"
	case seq.Late:
		return "乱序"
	default:
		return "有序"
	}
}

type snap struct {
	mx, ooo, late int64
	exceeds       bool
}

func main() {
	// 第三节六步事件（maxSlack=10）：10,10,5,12,11,13，每步一行。
	o, _ := api.New(10)
	var prev int64
	seen := false
	for _, v := range []int64{10, 10, 5, 12, 11, 13} {
		if err := o.Feed(v); err != nil {
			panic(err)
		}
		r, _ := seq.Classify(v, prev, seen)
		prev, seen = o.MaxSeen(), true
		fmt.Printf("Seq=%2d: MaxSeen=%2d %s OOO=%d MaxLate=%d\n",
			v, prev, kindLabel(r.Kind), o.OutOfOrder(), o.MaxLateness())
	}
	// 相等不算乱序；迟到量取历史最大(5 非最近1)；纯观测不丢弃(OOO=2)。
	report("equal/hist-max/no-drop",
		o.OutOfOrder() == 2 && o.MaxLateness() == 5 && o.MaxSeen() == 13)

	// 三类哨兵互不相同；被拒后全状态不变、仍可用（冻结粘性）。
	b, _ := api.New(10)
	_ = b.Feed(7)
	s0 := snap{b.MaxSeen(), b.OutOfOrder(), b.MaxLateness(), b.ExceedsSlack()}
	e1, e2 := b.Feed(0), b.Freeze()
	e3, e4 := b.Feed(9), b.Freeze()
	_, e5 := api.New(-1)
	s1 := snap{b.MaxSeen(), b.OutOfOrder(), b.MaxLateness(), b.ExceedsSlack()}
	distinct := errors.Is(e1, api.ErrInvalidSeq) && errors.Is(e2, nil) &&
		errors.Is(e3, api.ErrFrozen) && errors.Is(e4, api.ErrFrozen) &&
		errors.Is(e5, api.ErrNegativeSlack) && e1 != e3 && e3 != e5
	report("3-sentinels-distinct/reject-no-trace", distinct && s0 == s1)

	// 大 m 下检查个数有界：计数器非导出、外部不可读，由 SelfCheck 内部钉住。
	report("big-m O(1) check-count (via SelfCheck)", o.SelfCheck() == nil)

	// N 个 goroutine 并发只读同一已喂满实例，四项统计逐项相同；无 sleep。
	const n = 16
	got := make(chan snap, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got <- snap{o.MaxSeen(), o.OutOfOrder(), o.MaxLateness(), o.ExceedsSlack()}
		}()
	}
	wg.Wait()
	close(got)
	want := snap{13, 2, 5, false}
	agree := true
	for s := range got {
		if s != want {
			agree = false
		}
	}
	report("concurrent-readers-agree", agree)
}
