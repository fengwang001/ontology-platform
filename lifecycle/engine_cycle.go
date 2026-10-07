package lifecycle

import "sort"

// diagnoseCycle 在乐观投影（假设循环计划中的各步都生效）上评估链上各步，
// 返回比 ErrCycle 优先级更高的错误（ErrPrecondition/ErrCardinality/ErrHook/
// ErrTerminal）；不存在则返回 nil，由调用方沿用 ErrCycle。
func diagnoseCycle(e *Engine, fresh *buildResult, pl *plan) *LifecycleError {
	dv := newView(e.store)
	for _, id := range fresh.touched {
		if snap, ok := e.store.snapshotInstanceLocked(id); ok {
			dv.ensureStage(snap)
		}
	}
	n := fresh.nodes[pl.root.Instance]
	if n == nil {
		return nil
	}
	var best *LifecycleError
	consider := func(err *LifecycleError) {
		if err != nil && (best == nil || err.Code < best.Code) {
			best = err
		}
	}
	visited := map[InstanceID]bool{}
	var walk func(*node)
	walk = func(cur *node) {
		if visited[cur.id] || cur.step == nil || cur.step.rule == nil {
			return
		}
		visited[cur.id] = true
		st := cur.step
		if stage := dv.stages[st.target]; stage != nil {
			typ := e.store.types[stage.before.Type]
			if st.cascadeOf == "" {
				_ = dv.applyAttrs(stage, st.req.Attrs)
				_ = dv.applyLinks(stage, st.req.Links)
			}
			if ok, _ := dv.checkPreconditions(st.target, st.rule); !ok {
				consider(newErr(ErrPrecondition, st.target, st.rule.Name,
					"precondition not satisfied"))
			}
			dv.markState(stage, st.rule.To)
			if postErr, _ := dv.checkPost(st.target, st.rule); postErr != nil {
				consider(postErr)
			}
			if typ.IsTerminal(stage.before.State) {
				consider(newErr(ErrTerminal, st.target, st.rule.Name,
					"instance is terminal"))
			}
		}
		outs := append([]*edge(nil), cur.out...)
		sort.Slice(outs, func(i, j int) bool {
			if outs[i].to.id != outs[j].to.id {
				return outs[i].to.id < outs[j].to.id
			}
			return outs[i].via < outs[j].via
		})
		for _, ed := range outs {
			walk(ed.to)
		}
	}
	walk(n)
	if best != nil && best.Code < ErrCycle {
		return best
	}
	return nil
}
