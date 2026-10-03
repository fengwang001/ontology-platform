package tier

import (
	"sort"

	"ontology/hold"
	"ontology/rollup"
)

type holdInterval = hold.Interval

// Advance 把时钟推进到 now，并对当前状态一次性收敛：
// 删除过期小时、L0→L1、L1→L2。任一步溢出或注入 panic 都会回滚到调用前。
func (s *Store) Advance(now int64) (err error) {
	if now < 0 || now > maxTS {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.now {
		return ErrClock
	}
	if now == s.now {
		return nil
	}

	prev := s.snapshot()
	holds := s.holds.Snapshot()
	st := prev
	defer func() {
		if r := recover(); r != nil {
			s.commit(prev)
			err = ErrAborted
		}
	}()

	s.step("delete")
	if err = s.deleteExpired(&st, now, holds); err != nil {
		s.commit(prev)
		return err
	}
	s.step("fold0")
	if err = s.foldL0toL1(&st, now, holds); err != nil {
		s.commit(prev)
		return err
	}
	s.step("fold1")
	if err = s.foldL1toL2(&st, now, holds); err != nil {
		s.commit(prev)
		return err
	}

	s.commit(st)
	s.now = now
	return nil
}

func (s *Store) step(name string) {
	if s.hook != nil {
		s.hook(name)
	}
}

func intersects(holds []holdInterval, from, to int64) bool {
	for _, h := range holds {
		if h.From < to && from < h.To {
			return true
		}
	}
	return false
}

// ① 删除：够龄且不与保全相交的小时，清除其在三层的全部数据。
func (s *Store) deleteExpired(st *state, now int64, holds []holdInterval) error {
	hours := sortedHours(st)
	for _, h := range hours {
		from, to := rollup.HourRange(h)
		if intersects(holds, from, to) {
			continue
		}
		if now-to < s.a2 {
			continue
		}
		delete(st.l2, h)
		for m := h * 60; m < (h+1)*60; m++ {
			delete(st.l1, m)
		}
		kept := st.l0[:0]
		for _, p := range st.l0 {
			if rollup.HourOf(p.TS) != h {
				kept = append(kept, p)
			}
		}
		st.l0 = append([]rollup.Point(nil), kept...)
	}
	return nil
}

// ② L0→L1：够龄且不与保全相交的分钟，其全部点并入 L1 桶并从 L0 移除。
func (s *Store) foldL0toL1(st *state, now int64, holds []holdInterval) error {
	groups := map[int64]rollup.Bucket{}
	order := []int64{}
	kept := make([]rollup.Point, 0, len(st.l0))
	for _, p := range st.l0 {
		m := rollup.MinuteOf(p.TS)
		from, to := rollup.MinuteRange(m)
		if now-to >= s.a0 && !intersects(holds, from, to) {
			b, ok := groups[m]
			nb, err := rollup.AddPoint(b, p.V)
			if err != nil {
				return ErrOverflow
			}
			if !ok {
				order = append(order, m)
			}
			groups[m] = nb
		} else {
			kept = append(kept, p)
		}
	}
	sortMinutes(order)
	for _, m := range order {
		b, err := rollup.Merge(st.l1[m], groups[m])
		if err != nil {
			return ErrOverflow
		}
		st.l1[m] = b
	}
	st.l0 = kept
	return nil
}

// ③ L1→L2：够龄且不与保全相交的小时，其全部分钟桶并入 L2 并删除 L1 桶。
func (s *Store) foldL1toL2(st *state, now int64, holds []holdInterval) error {
	hourBuckets := map[int64]rollup.Bucket{}
	hourMinutes := map[int64][]int64{}
	minutes := make([]int64, 0, len(st.l1))
	for m := range st.l1 {
		minutes = append(minutes, m)
	}
	sort.Slice(minutes, func(i, j int) bool { return minutes[i] < minutes[j] })
	for _, m := range minutes {
		h := m / 60
		b, err := rollup.Merge(hourBuckets[h], st.l1[m])
		if err != nil {
			return ErrOverflow
		}
		hourBuckets[h] = b
		hourMinutes[h] = append(hourMinutes[h], m)
	}
	order := make([]int64, 0, len(hourBuckets))
	for h := range hourBuckets {
		order = append(order, h)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, h := range order {
		from, to := rollup.HourRange(h)
		if intersects(holds, from, to) || now-to < s.a1 {
			continue
		}
		merged, err := rollup.Merge(st.l2[h], hourBuckets[h])
		if err != nil {
			return ErrOverflow
		}
		st.l2[h] = merged
		for _, m := range hourMinutes[h] {
			delete(st.l1, m)
		}
	}
	return nil
}

func sortedHours(st *state) []int64 {
	seen := map[int64]struct{}{}
	for h := range st.l2 {
		seen[h] = struct{}{}
	}
	for m := range st.l1 {
		seen[m/60] = struct{}{}
	}
	for _, p := range st.l0 {
		seen[rollup.HourOf(p.TS)] = struct{}{}
	}
	hours := make([]int64, 0, len(seen))
	for h := range seen {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	return hours
}

func sortMinutes(ms []int64) {
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
}
