package lifecycle

import "sync/atomic"

// Engine 是生命周期状态机子系统的入口。一次 Execute 调用是一个
// 处理单元：计划展开 → 循环检测 → 多实例加锁 → 统一判定 →
// 整体提交或整体放弃。
type Engine struct {
	store *Store
	log   DecisionLog
	seq   uint64
}

func NewEngine(s *Store, log DecisionLog) *Engine {
	return &Engine{store: s, log: log}
}

// Outcome 是单个根请求的处理结果。
type Outcome struct {
	Request TransitionRequest
	Err     *LifecycleError
}

func (o Outcome) OK() bool { return o.Err == nil }

// Execute 在一个处理单元内原子地处理一批迁移请求。
// 被拒绝的请求不改变任何实例的状态、属性、链接或时钟戳；
// 链式触发整体生效或整体不生效。
func (e *Engine) Execute(reqs ...TransitionRequest) []Outcome {
	outcomes := make([]Outcome, len(reqs))
	for i := range reqs {
		outcomes[i] = Outcome{Request: reqs[i]}
	}
	if len(reqs) == 0 {
		return outcomes
	}

	// 乐观阶段仅用于发现需要加锁的实例集合。注意加锁顺序：
	// 必须先取实例锁、再取 Store RLock，避免与提交路径
	// （实例锁 → Store Lock）形成环。
	touched := func() []InstanceID {
		e.store.RLock()
		defer e.store.RUnlock()
		return append([]InstanceID(nil), newPlanner(e.store).Build(reqs).touched...)
	}()

	// 锁内重建权威计划；若两次展开之间有并发提交改变了图（出现新的
	// 关联实例），则扩大锁集合重试。最终判定全部在锁定快照上完成。
	var fresh *buildResult
	var unlock func()
	for {
		unlock = e.store.lockAll(touched)
		e.store.RLock()
		fresh = newPlanner(e.store).Build(reqs)
		missing := false
		locked := map[InstanceID]bool{}
		for _, id := range touched {
			locked[id] = true
		}
		for _, id := range fresh.touched {
			if !locked[id] {
				missing = true
				touched = append(touched, id)
			}
		}
		if !missing {
			break
		}
		e.store.RUnlock()
		unlock()
	}
	defer unlock()

	// 统一判定：在同一投影上做前置条件、迁移后基数与跨实例钩子校验，
	// 并以固定点方式处理“被拒绝环节撤出投影”，返回每个根的错误与
	// 最终允许生效的胜出步。
	rootErrs, active, v, results := e.evaluate(fresh)

	// 提交：仅对 active 的胜出步写回真实 Store，整体在 Store 写锁内完成。
	e.store.RUnlock()
	e.store.Lock()
	clock := e.store.nextClockLocked()
	committed := map[InstanceID]bool{}
	for _, st := range active {
		stage := v.stages[st.target]
		inst := e.store.instances[st.target]
		if inst == nil {
			continue
		}
		inst.State = stage.state
		inst.Attrs = cloneAttrs(stage.attrs)
		inst.Clock = clock
		inst.Version++
		e.applyLinksCommitLocked(st.target, stage)
		committed[st.target] = true
	}
	e.store.Unlock()

	// 日志与结果。
	for i, pl := range fresh.plans {
		err := rootErrs[i]
		if err == nil && !pl.loser && !committed[pl.root.Instance] {
			if pl.loser {
				err = newErr(ErrMutex, pl.root.Instance, pl.root.Rule,
					"higher-priority transition won this unit")
			} else {
				err = newErr(ErrUndeclared, pl.root.Instance, pl.root.Rule,
					"transition did not take effect")
			}
		}
		outcomes[i].Err = err
		e.recordLog(fresh, pl, results, err, committed)
	}
	return outcomes
}

func cloneAttrs(in map[AttrKey]AttrValue) map[AttrKey]AttrValue {
	out := make(map[AttrKey]AttrValue, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// applyLinksCommitLocked 把暂定链接增删写回 Store（调用方持有 Store 写锁）。
func (e *Engine) applyLinksCommitLocked(src InstanceID, st *stage) {
	for link, dsts := range st.linkAdd {
		if e.store.links[src] == nil {
			e.store.links[src] = map[LinkType]map[InstanceID]struct{}{}
		}
		if e.store.links[src][link] == nil {
			e.store.links[src][link] = map[InstanceID]struct{}{}
		}
		for dst := range dsts {
			e.store.links[src][link][dst] = struct{}{}
			if e.store.back[dst] == nil {
				e.store.back[dst] = map[LinkType]map[InstanceID]struct{}{}
			}
			if e.store.back[dst][link] == nil {
				e.store.back[dst][link] = map[InstanceID]struct{}{}
			}
			e.store.back[dst][link][src] = struct{}{}
			if dstInst := e.store.instances[dst]; dstInst != nil {
				dstInst.Version++
			}
		}
	}
	for link, dsts := range st.linkDel {
		if m := e.store.links[src]; m != nil {
			for dst := range dsts {
				delete(m[link], dst)
				if b := e.store.back[dst]; b != nil {
					delete(b[link], src)
				}
				if dstInst := e.store.instances[dst]; dstInst != nil {
					dstInst.Version++
				}
			}
		}
	}
}

func (e *Engine) recordLog(_ *buildResult, pl *plan, results map[InstanceID]*stepResult, err *LifecycleError, committed map[InstanceID]bool) {
	if e.log == nil {
		return
	}
	seq := atomic.AddUint64(&e.seq, 1)
	from, to := State(""), State("")
	reasons := []string{}
	if rr := results[pl.root.Instance]; rr != nil {
		from = rr.st.fromState
		to = rr.st.rule.To
		reasons = rr.reasons
	}
	e.log.Record(LogEntry{
		Seq:       seq,
		Request:   *pl.root,
		Accepted:  err == nil && committed[pl.root.Instance],
		FromState: from,
		ToState:   to,
		Reasons:   reasons,
		Error:     err,
	})
}

func sortSteps(steps []*step) {
	for i := 1; i < len(steps); i++ {
		for j := i; j > 0 && steps[j-1].idx > steps[j].idx; j-- {
			steps[j-1], steps[j] = steps[j], steps[j-1]
		}
	}
}
