package lifecycle

import "fmt"

// planFire 在工作视图上规划一次迁移及其全部级联随迁。
//
// 规划是“先完整推演、再一次性并入单元视图”的只读过程：
// 在私有 tentative 视图上按 DFS 逐环迁移，每环立即看到上游迁移后的
// 真实状态/链接，从而：
//   - 循环在任何一环并入单元视图之前就沿递归路径检出（CodeCycle）；
//   - 迁移后基数与钩子使用迁移生效后的真实链接状态；
//   - 任一环失败，私有视图直接丢弃，真实存储与单元视图均无任何变化。
//
// 固定判定优先级（每一环）：
//
//	未声明/源态不匹配(Unknown) > 终态(Terminal) > 前置条件(Precondition)
//	> 互斥(Mutex) > 循环(Cycle) > 基数(Cardinality) > 钩子(Hook)
//
// 多个错误并存时返回优先级最高者。
func (e *Engine) planFire(
	wv *workView,
	instanceID, transitionName string,
	rootIndex int,
	cascadeOf string,
	mutexUsed map[string]map[string]int,
) (*firePlan, *Error) {
	// 以单元视图当前状态为基底做私有推演。
	tent := newWorkView(wv.base)
	tent.state = copyStringMap(wv.state)
	tent.attrs = copyAttrOverlay(wv.attrs)
	tent.out = copyAdj(wv.out)

	plan := &firePlan{rootIndex: rootIndex, mutexClaims: map[string]string{}}
	visiting := map[string]bool{}
	finished := map[string]*planStep{}
	// 根环节按“本处理单元开始前”的状态求值：互斥迁移针对的是
	// 同一触发时刻各自都成立的情形；级联环则按上游迁移后状态求值。
	rootState := ""
	if in0 := wv.base.inst[instanceID]; in0 != nil {
		rootState = in0.State
	}
	rootSeen := false

	var dfs func(id, transName, cascadeOf string) *Error
	dfs = func(id, transName, cascadeOf string) *Error {
		inst := tent.instance(id)
		if id == instanceID && !rootSeen {
			inst.State = rootState
			rootSeen = true
		}
		if inst == nil {
			return &Error{Code: CodeUnknown, InstanceID: id, Transition: transName,
				Detail: "实例不存在"}
		}
		trans := e.schema.LookupTransition(inst.Type, transName)
		if trans == nil || trans.From != inst.State {
			return &Error{Code: CodeUnknown, InstanceID: id, Transition: transName,
				Detail: fmt.Sprintf("类型 %s 在状态 %s 下未声明迁移 %s",
					inst.Type, inst.State, transName)}
		}
		if e.schema.IsFinal(inst.Type, inst.State) {
			return &Error{Code: CodeTerminal, InstanceID: id, Transition: trans.Name,
				Detail: fmt.Sprintf("实例处于终态 %s，拒绝迁移", inst.State)}
		}

		// 循环：递归路径上再次遇到同一实例（在任何状态改变外被检出）。
		if visiting[id] {
			return &Error{Code: CodeCycle, InstanceID: id, Transition: trans.Name,
				Detail: fmt.Sprintf("链式触发在实例 %s 处形成循环", id)}
		}
		// 同一棵树中两条分支收敛到同一实例的同一条迁移：只生效一次。
		if prev, done := finished[id]; done {
			if prev.trans.Name == trans.Name {
				return nil
			}
			return &Error{Code: CodeCycle, InstanceID: id, Transition: trans.Name,
				Detail: fmt.Sprintf("链式触发冲突：%s 被要求同时迁移 %s 与 %s",
					id, prev.trans.Name, trans.Name)}
		}

		// 前置条件（迁移前）。
		basis, preErr := e.evalPreconds(tent, inst, trans)
		if preErr != nil {
			return preErr
		}

		// 互斥（调用方声明顺序优先：批内已被其他操作先占即拒绝）。
		if trans.MutexGroupID != "" {
			if holder := mutexUsed[id]; holder != nil {
				if heldBy, ok := holder[trans.MutexGroupID]; ok && heldBy != rootIndex {
					return &Error{Code: CodeMutex, InstanceID: id, Transition: trans.Name,
						Detail: fmt.Sprintf("互斥组 %s 已被批内顺序更靠前的操作 %d 占用",
							trans.MutexGroupID, heldBy)}
				}
			}
		}

		step := &planStep{
			inst: inst, trans: trans, cascadeOf: cascadeOf, basis: basis,
			fired: FiredStep{
				InstanceID: id, Type: inst.Type, Transition: trans.Name,
				From: inst.State, To: trans.To, CascadeOf: cascadeOf,
			},
		}

		// 在私有视图上即时生效本环（DFS 回滚靠“整体丢弃 tent”实现，
		// 因此这里不需要对私有视图维护逆操作）。
		visiting[id] = true
		tent.setState(id, trans.To)
		plan.steps = append(plan.steps, step)
		plan.fired = append(plan.fired, step.fired)

		// 递归级联：对端以上游迁移后的链接状态被发现，
		// 是否随迁取决于对端“当前”状态是否落在 WhenStates。
		for _, cr := range trans.Cascades {
			for _, pid := range tent.peers(id, cr.LinkType) {
				// 环回边：对端仍在当前递归路径上，无论它此刻是什么状态，
				// 这条“迁移 -> 级联”关系都构成循环触发，必须在生效前拒绝。
				if visiting[pid] {
					return &Error{Code: CodeCycle, InstanceID: pid,
						Transition: cr.Transition,
						Detail:     fmt.Sprintf("链式触发在实例 %s 处形成循环", pid)}
				}
				pst, ok := tent.stateOf(pid)
				if !ok || !containsState(cr.WhenStates, pst) {
					continue
				}
				if err := dfs(pid, cr.Transition, id); err != nil {
					return err
				}
			}
		}

		// 迁移后基数校验（此刻本环及其级联均已在 tent 中生效）。
		postInst := tent.instance(id)
		postBasis, postErr := e.evalPost(tent, postInst, trans)
		step.basis = append(step.basis, postBasis...)
		if postErr != nil {
			return postErr
		}

		visiting[id] = false
		finished[id] = step
		if trans.MutexGroupID != "" {
			plan.mutexClaims[id] = trans.MutexGroupID
		}
		return nil
	}

	if err := dfs(instanceID, transitionName, cascadeOf); err != nil {
		return nil, err
	}

	// 规划通过：占用互斥组，并把私有推演效果并入单元视图。
	for id, group := range plan.mutexClaims {
		if mutexUsed[id] == nil {
			mutexUsed[id] = map[string]int{}
		}
		mutexUsed[id][group] = rootIndex
	}
	wv.state = mergeStringMap(wv.state, tent.state)
	wv.attrs = mergeAttrOverlay(wv.attrs, tent.attrs)
	wv.out = tent.out
	for id := range tent.dirtyAdj {
		wv.dirtyAdj[id] = true
	}
	return plan, nil
}
