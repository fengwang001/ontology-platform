package lifecycle

// stepResult 记录单个胜出步的判定依据。
type stepResult struct {
	st      *step
	err     *LifecycleError
	reasons []string
}

// evaluate 在“锁内权威计划”上做统一判定。
//
// 固定点求解最终生效集合：
//  1. 每轮只沿当前仍存活的胜出根展开覆盖；
//  2. 在同一个“生效后投影”内按深度稳定顺序完成 前置条件 → 暂定推进 →
//     属性/迁移后基数/跨实例钩子 判定；
//  3. 任一环节失败的根被淘汰并记录固定优先级中最严重的错误，进入下一轮；
//  4. 仲裁落败根的最终结论：自身前置不成立 → ErrPrecondition；胜出方失败
//     → 跟随胜出方错误码；胜出方成功 → ErrMutex。
//
// 每轮都在全新投影上重算，因此上一轮被拒环节的暂定状态不会泄漏到下一轮，
// 天然实现“撤销顺序与生效顺序相反”的整体回滚语义。
func (e *Engine) evaluate(fresh *buildResult) (
	map[int]*LifecycleError,
	[]*step,
	*view,
	map[InstanceID]*stepResult,
) {
	rootErr := map[int]*LifecycleError{}
	dead := map[int]bool{}
	for _, pl := range fresh.plans {
		if pl.err != nil {
			if pl.err.Code != ErrCycle {
				rootErr[pl.rootIdx] = pl.err
				dead[pl.rootIdx] = true
			}
		}
	}

	maxIter := len(fresh.plans) + len(fresh.nodes) + 2
	for iter := 0; iter < maxIter; iter++ {
		// 循环根：在“假设整条循环链全部生效”的乐观投影上诊断是否存在比
		// ErrCycle 优先级更高的错误（前置条件/基数/钩子），有则从高报告。
		for _, pl := range fresh.plans {
			if pl.err == nil || pl.err.Code != ErrCycle || dead[pl.rootIdx] {
				continue
			}
			if better := diagnoseCycle(e, fresh, pl); better != nil {
				rootErr[pl.rootIdx] = better
			} else {
				rootErr[pl.rootIdx] = pl.err
			}
			dead[pl.rootIdx] = true
		}
		// 同一实例上的落败根若处在循环中，跟随该实例循环计划诊断出的
		// 更高优先级错误（如跨实例钩子），而不是报 ErrMutex。
		for _, pl := range fresh.plans {
			if !pl.loser || dead[pl.rootIdx] {
				continue
			}
			n := fresh.nodes[pl.root.Instance]
			if n != nil && n.cycle {
				if better := diagnoseCycle(e, fresh,
					&plan{root: pl.root, rootIdx: pl.rootIdx, steps: pl.steps}); better != nil {
					rootErr[pl.rootIdx] = better
				} else {
					rootErr[pl.rootIdx] = newErr(ErrCycle, pl.root.Instance, pl.root.Rule,
						"cascade cycle detected before any effect")
				}
				dead[pl.rootIdx] = true
			}
		}

		// 覆盖：当前存活的非落败根沿胜出边覆盖节点，记录深度用于模拟顺序。
		cover := map[InstanceID]*node{}
		depth := map[InstanceID]int{}
		coverRoot := map[InstanceID]int{}
		for _, pl := range fresh.plans {
			if dead[pl.rootIdx] || pl.loser {
				continue
			}
			n := fresh.nodes[pl.root.Instance]
			if n == nil {
				continue
			}
			walkCover(n, pl.rootIdx, 0, cover, depth, coverRoot, fresh, pl.root)
		}

		// 在全新投影上模拟全部被覆盖的胜出步。
		v := newView(e.store)
		for _, id := range fresh.touched {
			if snap, ok := e.store.snapshotInstanceLocked(id); ok {
				v.ensureStage(snap)
			}
		}

		stepOrder := make([]*step, 0, len(cover))
		for _, n := range cover {
			stepOrder = append(stepOrder, n.step)
		}
		sortSteps(stepOrder)
		sortStepsByDepth(stepOrder, depth)

		results := map[InstanceID]*stepResult{}
		for _, st := range stepOrder {
			stage := v.stages[st.target]
			if st.cascadeOf == "" {
				if err := v.applyAttrs(stage, st.req.Attrs); err != nil {
					results[st.target] = &stepResult{st: st,
						err: newErr(ErrUndeclared, st.target, st.rule.Name, err.Error())}
					continue
				}
				if err := v.applyLinks(stage, st.req.Links); err != nil {
					results[st.target] = &stepResult{st: st,
						err: newErr(ErrUndeclared, st.target, st.rule.Name, err.Error())}
					continue
				}
			}

			terminalErr := func() *LifecycleError {
				t := e.store.types[stage.before.Type]
				if t.IsTerminal(stage.before.State) {
					return newErr(ErrTerminal, st.target, st.rule.Name,
						"instance is in terminal state "+string(stage.before.State))
				}
				return nil
			}()

			preOK, preReasons := v.checkPreconditions(st.target, st.rule)
			v.markState(stage, st.rule.To)
			postErr, postReasons := v.checkPost(st.target, st.rule)
			reasons := append(preReasons, postReasons...)

			var err *LifecycleError
			switch {
			case !preOK:
				err = newErr(ErrPrecondition, st.target, st.rule.Name,
					"precondition not satisfied")
			case postErr != nil:
				err = postErr
			case terminalErr != nil:
				err = terminalErr
			}
			results[st.target] = &stepResult{st: st, err: err, reasons: reasons}
		}

		// 每个存活根沿自身覆盖链汇总错误（固定优先级取最严重者）。
		newlyDead := map[int]*LifecycleError{}
		for _, pl := range fresh.plans {
			if dead[pl.rootIdx] || pl.loser {
				continue
			}
			var worst *LifecycleError
			for id, root := range coverRoot {
				if root != pl.rootIdx {
					continue
				}
				if rr := results[id]; rr.err != nil &&
					(worst == nil || rr.err.Code < worst.Code) {
					worst = rr.err
				}
			}
			if worst != nil {
				newlyDead[pl.rootIdx] = &LifecycleError{
					Code: worst.Code, Target: pl.root.Instance,
					Rule: pl.root.Rule, Detail: worst.Detail,
				}
			}
		}

		// 仲裁落败根：在独立投影上评估自身前置条件与迁移后校验，自身存在
		// 比 ErrMutex 更高优先级（仅未声明/前置条件/循环等）的问题时优先报告；
		// 胜出方本轮因这些原因失败时跟随。基数/钩子是胜出迁移自身的责任，
		// 落败迁移不会发生，因此落败根不在此做迁移后假设。
		for _, pl := range fresh.plans {
			if dead[pl.rootIdx] || !pl.loser {
				continue
			}
			n := fresh.nodes[pl.root.Instance]
			_ = n
			for wi, werr := range newlyDead {
				wp := fresh.plans[wi]
				if !wp.loser && wp.root.Instance == pl.root.Instance {
					// 只跟随比 ErrMutex 更高优先级、且与“能否触发”相关的
					// 胜出方错误（未声明/前置条件/循环）；基数/钩子/终态是
					// 胜出迁移自身的责任，落败根保持未决，最终报互斥。
					if werr.Code > ErrMutex {
						continue
					}
					newlyDead[pl.rootIdx] = &LifecycleError{
						Code: werr.Code, Target: pl.root.Instance,
						Rule: pl.root.Rule, Detail: werr.Detail,
					}
				}
			}
		}

		if len(newlyDead) == 0 {
			// 固定点到达：仍未决的落败根 → ErrMutex。
			for _, pl := range fresh.plans {
				if pl.loser && !dead[pl.rootIdx] {
					rootErr[pl.rootIdx] = newErr(ErrMutex, pl.root.Instance, pl.root.Rule,
						"instance claimed by a higher-priority transition in this unit")
					dead[pl.rootIdx] = true
				}
			}
			active := make([]*step, 0, len(cover))
			for _, n := range cover {
				active = append(active, n.step)
			}
			sortSteps(active)
			return rootErr, active, v, results
		}

		for i, werr := range newlyDead {
			dead[i] = true
			rootErr[i] = werr
		}
	}

	// 不可达：保守全部拒绝。
	return rootErr, nil, newView(e.store), map[InstanceID]*stepResult{}
}

func walkCover(n *node, root, d int, cover map[InstanceID]*node,
	depth map[InstanceID]int, coverRoot map[InstanceID]int,
	fresh *buildResult, ownReq *TransitionRequest) {
	if _, seen := cover[n.id]; seen {
		return
	}
	cover[n.id] = n
	depth[n.id] = d
	coverRoot[n.id] = root
	for _, ed := range n.out {
		// 级联边指向一个被别的直接根显式迁移的实例：丢弃该边，
		// 不把那个实例纳入本根的覆盖链。
		if ed.claimConflict {
			continue
		}
		if ed.to.step != nil {
			walkCover(ed.to, root, d+1, cover, depth, coverRoot, fresh, ownReq)
		}
	}
}

func sortStepsByDepth(steps []*step, depth map[InstanceID]int) {
	for i := 1; i < len(steps); i++ {
		for j := i; j > 0; j-- {
			a, b := steps[j-1], steps[j]
			da, db := depth[a.target], depth[b.target]
			if da < db || (da == db && a.idx < b.idx) {
				break
			}
			steps[j-1], steps[j] = b, a
		}
	}
}
