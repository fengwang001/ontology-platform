package lifecycle

import (
	"fmt"
)

// failpoint 仅用于测试：提交到第 failAfterSteps 个生效环节之后人为失败，
// 以验证“撤销顺序与生效顺序相反”的整体回滚。
type failpoint struct {
	failAfterSteps int
}

// firePlan 是单个迁移操作（含其整棵级联触发树）的规划。
type firePlan struct {
	rootIndex   int
	steps       []*planStep
	fired       []FiredStep
	mutexClaims map[string]string
}

// Batch 在单个处理单元内原子地执行一组操作：全部成功才提交，任一被拒则整体不生效。
//
// 并发协议：
//  1. 先从各实例原子版本指针无锁加载当前版本，在私有视图上推演（只读）。
//  2. 收集本单元涉及的全部实例（含级联对端），按 ID 字典序加锁。
//  3. 持锁重取版本并与推演基底比对：若期间被其他批改动则释放锁整体重试。
//  4. 提交为每个属主构造新版本并原子替换；不相交实例集合的批处理
//     不共享任何锁，因而完全并行；同一实例的并发批处理经实例锁串行化，
//     最终结果严格等价于某一个合法的串行顺序。
func (e *Engine) Batch(ops []Op) (*BatchResult, error) {
	seq := e.nextSeq()
	for attempt := 0; ; attempt++ {
		res, retry := e.tryBatch(seq, ops)
		if !retry {
			e.logBatch(seq, ops, res)
			return res, nil
		}
		if attempt > 64 {
			return nil, fmt.Errorf("lifecycle: batch 重规划超过上限")
		}
	}
}

func (e *Engine) tryBatch(seq int64, ops []Op) (*BatchResult, bool) {
	baseSnap := e.store.snapshot()
	wv := newWorkView(baseSnap)
	plans := make([]*firePlan, len(ops))
	outcomes := make([]*Outcome, len(ops))

	mutexUsed := map[string]map[string]int{}
	claimed := map[string]bool{}
	rejected := false

	for i, op := range ops {
		oc := &Outcome{Index: i}
		outcomes[i] = oc
		switch op.Kind {
		case OpFire:
			plan, err := e.planFire(wv, op.InstanceID, op.Transition, i, "", mutexUsed)
			plans[i] = plan
			if err != nil {
				oc.Err = err
				rejected = true
			} else {
				oc.OK = true
				oc.Fired = plan.fired
				for _, st := range plan.steps {
					oc.Reason = append(oc.Reason, st.basis...)
					claimed[st.inst.ID] = true
				}
			}
		case OpSetAttr:
			basis, err := e.planSetAttr(wv, op)
			oc.Reason = basis
			if err != nil {
				oc.Err = err
				rejected = true
			} else {
				oc.OK = true
				claimed[op.InstanceID] = true
			}
		case OpAddLink, OpDelLink:
			basis, err := e.planLink(wv, op)
			oc.Reason = basis
			if err != nil {
				oc.Err = err
				rejected = true
			} else {
				oc.OK = true
				claimed[op.Link.FromID] = true
				claimed[op.Link.ToID] = true
			}
		default:
			oc.Err = &Error{Code: CodeUnknown, InstanceID: op.InstanceID,
				Detail: "未知操作种类"}
			rejected = true
		}
	}

	if rejected {
		return &BatchResult{Committed: false, Outcomes: outcomes}, false
	}

	lockIDs := make([]string, 0, len(claimed))
	for id := range claimed {
		lockIDs = append(lockIDs, id)
	}
	lockIDs = uniqueSorted(lockIDs)
	s := e.store
	// 在加锁前缓存槽指针：提交全程只需实例锁，不再触碰全局锁，
	// 严格维持“RLock -> 实例锁”的单向锁序。
	s.mu.RLock()
	slots := make(map[string]*atomicPtr, len(lockIDs))
	for _, id := range lockIDs {
		slots[id] = s.inst[id]
	}
	s.mu.RUnlock()
	s.lockIDs(lockIDs)
	unlock := func() { s.unlockIDs(lockIDs) }

	// 持锁后重取当前版本，与规划基底比较。任何涉及实例版本不同都意味着
	// 规划期间被并发批改动，需要释放锁并整体重规划。
	changed := false
	for _, id := range lockIDs {
		b := baseSnap.inst[id]
		l := slots[id].load()
		if b == nil || l == nil || b != l {
			changed = true
			break
		}
	}
	if changed {
		unlock()
		return nil, true
	}

	if err := e.commitCOW(s, wv, ops, plans, lockIDs, slots); err != nil {
		unlock()
		for _, oc := range outcomes {
			oc.OK = false
			oc.Fired = nil
			oc.Err = err
		}
		return &BatchResult{Committed: false, Outcomes: outcomes}, false
	}
	unlock()
	return &BatchResult{Committed: true, Outcomes: outcomes}, false
}
