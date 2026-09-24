// Package audit 提供句柄表的自检与统计。
package audit

import (
	"fmt"

	"ontology/table"
)

// Stats 是句柄表的统计快照。
type Stats struct {
	Live        int      // 存活（校验通过）句柄数
	Free        int      // 空闲槽位数
	Exhausted   int      // 代号耗尽槽位数
	Cap         int      // 容量
	Generations []uint32 // 各槽位当前代号
}

// Collect 采集统计快照。
func Collect(t *table.Table) Stats {
	slots := t.Slots()
	st := Stats{Free: len(t.FreeIndices()), Cap: t.Cap(), Generations: make([]uint32, len(slots))}
	for i, s := range slots {
		st.Generations[i] = s.Gen
		if s.InUse {
			st.Live++
		}
		if s.Exhausted {
			st.Exhausted++
		}
	}
	return st
}

// Check 一次性核验：Len+空闲+耗尽==Cap、空闲链表无在用槽位且无重复、
// 在用槽位的代号与其发出的句柄一致。全部通过返回 nil。
func Check(t *table.Table) error {
	slots := t.Slots()
	free := t.FreeIndices()
	seen := make(map[int]bool, len(free))
	for _, i := range free {
		switch {
		case i < 0 || i >= len(slots):
			return fmt.Errorf("audit: free index %d out of range", i)
		case seen[i]:
			return fmt.Errorf("audit: duplicate free slot %d", i)
		case slots[i].InUse:
			return fmt.Errorf("audit: slot %d both free and in use", i)
		case slots[i].Exhausted:
			return fmt.Errorf("audit: slot %d both free and exhausted", i)
		}
		seen[i] = true
	}
	live, dead := 0, 0
	for _, s := range slots {
		switch {
		case s.InUse:
			live++
			if _, err := t.Get(s.Handle); err != nil {
				return fmt.Errorf("audit: slot %d gen %d handle rejected: %w", s.Index, s.Gen, err)
			}
		case s.Exhausted:
			dead++
		}
	}
	if live != t.Len() {
		return fmt.Errorf("audit: live %d != Len %d", live, t.Len())
	}
	if live+len(free)+dead != t.Cap() {
		return fmt.Errorf("audit: live %d + free %d + exhausted %d != Cap %d",
			live, len(free), dead, t.Cap())
	}
	return nil
}
