package settlement

import (
	"fmt"
	"sort"
)

// version 一个电价表版本：生效时刻 + 按日类型划分的一日时段表。
type version struct {
	eff   int64
	sched [3][]Slot // 按 DayType 索引
}

// versionStore 按生效时刻升序保存全部版本。
type versionStore struct {
	vers []*version
}

// validateSchedules 校验每种日类型的时段表恰好无缝覆盖整日 [0, 86400)。
func validateSchedules(in map[DayType][]Slot) ([3][]Slot, error) {
	var out [3][]Slot
	if len(in) != 3 {
		return out, fmt.Errorf("时段表必须恰好覆盖三种日类型")
	}
	for dt := Workday; dt <= Holiday; dt++ {
		slots, ok := in[dt]
		if !ok || len(slots) == 0 {
			return out, fmt.Errorf("日类型 %s 缺少时段表", dt)
		}
		prevEnd := 0
		for _, sl := range slots {
			if sl.Start < 0 || sl.End > SecondsPerDay || sl.Start >= sl.End {
				return out, fmt.Errorf("日类型 %s 时段越界: [%d,%d)", dt, sl.Start, sl.End)
			}
			if sl.Price < 0 || sl.Price > MaxPrice {
				return out, fmt.Errorf("日类型 %s 单价越界: %d", dt, sl.Price)
			}
			if sl.Start != prevEnd {
				return out, fmt.Errorf("日类型 %s 时段表未无缝覆盖整日", dt)
			}
			prevEnd = sl.End
		}
		if prevEnd != SecondsPerDay {
			return out, fmt.Errorf("日类型 %s 时段表未覆盖整日", dt)
		}
		cp := make([]Slot, len(slots))
		copy(cp, slots)
		out[dt] = cp
	}
	return out, nil
}

// find 返回生效时刻为 eff 的版本下标；ok 表示存在。
func (s *versionStore) find(eff int64) (int, bool) {
	i := sort.Search(len(s.vers), func(i int) bool { return s.vers[i].eff >= eff })
	if i < len(s.vers) && s.vers[i].eff == eff {
		return i, true
	}
	return i, false
}

// add 按生效时刻有序插入版本；调用方保证 eff 不重复。
func (s *versionStore) add(eff int64, sched [3][]Slot) {
	i, _ := s.find(eff)
	s.vers = append(s.vers, nil)
	copy(s.vers[i+1:], s.vers[i:])
	s.vers[i] = &version{eff: eff, sched: sched}
}

// at 返回时刻 t 适用的版本：生效时刻不晚于 t 的版本中最新的一个。
func (s *versionStore) at(t int64) *version {
	i := sort.Search(len(s.vers), func(i int) bool { return s.vers[i].eff > t }) - 1
	if i < 0 {
		return nil
	}
	return s.vers[i]
}

// cutsBetween 把 (t0, t1) 内的版本切换时刻追加到 dst。
func (s *versionStore) cutsBetween(t0, t1 int64, dst []int64) []int64 {
	lo := sort.Search(len(s.vers), func(i int) bool { return s.vers[i].eff > t0 })
	hi := sort.Search(len(s.vers), func(i int) bool { return s.vers[i].eff >= t1 })
	for _, v := range s.vers[lo:hi] {
		dst = append(dst, v.eff)
	}
	return dst
}
