package compensate

import "sort"

// NaiveResult 是朴素串行参考模型的输出，用于与并发引擎对拍。
type NaiveResult struct {
	Records     []ErrorRecord
	Final       map[string]map[string]string
	Applied     map[string][]int
	Compensated map[string][]int
	Blocked     []string
}

type naiveApplied struct {
	idx  int
	op   WriteOp
	undo UndoRecord
}

// RunNaive 是独立实现的朴素串行模型，刻意不与并发引擎共享运行期代码。
//
// 生效阶段：取确定的合法拓扑顺序（每轮选名字最小的就绪分支），单 goroutine 逐分支、
// 分支内逐子操作执行；遇到首个自身失败即终止该分支，其后依赖它的分支记为被动失败。
//
// 补偿阶段：仅考虑有已生效副作用的分支，用一个确定的“补偿就绪”循环串行处理：
// 每次选 remaining==0 中名字最小的分支，分支内严格逆序恢复；
// 某分支逆操作失败则记录该子操作，并令其上游祖先永远不就绪（等价于并发调度里的阻塞）。
// 它是“存在至少一个合法拓扑顺序，按该顺序逐一补偿”的具体见证。
func RunNaive(graph *Graph, spec ActionSpec, logger Logger) (*NaiveResult, error) {
	decl, err := validate(spec)
	if err != nil {
		return nil, err
	}
	topo := deterministicTopo(spec, decl.dependents)

	res := &NaiveResult{Applied: map[string][]int{}, Compensated: map[string][]int{}}
	addRec := func(k ErrKind, branchName string, idx int, opID, msg string) {
		res.Records = append(res.Records, ErrorRecord{Kind: k, ActionID: spec.ID,
			Branch: branchName, OpIndex: idx, OpID: opID, Message: msg})
	}

	appliedByBranch := map[string][]naiveApplied{}
	status := map[string]int{} // 0=applied, 1=self failed, 2=upstream failed
	anyFailure := false

	for _, name := range topo {
		b := decl.byName[name]
		upstreamFailed := ""
		for _, dep := range b.Deps {
			if st, ok := status[dep]; ok && st != 0 {
				upstreamFailed = dep
				break
			}
		}
		if upstreamFailed != "" {
			status[name] = 2
			anyFailure = true
			addRec(KindUpstreamFailed, name, -1, "",
				"branch never started: upstream branch "+upstreamFailed+" terminally failed")
			logIf(logger, AttemptEvent{Phase: "apply", ActionID: spec.ID, Branch: name, OpIndex: -1,
				Result: "passively failed; no sub-operation started",
				Reason: "naive: upstream " + upstreamFailed + " failed", Allowed: false})
			continue
		}
		failed := false
		for i := range b.Ops {
			op := b.Ops[i]
			logIf(logger, AttemptEvent{Phase: "apply", ActionID: spec.ID, Branch: name, OpIndex: i, OpID: op.ID,
				Input: op.label(), Result: "naive serial attempt",
				Reason: "dependencies applied; declared order", Allowed: true})
			if op.FailApply {
				status[name] = 1
				anyFailure = true
				failed = true
				addRec(KindSubOperationFailed, name, i, op.ID, "sub-operation failed during apply")
				logIf(logger, AttemptEvent{Phase: "apply", ActionID: spec.ID, Branch: name, OpIndex: i, OpID: op.ID,
					Input: op.label(), Result: "sub-operation failed",
					Reason: "injected apply failure", Allowed: true})
				break
			}
			graph.Acquire(spec.ID, op.ObjectID, op.Property)
			undo := graph.CommitSet(spec.ID, op.ObjectID, op.Property, op.Value)
			appliedByBranch[name] = append(appliedByBranch[name], naiveApplied{idx: i, op: op, undo: undo})
			res.Applied[name] = append(res.Applied[name], i)
		}
		if !failed {
			status[name] = 0
		}
	}

	if anyFailure {
		naiveCompensate(spec, decl, graph, logger, appliedByBranch, res, addRec)
	}
	graph.ReleaseAll(spec.ID)

	res.Final = graph.Snapshot()
	sort.SliceStable(res.Records, func(i, j int) bool {
		if res.Records[i].Kind != res.Records[j].Kind {
			return kindPriority(res.Records[i].Kind) < kindPriority(res.Records[j].Kind)
		}
		if res.Records[i].Branch != res.Records[j].Branch {
			return res.Records[i].Branch < res.Records[j].Branch
		}
		return res.Records[i].OpIndex < res.Records[j].OpIndex
	})
	sort.Strings(res.Blocked)
	return res, nil
}

