// Package api 对外提供增量滑动窗口聚合的物化视图。依赖 wagg。
package api

import (
	"cmp"
	"errors"
	"maps"
	"slices"

	"ontology/wagg"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrBadSize     = wagg.ErrBadSize
	ErrEmptyKey    = wagg.ErrEmptyKey
	ErrTooManyOpen = wagg.ErrTooManyOpen
)

// Event 是一条带事件时间的变更。
type Event = wagg.Event

// Summary 是一个 Key 在 Feed 返回时的快照。
type Summary struct {
	Key string
	Sum int64
}

// Window 是增量滑动窗口聚合器，并发安全。
type Window struct{ agg *wagg.Aggregator }

// New 创建聚合器。size 必须为正；maxOpen 为单 Key 窗口内事件数上限，<=0 表示不限。
func New(size int64, maxOpen int) (*Window, error) {
	a, err := wagg.New(size, maxOpen)
	if err != nil {
		return nil, err
	}
	return &Window{agg: a}, nil
}

// Feed 原子地喂入一批事件：任一条被拒，整批不生效。返回每个 Key 的快照（按 Key 排序）。
func (w *Window) Feed(evs []Event) ([]Summary, error) {
	if err := w.agg.Apply(evs); err != nil {
		return nil, err
	}
	view := w.View()
	out := make([]Summary, 0, len(view))
	for k, s := range view {
		out = append(out, Summary{Key: k, Sum: s})
	}
	slices.SortFunc(out, func(a, b Summary) int { return cmp.Compare(a.Key, b.Key) })
	return out, nil
}

// View 返回每个 Key 当前窗口内的 Val 之和。
func (w *Window) View() map[string]int64 {
	v, _ := w.agg.Snapshot()
	return v
}

// Dropped 返回所有 Key 累计的「窗口外到达」丢弃数。
func (w *Window) Dropped() int64 {
	_, d := w.agg.Snapshot()
	return d
}

// SelfCheck 对内置事件序列核验四条不变量：与批量重算一致、成员守恒、水位线单调
// （用反序重喂结果相同来观测）、失败不留痕；并核验三类错误可判定且互不相同。
func (w *Window) SelfCheck() bool {
	const size = 10
	seq := []Event{
		{Key: "a", TS: 5, Val: 10}, {Key: "b", TS: 1, Val: 1},
		{Key: "a", TS: 15, Val: 20}, {Key: "a", TS: 12, Val: 30},
		{Key: "a", TS: 12, Val: 40}, {Key: "b", TS: 3, Val: 2},
		{Key: "a", TS: 25, Val: 50}, {Key: "a", TS: 15, Val: 60},
		{Key: "a", TS: 35, Val: 70}, {Key: "b", TS: 2, Val: 4},
		{Key: "c", TS: 50, Val: 5}, {Key: "c", TS: 40, Val: 6}, // c 的第二条被丢弃
	}
	wantView, wantDropped := reference(seq, size)
	x, err := New(size, 0)
	if err != nil {
		return false
	}
	if _, err = x.Feed(seq); err != nil {
		return false
	}
	if !maps.Equal(x.View(), wantView) || x.Dropped() != wantDropped || !x.agg.SelfCheck() {
		return false
	}
	rev, _ := New(size, 0) // 反序重喂：wm 只进不退 ⇒ 最终视图相同
	revSeq := slices.Clone(seq)
	slices.Reverse(revSeq)
	if _, err = rev.Feed(revSeq); err != nil || !maps.Equal(rev.View(), wantView) {
		return false
	}
	before, d0 := x.View(), x.Dropped() // 失败不留痕：三类错误可判定且互不相同
	if _, err = x.Feed([]Event{{Key: "", TS: 1, Val: 1}}); !errors.Is(err, ErrEmptyKey) {
		return false
	}
	if _, err = New(0, 0); !errors.Is(err, ErrBadSize) {
		return false
	}
	z, _ := New(size, 1)
	if _, err = z.Feed([]Event{{Key: "k", TS: 1, Val: 1}}); err != nil {
		return false
	}
	if _, err = z.Feed([]Event{{Key: "k", TS: 2, Val: 2}, {Key: "k", TS: 3, Val: 3}}); !errors.Is(err, ErrTooManyOpen) {
		return false
	}
	if ErrBadSize == ErrEmptyKey || ErrEmptyKey == ErrTooManyOpen || ErrBadSize == ErrTooManyOpen {
		return false
	}
	if !maps.Equal(x.View(), before) || x.Dropped() != d0 {
		return false
	}
	if _, err = x.Feed([]Event{{Key: "d", TS: 100, Val: 7}}); err != nil { // 被拒后仍可正常使用
		return false
	}
	return x.View()["d"] == 7
}

// reference 用与实现无关的朴素方法重算：逐事件模拟到达次序，返回每个 Key 的 sum 与丢弃数。
func reference(seq []Event, size int64) (map[string]int64, int64) {
	wm, kept := map[string]int64{}, map[string][]Event{}
	var dropped int64
	for _, e := range seq {
		if e.TS > wm[e.Key] {
			wm[e.Key] = e.TS
		}
		if _, ok := kept[e.Key]; !ok {
			kept[e.Key] = nil
		}
		if e.TS > wm[e.Key]-size {
			kept[e.Key] = append(kept[e.Key], e)
		} else {
			dropped++
		}
	}
	out := make(map[string]int64, len(kept))
	for k := range kept {
		for _, e := range kept[k] {
			if e.TS > wm[k]-size {
				out[k] += e.Val
			}
		}
	}
	return out, dropped
}
