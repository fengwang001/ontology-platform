// Package audit 提供句柄表的自检与统计：核验不变量并汇总各槽位状态。
package audit

import (
	"errors"
	"fmt"

	"ontology/handle"
	"ontology/slot"
	"ontology/table"
)

// ErrInvariant 是自检失败的可判定错误，具体原因由包裹信息给出。
var ErrInvariant = errors.New("audit: 不变量被破坏")

// Stats 是表的一次性统计快照。
type Stats struct {
	Alive     int      // 在用槽位数
	Free      int      // 空闲槽位数
	Exhausted int      // 代号耗尽退役槽位数
	Cap       int      // 槽位总数
	Gens      []uint32 // 各槽位当前代号
}

// Collect 汇总表的统计信息。
func Collect(t *table.Table) Stats {
	views, free := t.Inspect()
	st := Stats{Cap: t.Cap(), Free: len(free), Gens: make([]uint32, len(views))}
	for i, v := range views {
		st.Gens[i] = v.Gen
		switch v.State {
		case slot.InUse:
			st.Alive++
		case slot.Exhausted:
			st.Exhausted++
		}
	}
	return st
}

// Check 一次性核验：不变量 3 等式、空闲链表无在用槽位且无重复、
// 传入的存活句柄与槽位代号一致。全部通过返回 nil。
func Check(t *table.Table, live ...handle.Handle) error {
	views, free := t.Inspect()
	st := Collect(t)
	if st.Alive+st.Free+st.Exhausted != st.Cap {
		return fmt.Errorf("%w: 存活%d+空闲%d+耗尽%d != 容量%d",
			ErrInvariant, st.Alive, st.Free, st.Exhausted, st.Cap)
	}
	if st.Alive != t.Len() {
		return fmt.Errorf("%w: Len()=%d 与实际在用 %d 不符", ErrInvariant, t.Len(), st.Alive)
	}
	seen := make(map[int32]bool, len(free))
	freeCount := 0
	for _, v := range views {
		if v.State == slot.Free {
			freeCount++
		}
	}
	if len(free) != freeCount {
		return fmt.Errorf("%w: 空闲链表长度 %d 与空闲槽位数 %d 不符", ErrInvariant, len(free), freeCount)
	}
	for _, i := range free {
		if seen[i] {
			return fmt.Errorf("%w: 空闲链表中槽位 %d 重复", ErrInvariant, i)
		}
		seen[i] = true
		if views[i].State != slot.Free {
			return fmt.Errorf("%w: 空闲链表中的槽位 %d 状态为 %v", ErrInvariant, i, views[i].State)
		}
	}
	for _, h := range live {
		v := views[h.Slot()]
		if v.State != slot.InUse || v.Gen != h.Gen() {
			return fmt.Errorf("%w: 存活句柄(槽位%d,代号%d)与槽位状态(%v,代号%d)不符",
				ErrInvariant, h.Slot(), h.Gen(), v.State, v.Gen)
		}
	}
	return nil
}