// naiveCompensate 串行模拟补偿就绪调度。与并发引擎的差别仅在于：每次只挑一个分支、
// 一次只执行一个逆操作，因此其结果天然对应某个合法拓扑顺序。
func naiveCompensate(spec ActionSpec, decl *declaredSpec, graph *Graph, logger Logger,
	appliedByBranch map[string][]naiveApplied, res *NaiveResult,
	addRec func(ErrKind, string, int, string, string)) {

	// 只保留有副作用的分支之间的边。
	remaining := map[string]int{}
	downs := map[string][]string{}
	for name, items := range appliedByBranch {
		if len(items) == 0 {
			continue
		}
		n := 0
		for _, d := range decl.dependents[name] {
			if len(appliedByBranch[d]) > 0 {
				n++
			}
			downs[name] = append(downs[name], d)
		}
		remaining[name] = n
	}

	done := map[string]bool{}
	failedBranch := map[string]bool{}
	for len(done) < len(remaining) {
		cand := []string{}
		for name := range remaining {
			if done[name] {
				continue
			}
			if remaining[name] == 0 {
				cand = append(cand, name)
			}
		}
		if len(cand) == 0 {
			// 剩余分支均被失败的下游阻塞。
			for name := range remaining {
				if !done[name] {
					res.Blocked = append(res.Blocked, name)
					done[name] = true
				}
			}
			break
		}
		sort.Strings(cand)
		name := cand[0]
		items := appliedByBranch[name]
		failedAny := false
		for i := len(items) - 1; i >= 0; i-- {
			it := items[i]
			logIf(logger, AttemptEvent{Phase: "compensate", ActionID: spec.ID, Branch: name, OpIndex: it.idx, OpID: it.op.ID,
				Input: "inverse of " + it.op.label(), Result: "naive serial attempt",
				Reason: "downstream done; reverse order within branch", Allowed: true})
			if it.op.FailCompensate {
				failedAny = true
				failedBranch[name] = true
				addRec(KindInverseFailed, name, it.idx, it.op.ID,
					"inverse operation failed; effect at this slot remains")
				logIf(logger, AttemptEvent{Phase: "compensate", ActionID: spec.ID, Branch: name, OpIndex: it.idx, OpID: it.op.ID,
					Input: "inverse of " + it.op.label(), Result: "inverse operation failed",
					Reason: "injected compensate failure", Allowed: true})
				continue
			}
			graph.Restore(spec.ID, it.undo)
			res.Compensated[name] = append(res.Compensated[name], it.idx)
		}
		sort.Ints(res.Compensated[name])
		done[name] = true
		if !failedAny {
			for _, up := range decl.byName[name].Deps {
				if _, ok := remaining[up]; ok {
					remaining[up]--
				}
			}
		} else {
			// 失败分支不向上游发“完成”信号；其上游祖先永不就绪，最终进入 Blocked。
			for up := range ancestors(name, decl.byName) {
				if _, ok := remaining[up]; ok {
					remaining[up]++ // 保证 >0，不会被误选
				}
			}
		}
	}
}

func logIf(logger Logger, ev AttemptEvent) {
	if logger == nil {
		return
	}
	logger.LogAttempt(ev)
}

// deterministicTopo 返回每轮取名字最小就绪分支的确定性拓扑顺序。
func deterministicTopo(spec ActionSpec, dependents map[string][]string) []string {
	indeg := map[string]int{}
	for _, b := range spec.Branches {
		indeg[b.Name] = len(b.Deps)
	}
	ready := []string{}
	for _, b := range spec.Branches {
		if indeg[b.Name] == 0 {
			ready = append(ready, b.Name)
		}
	}
	out := []string{}
	for len(ready) > 0 {
		sort.Strings(ready)
		cur := ready[0]
		ready = ready[1:]
		out = append(out, cur)
		for _, d := range dependents[cur] {
			indeg[d]--
			if indeg[d] == 0 {
				ready = append(ready, d)
			}
		}
	}
	return out
}
