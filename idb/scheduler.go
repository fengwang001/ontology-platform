package idb

// scheduler 负责一个数据库上事务的准入排队。
// 活动集合 active 只包含尚未结束（含排队中）的事务，
// 已结束事务立即摘除，故准入判定只扫描未结束事务，开销与历史事务数无关。
type scheduler struct {
	order     []*Transaction // 创建序，未结束事务（含排队与运行）
	nextID    uint64
	commitSeq uint64
}

func newScheduler() *scheduler { return &scheduler{} }

func (sc *scheduler) register(t *Transaction) {
	sc.nextID++
	t.id = sc.nextID
	sc.order = append(sc.order, t)
}

func (sc *scheduler) runningCount() int {
	n := 0
	for _, t := range sc.order {
		if t.state == Running {
			n++
		}
	}
	return n
}

func (sc *scheduler) unregister(t *Transaction) {
	for i, x := range sc.order {
		if x == t {
			sc.order = append(sc.order[:i], sc.order[i+1:]...)
			return
		}
	}
}

// startReady 按创建序把所有当前可开始的排队事务收集成“一波”并统一放行。
// 对每个排队事务 t，检查创建序上更早的未结束事务 x：
//   - x 正在运行：t 必须与 x 可并行；
//   - x 在本波中（同一批一起开始，不存在谁越过谁）：必须两两可并行；
//   - x 仍在队中：若作用域与 t 重叠，t 不得越过它（即使当前可并行）；
//     若作用域不重叠，t 不受影响，可以先行。
//
// order 只保留未结束事务，判定开销不随已结束事务数增长。
func (sc *scheduler) startReady(start func(*Transaction)) {
	var wave []*Transaction
	for _, t := range sc.order {
		if t.state != Queued {
			continue
		}
		ok := true
		for _, x := range sc.order {
			if x == t || x.id > t.id {
				continue
			}
			switch x.state {
			case Running:
				if !parallel(x, t) {
					ok = false
				}
			case Queued:
				if inWave(wave, x) {
					if !parallel(x, t) {
						ok = false
					}
				} else if scopesOverlap(x, t) {
					ok = false // 不得越过更早、重叠、仍排队的事务
				}
			}
		}
		if ok {
			wave = append(wave, t)
		}
	}
	for _, t := range wave {
		start(t)
	}
}

func inWave(wave []*Transaction, t *Transaction) bool {
	for _, x := range wave {
		if x == t {
			return true
		}
	}
	return false
}

// parallel 判定两个事务是否可并行：
// 作用域不重叠恒可并行；只读之间重叠可并行；其余重叠不可并行。
// parallel：作用域不重叠恒并行；两个只读事务重叠可并行；
// 版本变更事务独占，与任何事务都不并行。
func parallel(a, b *Transaction) bool {
	if a.mode == VersionChange || b.mode == VersionChange {
		return false
	}
	if !scopesOverlap(a, b) {
		return true
	}
	return a.mode == ReadOnly && b.mode == ReadOnly
}

func scopesOverlap(a, b *Transaction) bool {
	small, large := a.scope, b.scope
	if len(small) > len(large) {
		small, large = large, small
	}
	for n := range small {
		if _, ok := large[n]; ok {
			return true
		}
	}
	return false
}

// nextCommit 为一次提交分配单调递增的可见序号。
func (sc *scheduler) nextCommit() uint64 {
	sc.commitSeq++
	return sc.commitSeq
}
