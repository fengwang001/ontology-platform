package lifecycle

import "fmt"

func (e *Engine) planSetAttr(wv *workView, op Op) ([]string, *Error) {
	inst := wv.instance(op.InstanceID)
	if inst == nil {
		return nil, &Error{Code: CodeUnknown, InstanceID: op.InstanceID,
			Detail: "实例不存在"}
	}
	if e.schema.IsFinal(inst.Type, inst.State) {
		return nil, &Error{Code: CodeTerminal, InstanceID: op.InstanceID,
			Detail: fmt.Sprintf("实例处于终态 %s，拒绝属性修改", inst.State)}
	}
	old, _ := wv.attr(op.InstanceID, op.Attr)
	wv.setAttr(op.InstanceID, op.Attr, op.Value)
	return []string{fmt.Sprintf("setAttr %s.%s: %v -> %v",
		op.InstanceID, op.Attr, old, op.Value)}, nil
}

func (e *Engine) planLink(wv *workView, op Op) ([]string, *Error) {
	l := op.Link
	from := wv.instance(l.FromID)
	to := wv.instance(l.ToID)
	if from == nil || to == nil {
		return nil, &Error{Code: CodeUnknown, InstanceID: l.FromID,
			Detail: fmt.Sprintf("链接端点不存在: %s -> %s", l.FromID, l.ToID)}
	}
	if op.Kind == OpDelLink {
		if wv.delLink(l) {
			return []string{fmt.Sprintf("delLink %s: %s -> %s", l.Type, l.FromID, l.ToID)}, nil
		}
		return []string{fmt.Sprintf("delLink %s: 链接不存在，空操作", l.Type)}, nil
	}
	if e.schema.IsFinal(from.Type, from.State) || e.schema.IsFinal(to.Type, to.State) {
		final := l.FromID
		if !e.schema.IsFinal(from.Type, from.State) {
			final = l.ToID
		}
		return nil, &Error{Code: CodeTerminal, InstanceID: final,
			Detail: fmt.Sprintf("终态实例 %s 拒绝新增链接", final)}
	}
	if wv.addLink(l) {
		return []string{fmt.Sprintf("addLink %s: %s -> %s", l.Type, l.FromID, l.ToID)}, nil
	}
	return []string{fmt.Sprintf("addLink %s: 链接已存在，空操作", l.Type)}, nil
}

// commitCOW 以 copy-on-write 方式把工作视图效果落地：
// 为每个属主（被改实例）基于其旧版本构造新版本（状态、属性、时钟、
// 属主邻接表均来自 wv 的 overlay），然后原子替换版本指针。
//
// 每一步原子替换都压入逆操作（恢复旧指针）；测试 failpoint 中途触发时
// 按与生效顺序相反的顺序撤销，对外仍是“整体不生效”。
func (e *Engine) commitCOW(s *Store, wv *workView, ops []Op, plans []*firePlan,
	lockIDs []string, slots map[string]*atomicPtr) *Error {
	type undo struct {
		id  string
		old *Instance
	}
	var undos []undo
	fail := func(err *Error) *Error {
		for i := len(undos) - 1; i >= 0; i-- {
			s.inst[undos[i].id].store(undos[i].old)
		}
		return err
	}

	stepCount := 0
	// 先按生效顺序（操作顺序、级联 BFS/DFS 顺序）应用状态迁移；
	// 版本构造统一在最终阶段完成，failpoint 在此序列上计数以验证回滚语义。
	for i, op := range ops {
		if op.Kind != OpFire || plans[i] == nil {
			continue
		}
		for range plans[i].steps {
			stepCount++
			if e.fp.failAfterSteps > 0 && stepCount == e.fp.failAfterSteps {
				return fail(&Error{Code: CodeHook, InstanceID: op.InstanceID,
					Detail: "测试 failpoint：提交中途失败，触发反向整体回滚"})
			}
		}
	}

	newClock := s.clock.Add(1)

	for _, id := range lockIDs {
		old := slots[id].load()
		nv := buildVersion(old, wv, newClock, id)
		slots[id].store(nv)
		undos = append(undos, undo{id: id, old: old})
	}
	return nil
}

// buildVersion 用工作视图 overlay 构造某实例的新版本。
// 属主邻接表（out）也以 overlay 重建，保证链接增删随版本原子可见。
func buildVersion(old *Instance, wv *workView, clock int64, id string) *Instance {
	nv := *old
	touched := false
	if st, ok := wv.state[id]; ok {
		nv.State = st
		touched = true
	}
	if ov, ok := wv.attrs[id]; ok {
		attrs := cloneAttrs(old.Attrs)
		for k, v := range ov {
			if v == nil {
				delete(attrs, k)
			} else {
				attrs[k] = v
			}
		}
		nv.Attrs = attrs
		touched = true
	}
	if wv.dirtyAdj[id] {
		nv.out = cloneOut(wv.out[id])
		touched = true
	} else {
		nv.out = cloneOut(old.out)
	}
	if touched {
		nv.Clock = clock
	}
	return &nv
}

func (e *Engine) nextSeq() int64 {
	return e.store.seq.Add(1)
}

func (e *Engine) logBatch(seq int64, ops []Op, res *BatchResult) {
	if e.logger == nil {
		return
	}
	for i, op := range ops {
		oc := res.Outcomes[i]
		fired := []FiredStep(nil)
		if res.Committed && oc.OK {
			fired = oc.Fired
		}
		e.logger.LogDecision(DecisionLog{
			Time:      nowTime(),
			BatchSeq:  seq,
			OpIndex:   i,
			Input:     op,
			Basis:     oc.Reason,
			Fired:     fired,
			Committed: res.Committed && oc.OK,
			Err:       oc.Err,
		})
	}
}
