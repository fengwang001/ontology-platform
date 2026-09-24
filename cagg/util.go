package cagg

import (
	"container/heap"
	"errors"
	"fmt"
	"slices"

	"ontology/cwin"
)

// entry 是堆中一个大窗口的定位键：end 为该窗下一个待触发子窗口终点。
// 终点相同按 Key、再按起点排序，弹出序即输出序。
type entry struct {
	end int64
	key string
	S   int64
}
type winID struct {
	key string
	S   int64
}
type win struct {
	bins  []int // bins[i] 计数 [S+i*step,S+(i+1)*step)
	acc   int   // 已触发子窗口的累计
	fired int   // 已触发子窗口数
}

// hp 是按下一待触发终点排序的最小堆（标准库 container/heap）。
type hp []entry

func (h hp) Len() int { return len(h) }
func (h hp) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.end != b.end {
		return a.end < b.end
	}
	if a.key != b.key {
		return a.key < b.key
	}
	return a.S < b.S
}
func (h hp) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *hp) Push(x any)   { *h = append(*h, x.(entry)) }
func (h *hp) Pop() any {
	o := *h
	n := len(o)
	e := o[n-1]
	*h = o[:n-1]
	return e
}

// state 是 Feed 开始时的整状态快照，失败时整体恢复（不变量4）。
type state struct {
	wm      int64
	dropped int
	scan    int
	outs    []Out
	wins    map[winID]*win
}

func (a *Agg) snapshot() state {
	ws := make(map[winID]*win, len(a.wins))
	for id, w := range a.wins {
		ws[id] = &win{bins: append([]int(nil), w.bins...), acc: w.acc, fired: w.fired}
	}
	return state{a.wm, a.dropped, a.scan, append([]Out(nil), a.outs...), ws}
}

func (s state) restore(a *Agg) {
	a.wins = make(map[winID]*win, len(s.wins))
	a.hp = hp{}
	for id, w0 := range s.wins { // 堆内容由存活窗口重建，堆序由 Init 恢复
		w := &win{bins: append([]int(nil), w0.bins...), acc: w0.acc, fired: w0.fired}
		a.wins[id] = w
		a.hp = append(a.hp, entry{a.sp.End(id.S, w.fired+1), id.key, id.S})
	}
	heap.Init(&a.hp)
	a.wm, a.dropped, a.scan, a.outs = s.wm, s.dropped, s.scan, s.outs
}

func o(S, E int64, c int) Out { return Out{Key: "K", Start: S, End: E, Count: c} }

// golden 是第三节九行表中八步与 Flush 的预期输出（Key=K）。
var golden = [][]Out{{o(-12, -8, 0)}, {o(-12, -4, 1)}, nil, {o(-12, 0, 2), o(0, 4, 1)},
	nil, {o(0, 8, 2)}, nil, {o(0, 12, 3), o(12, 16, 1)}, {o(12, 20, 2), o(12, 24, 2)}}

// SelfCheck 用内置事件序列核验四条不变量（逐步输出/丢弃/单调/失败不留痕）。
func (a *Agg) SelfCheck() error {
	sp, _ := cwin.New(12, 4, 2)
	y := New(sp, 2)
	for i, ts := range []int64{-5, 1, -2, 6, 3, 11, 12, 19} {
		got, e := y.Feed([]Event{{Key: "K", TS: ts}})
		if e != nil || !slices.Equal(got, golden[i]) {
			return fmt.Errorf("selfcheck step %d: %v %v", i+1, got, e)
		}
	}
	if !slices.Equal(y.Flush(), golden[8]) || y.Dropped() != 1 || len(y.All()) != 9 {
		return fmt.Errorf("selfcheck flush: %v", y.All())
	}
	last := map[winID]int{}
	for _, ot := range y.All() { // 不变量2：累计单调不减
		k := winID{ot.Key, ot.Start}
		if ot.Count < last[k] {
			return fmt.Errorf("selfcheck monotonic: %v", ot)
		}
		last[k] = ot.Count
	}
	z := New(sp, 1) // 不变量4：被拒整批不留痕、之后仍可用
	b0, d0 := z.All(), z.Dropped()
	if _, e := z.Feed([]Event{{Key: "", TS: 1}}); e != ErrEmptyKey {
		return fmt.Errorf("selfcheck emptykey: %v", e)
	}
	sp2, _ := cwin.New(12, 4, 100) // 大 delay：两个大窗口并存才超限
	z2 := New(sp2, 1)
	_, e2 := z2.Feed([]Event{{Key: "K", TS: 1}, {Key: "K", TS: 13}})
	if e2 != ErrTooManyOpenWindows || len(z2.All()) != 0 || z2.Dropped() != 0 {
		return fmt.Errorf("selfcheck capacity: %v", e2)
	}
	if !slices.Equal(z.All(), b0) || z.Dropped() != d0 {
		return errors.New("selfcheck: rejected batch left traces")
	}
	if _, e := z.Feed([]Event{{Key: "K", TS: 1}}); e != nil {
		return fmt.Errorf("selfcheck unusable: %w", e)
	}
	return nil
}
