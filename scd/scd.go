// Package scd 按键维护有序变更点与增量历史行：二分定位，增量完成替换/乱序拆分/乱序插入/闭合/新开，并提供 AsOf；并发安全。
package scd

import (
	"errors"
	"math/bits"
	"ontology/interval"
	"sync"
)

var ErrTooManyPoints = errors.New("scd: too many change points for key") // 某键变更点数超限
type Event struct {
	Key string
	interval.Point
}
type keyState struct {
	points []interval.Point // 按 Eff 升序、Eff 两两不同（权威点集）
	rows   []interval.Row   // 与点集始终一致的增量历史行
}
type Table struct {
	mu        sync.RWMutex
	maxPoints int
	states    map[string]*keyState
	cmp       int // 非导出计数器：最近一次处理事件时，两次二分（点集+历史行，行首即变更点 Eff）为定位而比较的次数
}

func NewTable(n int) *Table { return &Table{maxPoints: n, states: map[string]*keyState{}} }

// search 是下界二分，返回第一个使 less(i)==false 的下标；本身不碰计数器。
func search(n int, less func(i int) bool) int {
	lo, hi := 0, n
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if less(m) {
			lo = m + 1
		} else {
			hi = m
		}
	}
	return lo
}
func ins[T any](s []T, i int, v T) []T {
	s = append(s, *new(T))
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}
func (t *Table) applyOne(ks *keyState, p interval.Point) {
	// 把单个已预检合法的点增量并入键状态（调用方持写锁，故可安全累加 cmp）。
	i := search(len(ks.points), func(m int) bool { t.cmp++; return ks.points[m].Eff < p.Eff })
	found := i < len(ks.points) && ks.points[i].Eff == p.Eff
	j := search(len(ks.rows), func(m int) bool { t.cmp++; return ks.rows[m].From < p.Eff })
	if found { // 替换同一时刻：点数不增，仅更换该点自身产生的行
		if !ks.points[i].Del {
			ks.rows = append(ks.rows[:j], ks.rows[j+1:]...)
		}
		ks.points[i] = p
	} else {
		if j > 0 && ks.rows[j-1].To > p.Eff {
			ks.rows[j-1].To = p.Eff // 落入前行内部：拆分，闭合左段
		}
		ks.points = ins(ks.points, i, p)
	}
	if !p.Del { // Delete 只闭合不新开；Upsert 新开 [Eff, 下一点 Eff)
		to := interval.Inf
		if i+1 < len(ks.points) {
			to = ks.points[i+1].Eff
		}
		ks.rows = ins(ks.rows, j, interval.Row{From: p.Eff, To: to, Val: p.Val})
	}
}
func (t *Table) Apply(evs []Event) error {
	// 原子应用一批：先按批内顺序预检（Eff 合法、每键最终点数不超限，同一 Eff
	// 的替换不占名额），任一项不过即整批拒绝、状态不动；预检通过后并入不会失败。
	t.mu.Lock()
	defer t.mu.Unlock()
	add, cnt, exist := map[string]map[int64]bool{}, map[string]int{}, map[string]map[int64]bool{}
	for _, e := range evs {
		if !interval.ValidEff(e.Eff) {
			return interval.ErrInvalidEff
		}
		if add[e.Key] == nil {
			add[e.Key], exist[e.Key] = map[int64]bool{}, map[int64]bool{}
			if ks := t.states[e.Key]; ks != nil {
				cnt[e.Key] = len(ks.points)
				for _, p := range ks.points {
					exist[e.Key][p.Eff] = true
				}
			}
		}
		if !add[e.Key][e.Eff] {
			add[e.Key][e.Eff] = true
			if !exist[e.Key][e.Eff] {
				cnt[e.Key]++
			}
		}
		if cnt[e.Key] > t.maxPoints {
			return ErrTooManyPoints
		}
	}
	for _, e := range evs {
		ks := t.states[e.Key]
		if ks == nil {
			ks = &keyState{}
			t.states[e.Key] = ks
		}
		t.applyOne(ks, e.Point)
	}
	return nil
}
func (t *Table) History(key string) []interval.Row {
	// 返回某键历史行的拷贝（无该键时为 nil）。
	t.mu.RLock()
	defer t.mu.RUnlock()
	if ks := t.states[key]; ks != nil {
		return append([]interval.Row(nil), ks.rows...)
	}
	return nil
}
func (t *Table) AsOf(key string, at int64) (string, bool) {
	// 返回时刻 at 该键的值；键在该时刻不存在时 ok=false。
	t.mu.RLock()
	defer t.mu.RUnlock()
	ks := t.states[key]
	if ks == nil {
		return "", false
	}
	i := search(len(ks.rows), func(m int) bool { return ks.rows[m].From <= at })
	if i > 0 && at < ks.rows[i-1].To {
		return ks.rows[i-1].Val, true
	}
	return "", false
}
func (t *Table) LocatingIsLogarithmic() bool {
	// 用内部 cmp 验证多档 m 的乱序定位比较数有 2*ceil(log2 m)+4 上界；只回传布尔，数值不出包。
	for _, m := range []int{100, 1000, 10000} {
		b, es := NewTable(m+1), make([]Event, m)
		for k := range es {
			es[k] = Event{"M", interval.Point{Eff: int64(2 * k), Val: "x"}}
		}
		e1 := b.Apply(es)
		b.cmp = 0
		e2 := b.Apply([]Event{{"M", interval.Point{Eff: int64(m + 1), Val: "y"}}})
		if e1 != nil || e2 != nil || b.cmp > 2*bits.Len(uint(m-1))+4 {
			return false
		}
	}
	return true
}
