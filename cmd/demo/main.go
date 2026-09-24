package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/align"
	"ontology/api"
	"ontology/chanbuf"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func rec(ch int, k string, v int64) align.Item {
	return align.Item{Ch: ch, Kind: align.Record, Key: k, Val: v}
}

func bar(ch int, id int64) align.Item {
	return align.Item{Ch: ch, Kind: align.Barrier, ID: id}
}

func main() {
	// chanbuf: FIFO 缓冲与阻塞标志
	var c chanbuf.Chan
	c.Enqueue(chanbuf.Item{Key: "a", Val: 1})
	c.Enqueue(chanbuf.Item{Key: "b", Val: 2})
	first, _ := c.Dequeue()
	second, _ := c.Dequeue()
	_, empty := c.Dequeue()
	c.Block()
	check("chanbuf FIFO 保序与阻塞", first.Key == "a" && second.Key == "b" && !empty && c.Blocked() && c.Len() == 0)

	// align: 第三节十步序列，逐步核对输出流片段
	steps := []align.Item{
		rec(0, "x", 1), rec(1, "y", 2), bar(0, 1), rec(0, "x", 10), rec(1, "x", 3),
		rec(0, "y", 20), rec(1, "y", 4), bar(1, 1), rec(1, "x", 5), rec(0, "x", 100),
	}
	b1 := align.Out{Ch: -1, Kind: align.Barrier, ID: 1}
	want := [][]align.Out{
		{rec(0, "x", 1)}, {rec(1, "y", 2)}, {}, {}, {rec(1, "x", 3)},
		{}, {rec(1, "y", 4)}, {b1, rec(0, "x", 10), rec(0, "y", 20)}, {rec(1, "x", 5)}, {rec(0, "x", 100)},
	}
	a := align.New(16)
	var frag3, frag8 []align.Out
	snapAt3 := true
	okAll := true
	for i, it := range steps {
		out, err := a.Push([]align.Item{it})
		okAll = okAll && err == nil && slices.Equal(out, want[i])
		if i == 2 {
			frag3 = out
			_, snapAt3 = a.Snapshot(1)
		}
		if i == 7 {
			frag8 = out
		}
	}
	check("十步输出流片段逐步一致", okAll)
	check("第3步判定: c0 阻塞、无输出、快照未取", len(frag3) == 0 && !snapAt3)
	check("第8步判定: 对齐取快照+转发B1+按序重放", slices.Equal(frag8, want[7]))
	s1, ok1 := a.Snapshot(1)
	check("快照1 内容 {x:4 y:6}", ok1 && maps.Equal(s1, map[string]int64{"x": 4, "y": 6}))
	check("终态 {x:119 y:26}", maps.Equal(a.State(), map[string]int64{"x": 119, "y": 26}))
	check("align.SelfCheck(随机交错/不丢不重/大m检查数)", align.SelfCheck() == nil)

	// api: 三类可判定错误互不相同
	op := api.New(1)
	_, e1 := op.Push([]api.Item{{Ch: 3, Kind: api.Record, Key: "x"}})
	_, e2 := op.Push([]api.Item{bar(0, 5)})
	if _, err := op.Push([]api.Item{bar(0, 1)}); err != nil {
		check("api 建阻塞", false)
	}
	_, e3 := op.Push([]api.Item{rec(0, "x", 1), rec(0, "y", 2)}) // 第二条超限
	check("三类可判定错误互不相同", errors.Is(e1, api.ErrBadElem) &&
		errors.Is(e2, api.ErrBarrierOrder) && errors.Is(e3, api.ErrBufferFull) &&
		!errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3))

	// api: 被拒后状态不变且可继续使用
	st := op.State()
	_, errBad := op.Push([]api.Item{rec(0, "x", 1), rec(0, "y", 2)})
	out, errOK := op.Push([]api.Item{rec(1, "z", 5)})
	check("被拒后状态不变且可继续用", errors.Is(errBad, api.ErrBufferFull) &&
		maps.Equal(st, map[string]int64{}) && errOK == nil && len(out) == 1 &&
		maps.Equal(op.State(), map[string]int64{"z": 5}))

	// api: 并发只读同一个已完成对齐的实例，结果逐 Key 相同
	op2 := api.New(64)
	if _, err := op2.Push([]api.Item{rec(0, "x", 1), rec(1, "y", 2), bar(0, 1), bar(1, 1),
		rec(0, "x", 3), rec(1, "y", 4), bar(0, 2), bar(1, 2)}); err != nil {
		check("api 构造对齐", false)
	}
	wantState := op2.State()
	wantSnap1, _ := op2.Snapshot(1)
	var bad atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s1, _ := op2.Snapshot(1)
				if !maps.Equal(op2.State(), wantState) || !maps.Equal(s1, wantSnap1) {
					bad.Add(1)
				}
			}
			if op2.SelfCheck() != nil {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	check("并发只读结果一致", bad.Load() == 0)

	if failed {
		os.Exit(1)
	}
}
