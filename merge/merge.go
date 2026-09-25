// Package merge 维护一组互不相交的左闭右开区间，相接即合并。
package merge

import (
	"slices"
	"sync"

	"ontology/iv"
)

// Merger 增量合并区间；Ranges 始终按 Start 升序且两两不相交。
type Merger struct {
	mu     sync.Mutex
	ranges []iv.Interval
	total  int // 历史区间总数（非导出计数器）
}

func New() *Merger { return &Merger{} }

// Add 合并一个区间；v 须通过 iv.New 校验。
func (m *Merger) Add(v iv.Interval) error {
	if _, err := iv.New(v.Start, v.End); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.total++
	// 相接也算重叠：end >= next.Start 即并入，保证 [1,2)+[2,3)=[1,3)。
	lo, hi := v.Start, v.End
	keep := m.ranges[:0]
	for _, r := range m.ranges {
		switch {
		case r.End < lo: // 严格在左，留缝
			keep = append(keep, r)
		case hi < r.Start: // 严格在右，留缝
			keep = append(keep, r)
		default: // 重叠或相接，吸收
			lo, hi = min(lo, r.Start), max(hi, r.End)
		}
	}
	i, _ := slices.BinarySearchFunc(keep, lo, func(r iv.Interval, s int) int { return r.Start - s })
	m.ranges = slices.Insert(keep, i, iv.Interval{Start: lo, End: hi})
	return nil
}

// AddAll 依次合并一批区间，遇非法区间即返回错误。
func (m *Merger) AddAll(vs []iv.Interval) error {
	for _, v := range vs {
		if err := m.Add(v); err != nil {
			return err
		}
	}
	return nil
}

// Ranges 返回当前互不相交、按 Start 升序的区间快照。
func (m *Merger) Ranges() []iv.Interval {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.ranges)
}

// Total 返回历史加入的区间总数（合并只减不增的对照基准）。
func (m *Merger) Total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total
}
