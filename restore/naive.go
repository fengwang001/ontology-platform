package restore

// naiveVerdict 是“按依赖关系逐步独立判定”的朴素参照模型。
//
// 它故意不使用裁决组件的任何内部结构（不建依赖图、不做记忆化、
// 不做屏障推断），只按题面规则逐条递归核对：
//
//	类型：类不可用 -> 死；否则按自身状态。
//	对象：类不可用 -> 死；自身损坏 -> 死；其类型定义不可恢复 -> 死。
//	链接：类不可用 -> 死；自身损坏 -> 死；两端对象任一不可恢复 -> 死。
//	动作：类不可用 -> 死；自身损坏 -> 死；涉及对象/链接任一不可恢复 -> 死。
//
// 朴素模型对同一快照用不动点迭代求闭包（直到没有新结论），
// 因此天然与具体求解顺序无关；环上节点在朴素模型中永远无法被确认
// （它的前提无法闭合），对应裁决组件的 ReasonCycle。
// ExtraDeps 以“额外的逐对依赖”形式纳入迭代。
type naiveVerdict struct {
	dead map[RecordID]bool
	// reason 仅区分三大来源：class/self/dependency/cycle/broken。
	reason map[RecordID]ReasonCode
}

// naiveAdjudicate 运行朴素参照判定。
func naiveAdjudicate(snap *Snapshot) *naiveVerdict {
	nv := &naiveVerdict{
		dead:   map[RecordID]bool{},
		reason: map[RecordID]ReasonCode{},
	}
	g := buildGraph(snap)
	known := map[RecordID]bool{}
	for id := range g.nodes {
		known[id] = true
	}

	// 种子：类整体不可用 / 自身损坏 / 悬空引用。
	statuses := classifyClasses(snap)
	for _, id := range allRecordIDs(snap) {
		switch {
		case statuses[id.Class] == ClassUnavailable:
			nv.dead[id] = true
			nv.reason[id] = ReasonClassUnavailable
		default:
			st, ok := recordState(snap, id)
			if ok && st == StateCorrupt {
				nv.dead[id] = true
				nv.reason[id] = ReasonSelfCorrupt
			}
		}
	}
	// 悬空引用直接让引用方不可恢复（断引用也是一种依赖失败）。
	// 先收集所有“有悬空依赖”的节点。
	for from, deps := range g.deps {
		if !known[from] {
			continue
		}
		for _, to := range deps {
			if !known[to] {
				if !nv.dead[from] {
					nv.dead[from] = true
					nv.reason[from] = ReasonReferenceBroken
				}
			}
		}
	}

	// 不动点：任意依赖已死 -> 该节点死（级联）。
	for {
		changed := false
		for from := range known {
			if nv.dead[from] {
				continue
			}
			for _, to := range g.deps[from] {
				if nv.dead[to] {
					nv.dead[from] = true
					nv.reason[from] = ReasonDependencyUnavailable
					changed = true
					break
				}
			}
		}
		if !changed {
			break
		}
	}

	// 仍未判死、但存在依赖闭环（依赖闭包无法到达已知起点）的节点
	// 即为循环相关节点：朴素模型永远无法确认它们。
	cycleNodes := map[RecordID]bool{}
	for id := range known {
		if nv.dead[id] {
			continue
		}
		naiveFindBackEdges(id, g, nv.dead, map[RecordID]int{}, cycleNodes)
	}
	// 第二遍：从已确认的环成员出发，沿依赖反向传播，
	// 任何依赖环成员的节点也无法确认。
	reverse := map[RecordID][]RecordID{}
	for from, deps := range g.deps {
		for _, to := range deps {
			reverse[to] = append(reverse[to], from)
		}
	}
	stack := make([]RecordID, 0, len(cycleNodes))
	for id := range cycleNodes {
		stack = append(stack, id)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range reverse[cur] {
			if nv.dead[next] || cycleNodes[next] {
				continue
			}
			cycleNodes[next] = true
			stack = append(stack, next)
		}
	}
	for id := range cycleNodes {
		nv.dead[id] = true
		nv.reason[id] = ReasonCycle
	}
	return nv
}

// naiveFindBackEdges 在未判死子图上用三色 DFS 找回溯边。
// 回溯边指向的栈上节点是环成员。DFS 每条边恰好访问一次，
// 且不因某次命中而提前关闭同栈节点的颜色。
func naiveFindBackEdges(id RecordID, g *depGraph, dead map[RecordID]bool, color map[RecordID]int, cycleNodes map[RecordID]bool) {
	if dead[id] || color[id] != 0 {
		return
	}
	color[id] = 1
	for _, dep := range g.deps[id] {
		if dead[dep] || !g.nodes[dep] {
			continue
		}
		switch color[dep] {
		case 0:
			naiveFindBackEdges(dep, g, dead, color, cycleNodes)
		case 1:
			cycleNodes[dep] = true
		}
	}
	color[id] = 2
}

// agreesWith 比较朴素模型与正式裁决在“可恢复范围”上是否一致。
// 原因码做等价类归并：级联/类不可用/断引用都属于“依赖核对不通过”，
// 环单独区分；正式裁决允许把类不可用沿链保持为 ClassUnavailable，
// 这与朴素模型的 DependencyUnavailable 在“是否可恢复”层面等价。
func (nv *naiveVerdict) agreesWith(v *Verdict) (bool, []RecordID) {
	var mismatches []RecordID
	for id := range v.Records {
		formalDead := !v.Records[id].Recoverable
		naiveDead := nv.dead[id]
		if formalDead != naiveDead {
			mismatches = append(mismatches, id)
			continue
		}
		// 双端都判死时，环与非环必须一致区分。
		if formalDead {
			formalCycle := v.Records[id].Reasons[0] == ReasonCycle
			naiveCycle := nv.reason[id] == ReasonCycle
			if formalCycle != naiveCycle {
				mismatches = append(mismatches, id)
			}
		}
	}
	return len(mismatches) == 0, mismatches
}
