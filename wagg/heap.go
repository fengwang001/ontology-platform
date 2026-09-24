package wagg

import (
	"container/heap"
	"fmt"
	"slices"
)

type (
	Event struct {
		Key string
		TS  int64
	}
	Change struct {
		Add               bool
		Key               string
		Start, End, Count int64
	}
	ViewKey struct {
		Key        string
		Start, End int64
	}
	wkey struct {
		Key   string
		Start int64
	}
)

type winState struct {
	key               string
	start, end, count int64
	fired             bool
	idx               int
}

func pushHeap(h *endHeap, s *winState)   { heap.Push(h, s) }
func removeHeap(h *endHeap, s *winState) { heap.Remove(h, s.idx) }

// endHeap 按 end（再按 key、start）有序，保证 advance 沿序定位而非整表扫描。
type endHeap []*winState

func (h endHeap) Len() int { return len(h) }
func (h endHeap) Less(i, j int) bool {
	x, y := h[i], h[j]
	return x.end < y.end || x.end == y.end && (x.key < y.key || x.key == y.key && x.start < y.start)
}
func (h endHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].idx, h[j].idx = i, j }
func (h *endHeap) Push(x any) {
	s := x.(*winState)
	s.idx = len(*h)
	*h = append(*h, s)
}
func (h *endHeap) Pop() any {
	old := *h
	s := old[len(old)-1]
	*h = old[:len(old)-1]
	return s
}

// 第三节八事件序列及其推导日志（末两条为 Flush；步8 仅静默 purge 无输出）。
var builtinSeq = []Event{
	{"K", 2}, {"K", 5}, {"K", 9}, {"K", 15},
	{"K", 4}, {"K", 12}, {"K", 7}, {"K", 20},
}
var expectedLog = []Change{
	{true, "K", 0, 10, 2},                         // 步2 early
	{true, "K", 0, 10, 3},                         // 步4 on-time（唯一一次）
	{false, "K", 0, 10, 3}, {true, "K", 0, 10, 4}, // 步5 迟到接受
	{true, "K", 10, 20, 2},                        // 步6 early
	{false, "K", 0, 10, 4}, {true, "K", 0, 10, 5}, // 步7 迟到接受
	{true, "K", 10, 20, 2}, {true, "K", 20, 30, 1},
}

func (a *Agg) View() map[ViewKey]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	v := map[ViewKey]int64{}
	for _, c := range a.log {
		k := ViewKey{c.Key, c.Start, c.End}
		if c.Add {
			v[k] = c.Count
		} else {
			delete(v, k)
		}
	}
	return v
}
func (a *Agg) SelfCheck() error {
	g, err := New(10, 3, 5, 2, 0)
	if err != nil {
		return err
	}
	var all []Change
	for _, e := range builtinSeq {
		cs, err := g.Feed([]Event{e})
		if err != nil {
			return err
		}
		all = append(all, cs...)
	}
	all = append(all, g.Flush()...)
	if !slices.Equal(all, expectedLog) { // 与 NOTES.md 推导逐条一致（含 on-time 唯一）
		return fmt.Errorf("changelog %v want %v", all, expectedLog)
	}
	want := map[ViewKey]int64{{"K", 0, 10}: 5, {"K", 10, 20}: 2, {"K", 20, 30}: 1} // 不变量 1
	if fmt.Sprint(g.View()) != fmt.Sprint(want) {
		return fmt.Errorf("invariant 1: %v", g.View())
	}
	cur := map[ViewKey]int64{} // 不变量 2：每个前缀一键至多一值，每条 - 恰撤回当前值
	for i, c := range all {
		k := ViewKey{c.Key, c.Start, c.End}
		if c.Add {
			cur[k] = c.Count
		} else if v := cur[k]; v != c.Count {
			return fmt.Errorf("invariant 2 at %d: -%d not current %d", i, c.Count, v)
		} else {
			delete(cur, k)
		}
	}
	before := len(g.log) // 不变量 4：非法整批被拒后日志/丢弃数不变，仍可继续使用
	if _, err := g.Feed([]Event{{"K", 1}, {"", 2}}); err != ErrInvalidEvent {
		return fmt.Errorf("invariant 4: want ErrInvalidEvent, got %v", err)
	}
	if len(g.log) != before || g.Dropped() != 0 {
		return fmt.Errorf("invariant 4: rejected batch left a trace")
	}
	if _, err := g.Feed([]Event{{"K", 2}}); err != nil {
		return fmt.Errorf("invariant 4: unusable after rejection: %v", err)
	}
	q, _ := New(10, 1000000, 0, 1, 0) // m 个未触发窗口（大 delay 保活）
	evs := make([]Event, 256)
	for i := range evs {
		evs[i] = Event{string(rune('a' + i)), 0}
	}
	if _, err := q.Feed(evs); err != nil {
		return err
	}
	if _, err := q.Feed([]Event{{"probe", 1}}); err != nil { // wm 只进 1，不触发任何窗口
		return err
	}
	if q.inspected > 2 { // 与 m 无关的小常数：堆序定位而非整表扫描
		return fmt.Errorf("inspected %d with m=256", q.inspected)
	}
	return nil
}
