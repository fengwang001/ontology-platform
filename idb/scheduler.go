package idb

// Mode 是事务模式：只读或读写。
type Mode int

const (
	ReadOnly Mode = iota
	ReadWrite
)

func (m Mode) String() string {
	if m == ReadWrite {
		return "readwrite"
	}
	return "readonly"
}

// scheduler 只保留未结束的事务（排队 + 运行中），
// 因此"新事务能否立即开始"的判定开销与已结束事务数量无关。
type scheduler struct {
	pending []*Tx // 按创建序排列
	running map[*Tx]struct{}
}

func newScheduler() scheduler {
	return scheduler{running: map[*Tx]struct{}{}}
}

func scopesOverlap(a, b *Tx) bool {
	small, large := a.scope, b.scope
	if len(large) < len(small) {
		small, large = large, small
	}
	for name := range small {
		if large[name] {
			return true
		}
	}
	return false
}

// canRunTogether 判定两个事务能否并行：
// 作用域不重叠总是可以；重叠时仅只读-只读可以。
func canRunTogether(a, b *Tx) bool {
	if !scopesOverlap(a, b) {
		return true
	}
	return a.mode == ReadOnly && b.mode == ReadOnly
}

// computeStarts 按创建序扫描排队事务，返回本轮可开始的事务并把它们移入 running。
// 一个事务不得越过比它早创建、作用域重叠且仍未开始的排队事务（越过的判定只看重叠与创建序），
// 且不得与任何正在运行的事务违反并行规则。
func (s *scheduler) computeStarts(k *Kernel) []*Tx {
	if len(s.pending) == 0 {
		return nil
	}
	var starts []*Tx
	var remaining []*Tx
	for _, tx := range s.pending {
		can := true
		for _, earlier := range remaining {
			k.overlapChecks++
			if scopesOverlap(earlier, tx) {
				can = false
				break
			}
		}
		if can {
			for r := range s.running {
				k.overlapChecks++
				if !canRunTogether(r, tx) {
					can = false
					break
				}
			}
		}
		if can {
			s.running[tx] = struct{}{}
			starts = append(starts, tx)
		} else {
			remaining = append(remaining, tx)
		}
	}
	s.pending = remaining
	return starts
}

func (s *scheduler) removePending(tx *Tx) {
	for i, t := range s.pending {
		if t == tx {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}
