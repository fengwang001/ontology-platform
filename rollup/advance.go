package rollup

import (
	"sort"

	"ontology/hold"
	"ontology/tier"
)

// Advance 把时钟推进到 now，并在同一原子收敛周期内依次执行：
// ① 删除过期小时 ② L0→L1 折叠 ③ L1→L2 折叠。
// 任一步失败（溢出）或 panic，状态与时钟逐位回滚。
// 拒绝顺序：时钟回退 -> 溢出 -> 中止。
func (s *Store) Advance(now int64) (err error) {
	if now < 0 || now > tier.MaxClock {
		return tier.ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.now {
		return tier.ErrClockRollback
	}
	if now == s.now {
		return nil // 无操作
	}

	prev := s.st
	holds := s.Holds.Snapshot()
	draft := prev.clone()
	aborted := false

	// 任何 panic（含 PanicHook 注入）都转为中止错误并丢弃副本。
	defer func() {
		if r := recover(); r != nil {
			aborted = true
			err = tier.ErrAborted
		}
		if err != nil || aborted {
			s.st = prev
			// s.now 未被修改：只有三阶段全部成功后才提交新时钟。
		}
	}()

	if err = phaseDelete(&draft, now, s.a2, holds); err != nil {
		return err
	}
	s.fireHook("delete")

	if err = phaseFoldL0(&draft, now, s.a0, holds); err != nil {
		return err
	}
	s.fireHook("l0l1")

	if err = phaseFoldL1(&draft, now, s.a1, holds); err != nil {
		return err
	}
	s.fireHook("l1l2")

	// 提交：副本转正，时钟推进。折叠不增加净单位，故无需再查容量。
	s.st = draft
	s.now = now
	return nil
}

func (s *Store) fireHook(phase string) {
	if s.PanicHook != nil {
		s.PanicHook(phase)
	}
}

func heldOverlap(holds []hold.Hold, from, to int64) bool {
	for _, h := range holds {
		if tier.IntervalsOverlap(from, to, h.From, h.To) {
			return true
		}
	}
	return false
}

// ① 删除：小时 h 满足 now-(h+1)*HourMS >= A2 且小时区间不与保全相交，
// 则删除该小时在三层的全部数据。
func phaseDelete(d *state, now, a2 int64, holds []hold.Hold) error {
	candidates := map[int64]struct{}{}
	for _, p := range d.l0 {
		candidates[tier.HourOf(p.TS)] = struct{}{}
	}
	for m := range d.l1 {
		mf, _ := tier.MinuteRange(m)
		candidates[tier.HourOf(mf)] = struct{}{}
	}
	for h := range d.l2 {
		candidates[h] = struct{}{}
	}
	hours := make([]int64, 0, len(candidates))
	for h := range candidates {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	for _, h := range hours {
		hf, ht := tier.HourRange(h)
		if now-ht >= a2 && !heldOverlap(holds, hf, ht) {
			d.deleteHour(h)
		}
	}
	return nil
}

// ② L0→L1：分钟 m 够龄（now-(m+1)*MinuteMS >= A0）且不与保全相交时，
// 把 L0 中属于它的全部点（按有序顺序）并入 L1 桶 m。
func phaseFoldL0(d *state, now, a0 int64, holds []hold.Hold) error {
	type pts struct {
		m int64
		p []tier.Point
	}
	var groups []pts
	kept := d.l0[:0]
	for _, p := range d.l0 {
		m := tier.MinuteOf(p.TS)
		mf, mt := tier.MinuteRange(m)
		if now-mt >= a0 && !heldOverlap(holds, mf, mt) {
			if len(groups) == 0 || groups[len(groups)-1].m != m {
				groups = append(groups, pts{m: m})
			}
			groups[len(groups)-1].p = append(groups[len(groups)-1].p, p)
		} else {
			kept = append(kept, p)
		}
	}
	d.l0 = kept
	for _, g := range groups { // groups 天然按分钟升序
		b := d.l1[g.m]
		for _, p := range g.p {
			if err := b.AddPoint(p.V); err != nil {
				return err
			}
		}
		d.l1[g.m] = b
	}
	return nil
}

// ③ L1→L2：小时 h 够龄（now-(h+1)*HourMS >= A1）且不与保全相交时，
// 把它的全部分钟桶按分钟升序并入 L2 桶 h，并删除这些 L1 桶。
func phaseFoldL1(d *state, now, a1 int64, holds []hold.Hold) error {
	byHour := map[int64][]int64{}
	for m := range d.l1 {
		mf, _ := tier.MinuteRange(m)
		h := tier.HourOf(mf)
		hf, ht := tier.HourRange(h)
		if now-ht >= a1 && !heldOverlap(holds, hf, ht) {
			byHour[h] = append(byHour[h], m)
		}
	}
	hours := make([]int64, 0, len(byHour))
	for h := range byHour {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	for _, h := range hours {
		minutes := byHour[h]
		sort.Slice(minutes, func(i, j int) bool { return minutes[i] < minutes[j] })
		acc := d.l2[h]
		for _, m := range minutes {
			if err := acc.Merge(d.l1[m]); err != nil {
				return err
			}
		}
		d.l2[h] = acc
		for _, m := range minutes {
			delete(d.l1, m)
		}
	}
	return nil
}
